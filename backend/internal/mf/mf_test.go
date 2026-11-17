package mf

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }

func eqFund(isin string, ter int64, h map[string]int64) Fund {
	return Fund{ISIN: isin, Name: isin, Plan: PlanRegular, TERBps: ter, EquityOriented: true, Holdings: h}
}

func lot(buy time.Time, costRs, valueRs int64) Lot {
	return Lot{PurchaseDate: buy, Units: decimal.NewFromInt(100), CostPaise: costRs * 100, ValuePaise: valueRs * 100}
}

func TestOverlapMinIntersection(t *testing.T) {
	a := eqFund("A", 100, map[string]int64{"X": 500, "Y": 300})
	b := eqFund("B", 100, map[string]int64{"X": 200, "Y": 400, "Z": 100})
	tot, shared := Overlap(a, b)
	if tot != 500 { // min(500,200)+min(300,400)
		t.Fatalf("overlap = %d, want 500", tot)
	}
	if len(shared) != 2 || shared[0].StockISIN != "Y" || shared[0].WeightABps != 300 || shared[0].WeightBBps != 400 {
		t.Fatalf("unexpected shared: %+v", shared)
	}
	// symmetric, including when the smaller map is B (flip path)
	tot2, shared2 := Overlap(b, a)
	if tot2 != 500 || shared2[0].WeightABps != 400 || shared2[0].WeightBBps != 300 {
		t.Fatalf("flip broke weights: %d %+v", tot2, shared2)
	}
}

func TestOverlapDisjointIsZero(t *testing.T) {
	tot, shared := Overlap(eqFund("A", 1, map[string]int64{"X": 1}), eqFund("B", 1, map[string]int64{"Y": 1}))
	if tot != 0 || len(shared) != 0 {
		t.Fatalf("got %d %v", tot, shared)
	}
}

func TestComputeOverlapsPairsAndDeterminism(t *testing.T) {
	var funds []Fund
	for i := 0; i < 12; i++ {
		funds = append(funds, eqFund(string(rune('A'+i)), 100, map[string]int64{"X": int64(100 + i*10), "Y": 200}))
	}
	r1, err := ComputeOverlaps(context.Background(), funds, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(r1) != 12*11/2 {
		t.Fatalf("pairs = %d", len(r1))
	}
	r2, _ := ComputeOverlaps(context.Background(), funds, 1)
	for i := range r1 {
		if r1[i].A != r2[i].A || r1[i].B != r2[i].B || r1[i].OverlapBps != r2[i].OverlapBps {
			t.Fatalf("worker count changed result at %d", i)
		}
	}
}

func TestComputeOverlapsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	funds := []Fund{eqFund("A", 1, nil), eqFund("B", 1, nil), eqFund("C", 1, nil)}
	if _, err := ComputeOverlaps(ctx, funds, 2); err == nil {
		t.Fatal("expected context error")
	}
}

func TestExitCostLTCGExemptionAndCess(t *testing.T) {
	cfg := DefaultTaxConfig(3000)
	f := eqFund("A", 100, nil)
	// gain Rs2,00,000 long-term; exemption Rs1,25,000 => taxable 75,000 * 12.5% = 9,375 * 1.04 = 9,750
	c := cfg.exitCost(f, []Lot{lot(d(2023, 1, 1), 800_000, 1_000_000)}, d(2025, 6, 1), cfg.LTCGExemptionPaise)
	if c.LTCGTaxPaise != 975_000 || c.STCGTaxPaise != 0 || c.TotalPaise != 975_000 {
		t.Fatalf("%+v", c)
	}
}

func TestExitCostSTCG(t *testing.T) {
	cfg := DefaultTaxConfig(3000)
	c := cfg.exitCost(eqFund("A", 100, nil), []Lot{lot(d(2025, 3, 1), 900_000, 1_000_000)}, d(2025, 6, 1), cfg.LTCGExemptionPaise)
	// gain 1,00,000 * 20% = 20,000 * 1.04 = 20,800
	if c.STCGTaxPaise != 2_080_000 {
		t.Fatalf("%+v", c)
	}
}

func TestExitCostNoCess(t *testing.T) {
	cfg := DefaultTaxConfig(3000)
	cfg.CessBps = 0
	c := cfg.exitCost(eqFund("A", 100, nil), []Lot{lot(d(2025, 3, 1), 900_000, 1_000_000)}, d(2025, 6, 1), 0)
	if c.STCGTaxPaise != 2_000_000 {
		t.Fatalf("%+v", c)
	}
}

func TestExitCostLongTermBoundaryIs365DaysExclusive(t *testing.T) {
	cfg := DefaultTaxConfig(3000)
	f := eqFund("A", 100, nil)
	buy := d(2024, 6, 1)
	at365 := buy.AddDate(0, 0, 365)
	at366 := buy.AddDate(0, 0, 366)
	l := []Lot{lot(buy, 900_000, 1_000_000)}
	if c := cfg.exitCost(f, l, at365, 0); c.STCGTaxPaise == 0 {
		t.Fatal("exactly 365 days must still be short-term")
	}
	if c := cfg.exitCost(f, l, at366, 0); c.STCGTaxPaise != 0 || c.LTCGTaxPaise == 0 {
		t.Fatalf("366 days must be long-term: %+v", c)
	}
}

func TestExitCostLossSetOff(t *testing.T) {
	cfg := DefaultTaxConfig(3000)
	f := eqFund("A", 100, nil)
	at := d(2025, 6, 1)
	// ST loss 50k offsets LT gain 200k -> LT 150k; exemption 0 => 150k*12.5%*1.04 = 19,500
	c := cfg.exitCost(f, []Lot{
		lot(d(2025, 4, 1), 1_050_000, 1_000_000),
		lot(d(2023, 1, 1), 800_000, 1_000_000),
	}, at, 0)
	if c.STCGTaxPaise != 0 || c.LTCGTaxPaise != 1_950_000 {
		t.Fatalf("%+v", c)
	}
}

func TestExitLoadOnlyWithinWindow(t *testing.T) {
	cfg := DefaultTaxConfig(3000)
	f := eqFund("A", 100, nil)
	f.ExitLoadBps, f.ExitLoadDays = 100, 365
	c := cfg.exitCost(f, []Lot{lot(d(2025, 2, 21), 1_000_000, 1_000_000)}, d(2025, 6, 1), 0) // 100 days old, no gain
	if c.ExitLoadPaise != 1_000_000 { // 1% of Rs10,00,000 = Rs10,000
		t.Fatalf("%+v", c)
	}
	c = cfg.exitCost(f, []Lot{lot(d(2023, 1, 1), 1_000_000, 1_000_000)}, d(2025, 6, 1), 0)
	if c.ExitLoadPaise != 0 {
		t.Fatalf("%+v", c)
	}
}

func TestNonEquityTaxedAtSlab(t *testing.T) {
	cfg := DefaultTaxConfig(3000) // 30% slab
	cfg.CessBps = 0
	f := Fund{ISIN: "D", EquityOriented: false}
	c := cfg.exitCost(f, []Lot{lot(d(2023, 1, 1), 900_000, 1_000_000)}, d(2025, 6, 1), cfg.LTCGExemptionPaise)
	if c.TotalPaise != 3_000_000 { // 1,00,000 * 30%
		t.Fatalf("%+v", c)
	}
}

func TestJudgeConsolidateNow(t *testing.T) {
	cfg := DefaultTaxConfig(3000)
	exit, keep := eqFund("HI", 160, nil), eqFund("LO", 60, nil)
	// gain Rs2L long-term -> tax Rs9,750; saving 1% of Rs10L = Rs10,000/yr => payback < 12m
	pos := Position{FundISIN: "HI", Lots: []Lot{lot(d(2023, 1, 10), 800_000, 1_000_000)}}
	a := cfg.Judge(exit, keep, pos, d(2025, 6, 1), 0)
	if a.Verdict != ConsolidateNow || a.AnnualSavingPaise != 1_000_000 || a.ExitNow.TotalPaise != 975_000 {
		t.Fatalf("%+v", a)
	}
}

func TestJudgeHoldUntilLTCG(t *testing.T) {
	cfg := DefaultTaxConfig(3000)
	exit, keep := eqFund("HI", 160, nil), eqFund("LO", 60, nil)
	pos := Position{Lots: []Lot{lot(d(2024, 8, 5), 800_000, 1_000_000)}} // 300 days old
	now := d(2025, 6, 1)
	a := cfg.Judge(exit, keep, pos, now, 0)
	if a.Verdict != HoldUntilLTCG || a.HoldUntil == nil {
		t.Fatalf("%+v", a)
	}
	if want := d(2025, 8, 6); !a.HoldUntil.Equal(want) {
		t.Fatalf("hold until %v, want %v", a.HoldUntil, want)
	}
}

func TestJudgeFinancialYearBoundaryRestoresExemption(t *testing.T) {
	cfg := DefaultTaxConfig(3000)
	exit, keep := eqFund("HI", 160, nil), eqFund("LO", 60, nil)
	pos := Position{Lots: []Lot{lot(d(2025, 4, 5), 800_000, 1_000_000)}} // 330 days old on 2026-03-01
	a := cfg.Judge(exit, keep, pos, d(2026, 3, 1), 125_000_00)          // exemption already used up
	if a.Verdict != HoldUntilLTCG || a.HoldUntil == nil || a.HoldUntil.Month() != time.April {
		t.Fatalf("%+v", a)
	}
}

func TestJudgeKeepWhenSavingTooSmall(t *testing.T) {
	cfg := DefaultTaxConfig(3000)
	exit, keep := eqFund("HI", 70, nil), eqFund("LO", 60, nil) // 10 bps => Rs1,000/yr
	pos := Position{Lots: []Lot{lot(d(2024, 8, 5), 800_000, 1_000_000)}}
	if a := cfg.Judge(exit, keep, pos, d(2025, 6, 1), 0); a.Verdict != Keep {
		t.Fatalf("%+v", a)
	}
}

func TestJudgeKeepWhenNoTERAdvantage(t *testing.T) {
	cfg := DefaultTaxConfig(3000)
	a := cfg.Judge(eqFund("A", 100, nil), eqFund("B", 100, nil),
		Position{Lots: []Lot{lot(d(2023, 1, 1), 1, 100)}}, d(2025, 6, 1), 0)
	if a.Verdict != Keep {
		t.Fatalf("%+v", a)
	}
}

func TestAnalyzeFlagsOnlyAboveThreshold(t *testing.T) {
	hi := eqFund("HI", 160, map[string]int64{"X": 2000, "Y": 2000})
	lo := eqFund("LO", 60, map[string]int64{"X": 1700, "Y": 1700}) // overlap 3400 (>3000)
	other := eqFund("OT", 90, map[string]int64{"X": 1000, "Z": 5000}) // overlap with HI/LO = 1000
	pos := map[string]Position{
		"HI": {Lots: []Lot{lot(d(2023, 1, 10), 800_000, 1_000_000)}},
		"LO": {Lots: []Lot{lot(d(2023, 1, 10), 100_000, 120_000)}},
		"OT": {Lots: []Lot{lot(d(2023, 1, 10), 100_000, 120_000)}},
	}
	pairs, leak, err := Analyze(context.Background(), []Fund{hi, lo, other}, pos, DefaultTaxConfig(3000), d(2025, 6, 1), 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 3 {
		t.Fatalf("pairs %d", len(pairs))
	}
	p := pairs[0] // HI x LO
	if p.OverlapBps != 3400 || p.Action.Verdict != ConsolidateNow || p.Action.ExitFundIndex != 0 || p.Action.KeepFundIndex != 1 {
		t.Fatalf("%+v", p)
	}
	for _, q := range pairs[1:] {
		if q.Action.Verdict != Keep || q.OverlapBps > FlagThresholdBps {
			t.Fatalf("unexpected flag %+v", q)
		}
	}
	if leak != 1_000_000 {
		t.Fatalf("leak %d", leak)
	}
}

func TestRedactFolioDeterministicPerSaltAndUnlinkable(t *testing.T) {
	s1, s2 := NewSalt(), NewSalt()
	a, b := RedactFolio("12345678/90", s1), RedactFolio("12345678/90", s1)
	if a != b || len(a) != 12 {
		t.Fatalf("not deterministic: %s %s", a, b)
	}
	if RedactFolio("12345678/90", s2) == a {
		t.Fatal("different salts must not link")
	}
	if strings.Contains(a, "1234") {
		t.Fatal("leaks folio")
	}
}

func TestZeroAndScrubPII(t *testing.T) {
	b := []byte("secret")
	Zero(b)
	for _, x := range b {
		if x != 0 {
			t.Fatal("not zeroed")
		}
	}
	got := ScrubPII("PAN ABCDE1234F mail a.b@x.co phone +91 9876543210 ok")
	if strings.Contains(got, "ABCDE") || strings.Contains(got, "@") || strings.Contains(got, "98765") {
		t.Fatalf("not scrubbed: %s", got)
	}
}
