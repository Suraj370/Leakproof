package cards

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestSimplexTextbookMaximisation(t *testing.T) {
	// max 3x + 2y  s.t.  x + y <= 4,  x + 3y <= 6,  x <= 3   => (3, 1), 11
	x, obj, ok := simplex([]float64{3, 2}, []lpRow{
		{[]float64{1, 1}, rowLE, 4}, {[]float64{1, 3}, rowLE, 6}, {[]float64{1, 0}, rowLE, 3},
	})
	if !ok || !near(obj, 11) || !near(x[0], 3) || !near(x[1], 1) {
		t.Fatalf("%v %v %v", x, obj, ok)
	}
}

func TestSimplexNeedsPhaseOneForGEConstraints(t *testing.T) {
	// min x + y  s.t.  x + 2y >= 4,  3x + y >= 3   => (0.4, 1.8), 2.2  (as a max of -x-y)
	x, obj, ok := simplex([]float64{-1, -1}, []lpRow{
		{[]float64{1, 2}, rowGE, 4}, {[]float64{3, 1}, rowGE, 3},
	})
	if !ok || !near(obj, -2.2) || !near(x[0], 0.4) || !near(x[1], 1.8) {
		t.Fatalf("%v %v %v", x, obj, ok)
	}
}

func TestSimplexMixedRowsAndNegativeRHS(t *testing.T) {
	// max x + y  s.t.  x + y <= 10,  -x <= -2 (i.e. x >= 2),  y <= 3  => x = 7, y = 3, 10
	_, obj, ok := simplex([]float64{1, 1}, []lpRow{
		{[]float64{1, 1}, rowLE, 10}, {[]float64{-1, 0}, rowLE, -2}, {[]float64{0, 1}, rowLE, 3},
	})
	if !ok || !near(obj, 10) {
		t.Fatalf("%v %v", obj, ok)
	}
}

func TestSimplexInfeasibleAndUnbounded(t *testing.T) {
	if _, _, ok := simplex([]float64{1}, []lpRow{{[]float64{1}, rowLE, 1}, {[]float64{1}, rowGE, 2}}); ok {
		t.Fatal("x<=1 and x>=2 is infeasible")
	}
	if _, _, ok := simplex([]float64{1}, []lpRow{{[]float64{1}, rowGE, 1}}); ok {
		t.Fatal("max x with x>=1 is unbounded")
	}
}

func TestSimplexHandlesDegeneracyAndRedundantRows(t *testing.T) {
	// Duplicate and zero-RHS rows create degenerate vertices; Bland's rule must still terminate.
	x, obj, ok := simplex([]float64{2, 1}, []lpRow{
		{[]float64{1, 1}, rowLE, 2}, {[]float64{1, 1}, rowLE, 2}, {[]float64{1, 0}, rowLE, 0}, {[]float64{0, 1}, rowLE, 5},
	})
	if !ok || !near(obj, 2) || !near(x[0], 0) || !near(x[1], 2) {
		t.Fatalf("%v %v %v", x, obj, ok)
	}
}

// ---- allocation ----

// A has a shared cap over ONLINE (5%) and DINING (4%); B pays 5% online and 1% dining, uncapped.
// Greedy gives A the online spend first (a tie on rate), burning A's whole cap on the category B matches,
// and leaves dining at 1%. The optimum puts online on B and dining on A.
func capTrapCards() []Card {
	a := Card{
		ID: "A", RedeemPaisePerPoint: 100, BaseEarnBps: 0,
		Rules: map[Category]CategoryRule{
			OnlineShopping: {EarnBps: 500, CapGroup: "g"},
			Dining:         {EarnBps: 400, CapGroup: "g"},
		},
		GroupCapPoints: map[string]int64{"g": 2000},
	}
	b := Card{
		ID: "B", RedeemPaisePerPoint: 100,
		Rules: map[Category]CategoryRule{OnlineShopping: {EarnBps: 500}, Dining: {EarnBps: 100}},
	}
	return []Card{a, b}
}

func TestExactBeatsGreedyWhenASharedCapIsMisused(t *testing.T) {
	cards := capTrapCards()
	spend := Spend{OnlineShopping: 4_000_000, Dining: 4_000_000} // Rs40,000 each per month

	greedy := realValue(cards, allocateGreedy(cards, spend), false)
	exact := realValue(cards, Allocate(cards, spend), false)
	if greedy != 2_880_000 { // 2,400 points/month: the trap
		t.Fatalf("greedy = %d (the scenario no longer exercises the trap)", greedy)
	}
	if exact != 4_320_000 { // 3,600 points/month: online on B (2,000) + dining on A (1,600)
		t.Fatalf("exact = %d, want 4,320,000", exact)
	}
	alloc := Allocate(cards, spend)
	if alloc["A"][Dining] != 4_000_000 || alloc["B"][OnlineShopping] != 4_000_000 || alloc["A"][OnlineShopping] != 0 {
		t.Fatalf("%v", alloc)
	}
}

func TestExactChasesAMilestoneGreedyIgnores(t *testing.T) {
	m := Card{ID: "M", BaseEarnBps: 100, RedeemPaisePerPoint: 100,
		Milestones: []Milestone{{AnnualSpendPaise: 10_000_000, BonusValuePaise: 500_000}}} // Rs1L/yr => Rs5,000
	n := Card{ID: "N", BaseEarnBps: 120, RedeemPaisePerPoint: 100}
	cards := []Card{m, n}
	spend := Spend{OfflinePOS: 1_000_000} // Rs10,000/month = Rs1.2L/yr

	greedy := realValue(cards, allocateGreedy(cards, spend), false)
	alloc := Allocate(cards, spend)
	exact := realValue(cards, alloc, false)
	if exact <= greedy+400_000 { // the Rs5,000 voucher dwarfs the 0.2% rate gap
		t.Fatalf("exact %d vs greedy %d", exact, greedy)
	}
	if got := alloc["M"][OfflinePOS]; got*12 < 10_000_000 || got > 1_000_000 {
		t.Fatalf("M must get just enough to reach the milestone, got %d", got)
	}
	if y := NetYield(m, alloc["M"], false); y.MilestonePaise != 500_000 {
		t.Fatalf("%+v", y)
	}
}

func TestExactWinsAFeeWaiverWhenItIsWorthIt(t *testing.T) {
	// X: 2% but a Rs5,000 fee, waived at Rs1.2L/yr. Y: 1.5% no fee. Rs10,000/month spend (Rs1.2L/yr).
	x := Card{ID: "X", BaseEarnBps: 200, RedeemPaisePerPoint: 100, AnnualFeePaise: 500_000, FeeWaiverSpendPaise: 12_000_000}
	y := Card{ID: "Y", BaseEarnBps: 150, RedeemPaisePerPoint: 100}
	cards := []Card{x, y}
	spend := Spend{OfflinePOS: 1_000_000}
	alloc := Allocate(cards, spend)
	if alloc["X"][OfflinePOS] != 1_000_000 { // all on X: waived and 2% beats 1.5%
		t.Fatalf("%v", alloc)
	}
	if NetYield(x, alloc["X"], false).FeePaise != 0 {
		t.Fatal("fee should be waived")
	}
}

func TestAllocationNeverExceedsSpendAndNeverLosesToGreedy(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for n := 0; n < 250; n++ {
		cards, spend := randomInstance(rng, 1+rng.Intn(3))
		alloc := Allocate(cards, spend)

		used := Spend{}
		for _, s := range alloc {
			for cat, v := range s {
				if v < 0 {
					t.Fatalf("case %d: negative allocation", n)
				}
				used[cat] += v
			}
		}
		for cat, v := range used {
			if v > spend[cat] {
				t.Fatalf("case %d: %s over-assigned %d > %d", n, cat, v, spend[cat])
			}
		}
		for _, held := range []bool{false, true} {
			exact := realValue(cards, allocate(cards, spend, held), held)
			greedy := realValue(cards, allocateGreedy(cards, spend), held)
			if exact < greedy {
				t.Fatalf("case %d held=%v: exact %d < greedy %d", n, held, exact, greedy)
			}
			if ub := upperBound(cards, spend); float64(exact) > ub {
				t.Fatalf("case %d: value %d above upper bound %f", n, exact, ub)
			}
		}
	}
}

// Brute force over a coarse grid of two-card splits: the exact solver must never do worse than the best
// grid point (it can do better, since the grid is coarse), up to the per-category point flooring.
func TestExactMatchesBruteForceOnTwoCardInstances(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for n := 0; n < 120; n++ {
		cards, spend := randomInstance(rng, 2)
		cats := make([]Category, 0, len(spend))
		for cat := range spend {
			if cat != Excluded {
				cats = append(cats, cat)
			}
		}
		const chunk = 50_000 // Rs500
		var best int64 = math.MinInt64
		var rec func(i int, a, b Spend)
		rec = func(i int, a, b Spend) {
			if i == len(cats) {
				v := NetYield(cards[0], a, false).NetPaise + NetYield(cards[1], b, false).NetPaise
				best = max(best, v)
				return
			}
			total := spend[cats[i]]
			for k := int64(0); k*chunk <= total; k++ {
				x := min(k*chunk, total)
				a2, b2 := cloneSpend(a), cloneSpend(b)
				a2[cats[i]], b2[cats[i]] = x, total-x
				rec(i+1, a2, b2)
			}
		}
		rec(0, Spend{}, Spend{})

		exact := realValue(cards, Allocate(cards, spend), false)
		const flooringSlack = 8_000 // < 1 point per card per category per month, annualised
		if exact+flooringSlack < best {
			t.Fatalf("case %d: exact %d is below the brute-force best %d\ncards=%+v\nspend=%v", n, exact, best, cards, spend)
		}
	}
}

func cloneSpend(s Spend) Spend {
	o := Spend{}
	for k, v := range s {
		o[k] = v
	}
	return o
}

// randomInstance builds small but nasty cards: shared caps, exclusions, milestones and fee waivers.
func randomInstance(rng *rand.Rand, nCards int) ([]Card, Spend) {
	cats := []Category{Dining, Groceries, OnlineShopping, Fuel}
	var cards []Card
	for k := 0; k < nCards; k++ {
		c := Card{
			ID: string(rune('A' + k)), BaseEarnBps: int64(rng.Intn(250)), RedeemPaisePerPoint: int64([]int{25, 50, 100}[rng.Intn(3)]),
			Rules: map[Category]CategoryRule{}, GroupCapPoints: map[string]int64{},
		}
		for _, cat := range cats {
			switch rng.Intn(4) {
			case 0: // plain accelerated rate
				c.Rules[cat] = CategoryRule{EarnBps: int64(100 + rng.Intn(500))}
			case 1: // accelerated and capped, possibly sharing the cap with another category
				c.Rules[cat] = CategoryRule{EarnBps: int64(200 + rng.Intn(400)), CapGroup: []string{"g1", "g2"}[rng.Intn(2)]}
			case 2:
				c.Rules[cat] = CategoryRule{Excluded: rng.Intn(2) == 0, EarnBps: int64(rng.Intn(100))}
			}
		}
		for _, g := range []string{"g1", "g2"} {
			if rng.Intn(3) > 0 {
				c.GroupCapPoints[g] = int64(100 + rng.Intn(1500))
			}
		}
		if rng.Intn(2) == 0 {
			c.AnnualFeePaise = int64(1 + rng.Intn(10)) * 50_000
			if rng.Intn(2) == 0 {
				c.FeeWaiverSpendPaise = int64(5+rng.Intn(60)) * 1_000_000
			}
		}
		for m := rng.Intn(3); m > 0; m-- {
			spendTh := int64(5+rng.Intn(80)) * 1_000_000
			c.Milestones = append(c.Milestones, Milestone{AnnualSpendPaise: spendTh, BonusValuePaise: int64(1+rng.Intn(8)) * 100_000})
		}
		cards = append(cards, c)
	}
	spend := Spend{}
	for _, cat := range cats {
		if rng.Intn(4) > 0 {
			spend[cat] = int64(1+rng.Intn(8)) * 50_000 * int64(1+rng.Intn(3)) // Rs500..Rs12,000 / month
		}
	}
	if len(spend) == 0 {
		spend[Dining] = 300_000
	}
	return cards, spend
}

// ---- best combo ----

func TestBestComboMatchesExhaustiveSearch(t *testing.T) {
	rng := rand.New(rand.NewSource(23))
	for n := 0; n < 40; n++ {
		var catalog []Card
		for i := 0; i < 6; i++ {
			cs, _ := randomInstance(rng, 1)
			cs[0].ID = string(rune('A' + i))
			catalog = append(catalog, cs[0])
		}
		_, spend := randomInstance(rng, 1)
		current := catalog[:1+rng.Intn(2)]

		got := BestCombo(catalog, current, spend)

		bestNet, bestIDs := int64(math.MinInt64), []string(nil)
		consider := func(cs ...Card) {
			net, _ := evaluate(cs, spend, false)
			if net > bestNet {
				bestNet, bestIDs = net, nil
				for _, c := range cs {
					bestIDs = append(bestIDs, c.ID)
				}
			}
		}
		for i := range catalog {
			consider(catalog[i])
			for j := i + 1; j < len(catalog); j++ {
				consider(catalog[i], catalog[j])
			}
		}
		if got.NetPaise != bestNet || !reflect.DeepEqual(got.CardIDs, bestIDs) {
			t.Fatalf("case %d: branch-and-bound %v/%d != exhaustive %v/%d", n, got.CardIDs, got.NetPaise, bestIDs, bestNet)
		}
		base, _ := evaluate(current, spend, true)
		if got.BaselinePaise != base || got.GainPaise != got.NetPaise-base {
			t.Fatalf("case %d: %+v", n, got)
		}
	}
}

func TestBestComboIsDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	var catalog []Card
	for i := 0; i < 8; i++ {
		cs, _ := randomInstance(rng, 1)
		cs[0].ID = string(rune('A' + i))
		catalog = append(catalog, cs[0])
	}
	_, spend := randomInstance(rng, 1)
	a, b := BestCombo(catalog, nil, spend), BestCombo(catalog, nil, spend)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("%+v vs %+v", a, b)
	}
}

func TestExactSolverFallsBackWhenTheStateSpaceIsHuge(t *testing.T) {
	// 7 thresholds on each of 4 cards => 8^4 = 4096 > maxLevelStates: allocate() must still answer (greedy).
	var cards []Card
	for k := 0; k < 4; k++ {
		c := Card{ID: string(rune('A' + k)), BaseEarnBps: 100 + int64(k)*10, RedeemPaisePerPoint: 100}
		for m := 1; m <= 6; m++ {
			c.Milestones = append(c.Milestones, Milestone{AnnualSpendPaise: int64(m) * 10_000_000, BonusValuePaise: 10_000})
		}
		c.AnnualFeePaise, c.FeeWaiverSpendPaise = 100_000, 5_000_000
		cards = append(cards, c)
	}
	spend := Spend{Dining: 2_000_000}
	if _, ok := allocateExact(cards, spend, false); ok {
		t.Fatal("expected the exact solver to decline")
	}
	alloc := Allocate(cards, spend)
	var total int64
	for _, s := range alloc {
		total += s[Dining]
	}
	if total != 2_000_000 {
		t.Fatalf("greedy fallback lost spend: %d", total)
	}
}
