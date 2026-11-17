// Package cards models Indian credit-card reward rules (caps, exclusions, redemption value, fee waivers)
// and finds the best 1- or 2-card combination for a monthly spend profile. Money is int64 paise.
package cards

import (
	"sort"

	"github.com/shopspring/decimal"
)

type Category string

const (
	Dining         Category = "DINING"
	Groceries      Category = "GROCERIES"
	FlightsHotels  Category = "FLIGHTS_HOTELS"
	Utilities      Category = "UTILITIES"
	Fuel           Category = "FUEL"
	OnlineShopping Category = "ONLINE_SHOPPING"
	OfflinePOS     Category = "OFFLINE_POS"
	Excluded       Category = "EXCLUDED"
)

var AllCategories = []Category{Dining, Groceries, FlightsHotels, Utilities, Fuel, OnlineShopping, OfflinePOS, Excluded}

func (c Category) Valid() bool {
	for _, x := range AllCategories {
		if x == c {
			return true
		}
	}
	return false
}

type CategoryRule struct {
	EarnBps  int64  `json:"earnBps"` // points per Rs1 x 10_000 (5% cashback at 1pt=Rs1 => 500)
	Excluded bool   `json:"excluded"`
	CapGroup string `json:"capGroup"` // categories sharing one monthly cap use the same group
}

type Card struct {
	ID                  string                    `json:"id"`
	Name                string                    `json:"name"`
	AnnualFeePaise      int64                     `json:"annualFeePaise"`
	FeeWaiverSpendPaise int64                     `json:"feeWaiverSpendPaise"` // 0 = no waiver
	BaseEarnBps         int64                     `json:"baseEarnBps"`
	RedeemPaisePerPoint int64                     `json:"redeemPaisePerPoint"` // 100 = Rs1/pt, 25 = Rs0.25/pt
	Rules               map[Category]CategoryRule `json:"rules"`
	GroupCapPoints      map[string]int64          `json:"groupCapPoints"` // per statement cycle
}

type Spend map[Category]int64 // monthly paise

type CardYield struct {
	CardID            string `json:"cardId"`
	AnnualRewardPaise int64  `json:"annualRewardPaise"`
	LostToCapsPaise   int64  `json:"lostToCapsPaise"`
	FeePaise          int64  `json:"feePaise"`
	NetPaise          int64  `json:"netPaise"`
}

func (c Card) rule(cat Category) CategoryRule {
	if r, ok := c.Rules[cat]; ok {
		return r
	}
	return CategoryRule{EarnBps: c.BaseEarnBps}
}

// grant is pure: points earned for `spend` given points already used in each cap group this cycle.
// raw is what would have been earned without caps.
func (c Card) grant(cat Category, spend int64, used map[string]int64) (pts, raw int64) {
	r := c.rule(cat)
	if cat == Excluded || r.Excluded {
		return 0, 0
	}
	raw = decimal.NewFromInt(spend).Mul(decimal.NewFromInt(r.EarnBps)).
		Div(decimal.NewFromInt(1_000_000)).Floor().IntPart()
	capPts, capped := c.GroupCapPoints[r.CapGroup]
	if r.CapGroup == "" || !capped {
		return raw, raw
	}
	return min(raw, max(0, capPts-used[r.CapGroup])), raw
}

func sortedCats(m Spend, less func(a, b Category) bool) []Category {
	cats := make([]Category, 0, len(m))
	for k, v := range m {
		if v > 0 {
			cats = append(cats, k)
		}
	}
	sort.Slice(cats, func(i, j int) bool {
		if less(cats[i], cats[j]) {
			return true
		}
		if less(cats[j], cats[i]) {
			return false
		}
		return cats[i] < cats[j] // deterministic tie-break
	})
	return cats
}

// NetYield is the annual net rupees from one card for a monthly spend profile.
// Caps apply per statement cycle, so one month is computed and multiplied by 12.
// holdFee: the user already holds the card and pays the fee even with zero spend.
func NetYield(c Card, monthly Spend, holdFee bool) CardYield {
	// Highest earn-rate first so shared caps are consumed by the most valuable spend.
	cats := sortedCats(monthly, func(a, b Category) bool { return c.rule(a).EarnBps > c.rule(b).EarnBps })

	used := map[string]int64{}
	var pts, raw, eligible, total int64
	for _, cat := range cats {
		s := monthly[cat]
		total += s
		p, r := c.grant(cat, s, used)
		pts, raw = pts+p, raw+r
		rl := c.rule(cat)
		if rl.CapGroup != "" {
			used[rl.CapGroup] += p
		}
		if cat != Excluded && !rl.Excluded {
			eligible += s
		}
	}
	y := CardYield{CardID: c.ID}
	y.AnnualRewardPaise = pts * c.RedeemPaisePerPoint * 12
	y.LostToCapsPaise = (raw - pts) * c.RedeemPaisePerPoint * 12
	waived := c.FeeWaiverSpendPaise > 0 && eligible*12 >= c.FeeWaiverSpendPaise
	if (total > 0 || holdFee) && !waived {
		y.FeePaise = c.AnnualFeePaise
	}
	y.NetPaise = y.AnnualRewardPaise - y.FeePaise
	return y
}

// Allocate splits each category's spend across cards in Rs500 chunks, always to the card with the best
// marginal reward given its remaining cap. It is a marginal-greedy heuristic, not a proof of optimality.
func Allocate(cards []Card, spend Spend) map[string]Spend {
	const chunk = 50_000
	out := map[string]Spend{}
	used := map[string]map[string]int64{}
	for _, c := range cards {
		out[c.ID], used[c.ID] = Spend{}, map[string]int64{}
	}
	if len(cards) == 0 {
		return out
	}
	best := func(cat Category) (b int64) {
		for _, c := range cards {
			b = max(b, c.rule(cat).EarnBps*c.RedeemPaisePerPoint)
		}
		return
	}
	for _, cat := range sortedCats(spend, func(a, b Category) bool { return best(a) > best(b) }) {
		for left := spend[cat]; left > 0; {
			part := min(chunk, left)
			pick, pickVal := 0, int64(-1)
			for i, c := range cards {
				p, _ := c.grant(cat, part, used[c.ID])
				if v := p * c.RedeemPaisePerPoint; v > pickVal {
					pick, pickVal = i, v
				}
			}
			c := cards[pick]
			p, _ := c.grant(cat, part, used[c.ID])
			if g := c.rule(cat).CapGroup; g != "" {
				used[c.ID][g] += p
			}
			out[c.ID][cat] += part
			left -= part
		}
	}
	return out
}

func evaluate(cards []Card, spend Spend, held bool) (int64, []CardYield) {
	alloc := Allocate(cards, spend)
	var net int64
	ys := make([]CardYield, 0, len(cards))
	for _, c := range cards {
		y := NetYield(c, alloc[c.ID], held)
		net += y.NetPaise
		ys = append(ys, y)
	}
	return net, ys
}

// Evaluate returns total net annual paise and per-card yields for a wallet the user already holds.
func Evaluate(cards []Card, spend Spend) (int64, []CardYield) { return evaluate(cards, spend, true) }

type ComboResult struct {
	CardIDs       []string    `json:"cardIds"`
	Yields        []CardYield `json:"yields"`
	NetPaise      int64       `json:"netPaise"`
	BaselinePaise int64       `json:"baselinePaise"`
	GainPaise     int64       `json:"netAnnualRupeesGainedPaise"`
}

// BestCombo tries every 1-card and 2-card subset of the catalog and compares against `current`.
func BestCombo(catalog, current []Card, spend Spend) ComboResult {
	base, _ := evaluate(current, spend, true)
	res := ComboResult{BaselinePaise: base, CardIDs: []string{}, Yields: []CardYield{}}
	if len(catalog) == 0 {
		res.NetPaise = base
		return res
	}
	res.NetPaise = -1 << 62
	try := func(cs ...Card) {
		if n, ys := evaluate(cs, spend, false); n > res.NetPaise {
			res.NetPaise, res.Yields, res.CardIDs = n, ys, nil
			for _, c := range cs {
				res.CardIDs = append(res.CardIDs, c.ID)
			}
		}
	}
	for i := range catalog {
		try(catalog[i])
		for j := i + 1; j < len(catalog); j++ {
			try(catalog[i], catalog[j])
		}
	}
	res.GainPaise = res.NetPaise - base
	return res
}
