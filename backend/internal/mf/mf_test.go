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

func lotUnits(buy time.Time, units string, costRs, valueRs int64) Lot {
	l := lot(buy, costRs, valueRs)
	l.Units = decimal.RequireFromString(units)
	return l
}

func TestGrandfatheredCostStepsUpPre2018Units(t *testing.T) {
	cfg := DefaultTaxConfig(3000)
	cfg.CessBps = 0
	f := eqFund("A", 100, nil)
	f.GrandfatherNAV = decimal.RequireFromString("5000") // NAV on 31-Jan-2018
	// 100 units bought 2015 for Rs1,00,000; FMV(31-Jan-2018) = Rs5,00,000; sells at Rs10,00,000.
	// Step-up: cost = max(1L, min(5L, 10L)) = 5L => gain 5L; taxable (5L - 1.25L) * 12.5% = 46,875.
	l := lotUnits(d(2015, 3, 1), "100", 100_000, 1_000_000)
	c := cfg.exitCost(f, []Lot{l}, d(2025, 6, 1), cfg.LTCGExemptionPaise)
	if c.LTCGTaxPaise != 4_687_500 || c.ExemptionUsedPaise != 12_500_000 {
		t.Fatalf("%+v", c)
	}
	// Without the NAV the full 9L gain is taxed: (9L - 1.25L) * 12.5% = 96,875.
	f.GrandfatherNAV = decimal.Zero
	if c := cfg.exitCost(f, []Lot{l}, d(2025, 6, 1), cfg.LTCGExemptionPaise); c.LTCGTaxPaise != 9_687_500 {
		t.Fatalf("%+v", c)
	}
}

func TestGrandfatheringNeverCreatesALoss(t *testing.T) {
	cfg := DefaultTaxConfig(3000)
	f := eqFund("A", 100, nil)
	f.GrandfatherNAV = decimal.RequireFromString("5000")
	// FMV (5L) is above the sale value (3L): cost becomes the 3L sale value => zero gain, not a 2L loss.
	l := lotUnits(d(2015, 3, 1), "100", 100_000, 300_000)
	if c := cfg.exitCost(f, []Lot{l}, d(2025, 6, 1), 0); c.TotalPaise != 0 {
		t.Fatalf("%+v", c)
	}
}

func TestGrandfatheringIgnoresPost2018AndNonEquity(t *testing.T) {
	f := eqFund("A", 100, nil)
	f.GrandfatherNAV = decimal.RequireFromString("5000")
	post := lotUnits(d(2018, 2, 1), "100", 100_000, 1_000_000) // 1 Feb 2018 itself is NOT grandfathered
	if got := grandfatheredCost(f, post); got != post.CostPaise {
		t.Fatalf("post-cutoff lot stepped up: %d", got)
	}
	pre := lotUnits(d(2018, 1, 31), "100", 100_000, 1_000_000)
	if got := grandfatheredCost(f, pre); got != 50_000_000 {
		t.Fatalf("31 Jan 2018 lot should step up: %d", got)
	}
	f.EquityOriented = false
	if got := grandfatheredCost(f, pre); got != pre.CostPaise {
		t.Fatalf("non-equity stepped up: %d", got)
	}
}

func TestJudgeNotesMissingGrandfatherNAV(t *testing.T) {
	cfg := DefaultTaxConfig(3000)
	exit, keep := eqFund("HI", 160, nil), eqFund("LO", 60, nil)
	pos := Position{Lots: []Lot{lotUnits(d(2015, 3, 1), "100", 100_000, 1_000_000)}}
	a := cfg.Judge(exit, keep, pos, d(2025, 6, 1), 0)
	if len(a.Notes) != 1 || !strings.Contains(a.Notes[0], "31-Jan-2018") {
		t.Fatalf("%+v", a.Notes)
	}
	exit.GrandfatherNAV = decimal.RequireFromString("5000")
	if a := cfg.Judge(exit, keep, pos, d(2025, 6, 1), 0); len(a.Notes) != 0 {
		t.Fatalf("unexpected note %+v", a.Notes)
	}
}

func TestAnalyzeSharesTheExemptionAcrossExitFunds(t *testing.T) {
	stocks := map[string]int64{"X": 2000, "Y": 2000}
	a := eqFund("A", 160, stocks) // exit candidate, saving 100bps
	c := eqFund("C", 150, stocks) // exit candidate, saving 90bps
	b := eqFund("B", 60, stocks)  // the cheap fund both overlap with
	// Each exit fund holds Rs10L with exactly Rs1.25L of long-term gain: alone it fits the exemption.
	mk := func() Position { return Position{Lots: []Lot{lot(d(2023, 1, 10), 875_000, 1_000_000)}} }
	pos := map[string]Position{"A": mk(), "C": mk(), "B": {Lots: []Lot{lot(d(2023, 1, 10), 100, 100)}}}

	pairs, _, err := Analyze(context.Background(), []Fund{a, b, c}, pos, DefaultTaxConfig(3000), d(2025, 6, 1), 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	byExit := map[int]PairAction{}
	for _, p := range pairs {
		if p.OverlapBps > FlagThresholdBps {
			byExit[p.Action.ExitFundIndex] = p.Action
		}
	}
	first, second := byExit[0], byExit[2] // A (index 0) has the larger saving and goes first
	if first.Verdict != ConsolidateNow || first.ExitNow.ExemptionUsedPaise != 12_500_000 || first.ExitNow.TotalPaise != 0 {
		t.Fatalf("first exit should use the whole exemption tax-free: %+v", first)
	}
	if second.Verdict != Keep || second.ExitNow.LTCGTaxPaise == 0 {
		t.Fatalf("second exit must see an empty exemption pool: %+v", second)
	}
}

func TestAnalyzeExitFundInSeveralPairsDoesNotCompeteWithItself(t *testing.T) {
	stocks := map[string]int64{"X": 2000, "Y": 2000}
	a := eqFund("A", 160, stocks)
	b := eqFund("B", 60, stocks)
	c := eqFund("C", 70, stocks)
	pos := map[string]Position{
		"A": {Lots: []Lot{lot(d(2023, 1, 10), 875_000, 1_000_000)}}, // Rs1.25L gain: exactly the pool
		"B": {Lots: []Lot{lot(d(2023, 1, 10), 100, 100)}},
		"C": {Lots: []Lot{lot(d(2023, 1, 10), 100, 100)}},
	}
	pairs, _, _ := Analyze(context.Background(), []Fund{a, b, c}, pos, DefaultTaxConfig(3000), d(2025, 6, 1), 0, 2)
	for _, p := range pairs {
		if p.Action.ExitFundIndex == 0 && p.OverlapBps > FlagThresholdBps && p.Action.ExitNow.TotalPaise != 0 {
			t.Fatalf("A is one redemption: both of its pairs must see the full pool, got %+v", p.Action)
		}
	}
}

func fundWith(isin string, h map[string]int64) Fund {
	f := eqFund(isin, 100, h)
	f.HoldingNames = map[string]string{}
	for k := range h {
		f.HoldingNames[k] = "name-" + k
	}
	return f
}

func TestLookThroughScalesUnderlyingHoldings(t *testing.T) {
	u1 := fundWith("U1", map[string]int64{"X": 5000, "Y": 3000})
	u2 := fundWith("U2", map[string]int64{"X": 2000, "Z": 6000})
	fof := fundWith("FOF", map[string]int64{"U1": 6000, "U2": 3000, "CASHSTOCK": 500})
	in := map[string]Fund{"U1": u1, "U2": u2, "FOF": fof}

	out := LookThrough(in)
	h := out["FOF"].Holdings
	want := map[string]int64{"X": 3600, "Y": 1800, "Z": 1800, "CASHSTOCK": 500} // X = 60%*50% + 30%*20%
	if len(h) != len(want) {
		t.Fatalf("%v", h)
	}
	for k, v := range want {
		if h[k] != v {
			t.Errorf("%s = %d, want %d (%v)", k, h[k], v, h)
		}
	}
	if out["FOF"].HoldingNames["X"] != "name-X" {
		t.Fatalf("names lost: %v", out["FOF"].HoldingNames)
	}
	// the input is untouched and flat funds are returned unchanged
	if len(in["FOF"].Holdings) != 3 || in["FOF"].Holdings["U1"] != 6000 {
		t.Fatal("input mutated")
	}
	if len(out["U1"].Holdings) != 2 || out["U1"].Holdings["X"] != 5000 {
		t.Fatalf("flat fund changed: %v", out["U1"].Holdings)
	}
}

func TestLookThroughMakesAFundOfFundsOverlapItsUnderlying(t *testing.T) {
	u1 := fundWith("U1", map[string]int64{"X": 5000, "Y": 3000})
	fof := fundWith("FOF", map[string]int64{"U1": 9000})
	// Raw holdings share nothing (the FoF "holds" a fund ISIN), so without look-through overlap is 0.
	if o, _ := Overlap(fof, u1); o != 0 {
		t.Fatalf("raw overlap %d", o)
	}
	flat := LookThrough(map[string]Fund{"U1": u1, "FOF": fof})
	o, shared := Overlap(flat["FOF"], flat["U1"])
	if o != 7200 || len(shared) != 2 { // 90% of (50% + 30%) = 72%
		t.Fatalf("overlap %d %v", o, shared)
	}
	if o <= FlagThresholdBps {
		t.Fatal("a FoF and its underlying must be flagged")
	}
}

func TestLookThroughNestedAndCycles(t *testing.T) {
	eq := fundWith("EQ", map[string]int64{"X": 8000})
	mid := fundWith("MID", map[string]int64{"EQ": 5000})
	top := fundWith("TOP", map[string]int64{"MID": 5000})
	out := LookThrough(map[string]Fund{"EQ": eq, "MID": mid, "TOP": top})
	if got := out["TOP"].Holdings["X"]; got != 2000 { // 50% * 50% * 80%
		t.Fatalf("nested = %d (%v)", got, out["TOP"].Holdings)
	}

	a := fundWith("A", map[string]int64{"B": 5000, "S": 2000})
	b := fundWith("B", map[string]int64{"A": 5000, "T": 4000})
	cyc := LookThrough(map[string]Fund{"A": a, "B": b}) // must terminate
	if cyc["A"].Holdings["T"] != 2000 || cyc["A"].Holdings["S"] != 2000 {
		t.Fatalf("%v", cyc["A"].Holdings)
	}
	if _, kept := cyc["A"].Holdings["A"]; kept {
		// A -> B -> A: the inner A is opaque (cycle), so its 5000*5000/10000 = 2500 stays under key "A"
		if cyc["A"].Holdings["A"] != 2500 {
			t.Fatalf("%v", cyc["A"].Holdings)
		}
	}
}

func chainFunds(n int) map[string]Fund {
	chain := map[string]Fund{"L0": fundWith("L0", map[string]int64{"X": 10000})}
	prev := "L0"
	for i := 1; i <= n; i++ {
		id := "L" + string(rune('0'+i))
		chain[id] = fundWith(id, map[string]int64{prev: 10000})
		prev = id
	}
	return chain
}

func TestLookThroughDepthLimitTerminatesWithoutGrowingWeights(t *testing.T) {
	out := LookThrough(chainFunds(8)) // far deeper than MaxLookThroughDepth
	var total int64
	for _, w := range out["L8"].Holdings {
		total += w
	}
	if total != 10_000 {
		t.Fatalf("weights not conserved: %d (%v)", total, out["L8"].Holdings)
	}
}

func TestLookThroughIsIdempotentWithinDepthLimit(t *testing.T) {
	once := LookThrough(chainFunds(MaxLookThroughDepth))
	if once["L3"].Holdings["X"] != 10_000 {
		t.Fatalf("a chain of exactly the depth limit must fully expand: %v", once["L3"].Holdings)
	}
	twice := LookThrough(once)
	for k, v := range once["L3"].Holdings {
		if twice["L3"].Holdings[k] != v {
			t.Fatalf("not idempotent at %s", k)
		}
	}
}
