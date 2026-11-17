package mf

import (
	"time"

	"github.com/shopspring/decimal"
)

type TaxConfig struct {
	STCGBps            int64 // 2000
	LTCGBps            int64 // 1250
	CessBps            int64 // 400 (health & education cess); set 0 for the raw 20% / 12.5%
	SlabBps            int64 // user's marginal slab, used for non-equity funds
	LTCGExemptionPaise int64 // 125_000_00 per FY
	LongTermDays       int   // 365: equity units held MORE than 12 months are long-term
	MaxPaybackMonths   int64 // consolidate only if exit cost is recovered by fee savings within this
}

func DefaultTaxConfig(slabBps int64) TaxConfig {
	return TaxConfig{
		STCGBps: 2000, LTCGBps: 1250, CessBps: 400, SlabBps: slabBps,
		LTCGExemptionPaise: 125_000_00, LongTermDays: 365, MaxPaybackMonths: 12,
	}
}

// bps returns paise*rate/10000 rounded to the nearest paise.
func bps(paise, rate int64) int64 {
	return decimal.NewFromInt(paise).Mul(decimal.NewFromInt(rate)).
		Div(decimal.NewFromInt(10_000)).Round(0).IntPart()
}

func daysBetween(a, b time.Time) int { return int(b.Sub(a).Hours() / 24) }

// fy returns the starting calendar year of the Indian financial year containing t.
func fy(t time.Time) int {
	if t.Month() >= time.April {
		return t.Year()
	}
	return t.Year() - 1
}

type ExitCost struct {
	STCGTaxPaise  int64 `json:"stcgTaxPaise"`
	LTCGTaxPaise  int64 `json:"ltcgTaxPaise"`
	ExitLoadPaise int64 `json:"exitLoadPaise"`
	TotalPaise    int64 `json:"totalPaise"`
	// ExemptionUsedPaise is how much of the Rs1.25L LTCG exemption this exit consumes (for sharing it across exits).
	ExemptionUsedPaise int64 `json:"exemptionUsedPaise"`
}

// grandfatherCutoff: units acquired BEFORE 1 Feb 2018 get a stepped-up cost of acquisition.
var grandfatherCutoff = time.Date(2018, 2, 1, 0, 0, 0, 0, time.UTC)

// grandfatheredCost applies Sec. 55(2)(ac): cost = max(actual cost, min(FMV on 31-Jan-2018, sale value)).
// Without a known FMV the actual cost is used (tax can only be overstated; see grandfatherNote).
func grandfatheredCost(f Fund, l Lot) int64 {
	if !f.EquityOriented || !l.PurchaseDate.Before(grandfatherCutoff) || f.GrandfatherNAV.IsZero() {
		return l.CostPaise
	}
	fmv := l.Units.Mul(f.GrandfatherNAV).Mul(decimal.NewFromInt(100)).Round(0).IntPart()
	return max(l.CostPaise, min(fmv, l.ValuePaise))
}

// grandfatherNote explains when a pre-2018 lot could not be stepped up because the fund has no 31-Jan-2018 NAV.
func grandfatherNote(f Fund, lots []Lot) string {
	if !f.EquityOriented || !f.GrandfatherNAV.IsZero() {
		return ""
	}
	for _, l := range lots {
		if l.PurchaseDate.Before(grandfatherCutoff) {
			return "Units bought before Feb 2018 are taxed on actual cost because the 31-Jan-2018 NAV for " + f.ISIN + " is not loaded; tax may be overstated."
		}
	}
	return ""
}

func (t TaxConfig) withCess(tax int64) int64 { return bps(tax, 10_000+t.CessBps) }

// exitCost is the cost of fully redeeming lots at time `at`, assuming NAV stays at today's value.
// exemptionLeft is the unused part of the Rs1.25L LTCG exemption for the FY containing `at`.
func (t TaxConfig) exitCost(f Fund, lots []Lot, at time.Time, exemptionLeft int64) ExitCost {
	var st, lt, load int64
	for _, l := range lots {
		age := daysBetween(l.PurchaseDate, at)
		if f.ExitLoadDays > 0 && age < f.ExitLoadDays {
			load += bps(l.ValuePaise, f.ExitLoadBps)
		}
		if age > t.LongTermDays {
			lt += l.ValuePaise - grandfatheredCost(f, l)
		} else {
			st += l.ValuePaise - l.CostPaise
		}
	}
	c := ExitCost{ExitLoadPaise: load}
	if !f.EquityOriented {
		// Non-equity funds: gains taxed at slab rate regardless of holding period (post-Apr-2023 purchases).
		if g := st + lt; g > 0 {
			c.STCGTaxPaise = t.withCess(bps(g, t.SlabBps))
		}
	} else {
		// Set-off: a loss in one bucket reduces the gain in the other.
		switch {
		case st < 0 && lt > 0:
			lt, st = lt+st, 0
		case lt < 0 && st > 0:
			st, lt = st+lt, 0
		}
		if st > 0 {
			c.STCGTaxPaise = t.withCess(bps(st, t.STCGBps))
		}
		c.ExemptionUsedPaise = min(max(lt, 0), exemptionLeft)
		if taxable := lt - exemptionLeft; taxable > 0 {
			c.LTCGTaxPaise = t.withCess(bps(taxable, t.LTCGBps))
		}
	}
	c.TotalPaise = c.STCGTaxPaise + c.LTCGTaxPaise + c.ExitLoadPaise
	return c
}

// daysToAllLongTerm is how many days until every short-term lot becomes long-term.
func (t TaxConfig) daysToAllLongTerm(lots []Lot, now time.Time) int {
	w := 0
	for _, l := range lots {
		if age := daysBetween(l.PurchaseDate, now); age <= t.LongTermDays {
			w = max(w, t.LongTermDays+1-age)
		}
	}
	return w
}

type PairAction struct {
	Verdict           Verdict    `json:"verdict"`
	ExitFundIndex     int        `json:"exitFundIndex"`
	KeepFundIndex     int        `json:"keepFundIndex"`
	ExitNow           ExitCost   `json:"exitNow"`
	AnnualSavingPaise int64      `json:"annualSavingPaise"`
	HoldUntil         *time.Time `json:"holdUntil,omitempty"`
	Notes             []string   `json:"notes,omitempty"`
}

func (t TaxConfig) paybackOK(cost, annualSaving int64) bool {
	return cost*12 <= t.MaxPaybackMonths*annualSaving
}

// Judge decides what to do with the higher-TER fund of an overlapping pair.
// realisedLTCG is long-term gain already booked this FY (it eats into the Rs1.25L exemption).
func (t TaxConfig) Judge(exit, keep Fund, pos Position, now time.Time, realisedLTCG int64) PairAction {
	saving := bps(pos.ValuePaise(), exit.TERBps-keep.TERBps)
	act := PairAction{Verdict: Keep, AnnualSavingPaise: saving}
	if n := grandfatherNote(exit, pos.Lots); n != "" {
		act.Notes = append(act.Notes, n)
	}
	if saving <= 0 {
		return act
	}
	left := max(0, t.LTCGExemptionPaise-realisedLTCG)
	act.ExitNow = t.exitCost(exit, pos.Lots, now, left)

	if wait := t.daysToAllLongTerm(pos.Lots, now); wait > 0 {
		later := now.AddDate(0, 0, wait)
		laterLeft := left
		if fy(later) != fy(now) { // a new financial year restores the full exemption
			laterLeft = t.LTCGExemptionPaise
		}
		lc := t.exitCost(exit, pos.Lots, later, laterLeft)
		waitCost := saving * int64(wait) / 365 // fee drag while waiting
		if act.ExitNow.TotalPaise-lc.TotalPaise > waitCost && t.paybackOK(lc.TotalPaise, saving) {
			act.Verdict, act.HoldUntil = HoldUntilLTCG, &later
			return act
		}
	}
	if t.paybackOK(act.ExitNow.TotalPaise, saving) {
		act.Verdict = ConsolidateNow
	}
	return act
}
