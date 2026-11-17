package cards

import (
	"encoding/json"
	"math/rand"
	"os"
	"reflect"
	"testing"
)

// golden_solver.json is asserted by this test and by frontend/src/solver.test.ts, so the Go and TypeScript
// solvers must return identical allocations and totals. Regenerate after an intentional solver change:
//
//	UPDATE_GOLDEN=1 go test ./internal/cards -run TestGoldenSolverParity
const goldenSolverFile = "../../testdata/golden_solver.json"

type solverCase struct {
	Name               string           `json:"name"`
	Cards              []Card           `json:"cards"`
	Spend              Spend            `json:"monthlySpendPaise"`
	Held               bool             `json:"held"`
	ExpectedAllocation map[string]Spend `json:"expectedAllocation"`
	ExpectedNetPaise   int64            `json:"expectedNetPaise"`
}

type comboCase struct {
	Name             string   `json:"name"`
	Catalog          []Card   `json:"catalog"`
	Current          []Card   `json:"current"`
	Spend            Spend    `json:"monthlySpendPaise"`
	ExpectedCardIDs  []string `json:"expectedCardIds"`
	ExpectedNetPaise int64    `json:"expectedNetPaise"`
	ExpectedBaseline int64    `json:"expectedBaselinePaise"`
}

type solverGolden struct {
	Allocations []solverCase `json:"allocations"`
	Combos      []comboCase  `json:"combos"`
}

// jsonSafe gives every card non-nil maps/slices so the fixture carries {} and [] (never null), exactly
// like the API does.
func jsonSafe(in []Card) []Card {
	out := append([]Card(nil), in...)
	for i := range out {
		if out[i].Rules == nil {
			out[i].Rules = map[Category]CategoryRule{}
		}
		if out[i].GroupCapPoints == nil {
			out[i].GroupCapPoints = map[string]int64{}
		}
		if out[i].Milestones == nil {
			out[i].Milestones = []Milestone{}
		}
	}
	return out
}

func buildSolverGolden() solverGolden {
	var g solverGolden
	add := func(name string, cards []Card, spend Spend, held bool) {
		cards = jsonSafe(cards)
		alloc := allocate(cards, spend, held)
		g.Allocations = append(g.Allocations, solverCase{name, cards, spend, held, alloc, realValue(cards, alloc, held)})
	}

	add("shared cap trap", capTrapCards(), Spend{OnlineShopping: 4_000_000, Dining: 4_000_000}, false)

	m := Card{ID: "M", BaseEarnBps: 100, RedeemPaisePerPoint: 100, Rules: map[Category]CategoryRule{}, GroupCapPoints: map[string]int64{},
		Milestones: []Milestone{{AnnualSpendPaise: 10_000_000, BonusValuePaise: 500_000}}}
	n := Card{ID: "N", BaseEarnBps: 120, RedeemPaisePerPoint: 100, Rules: map[Category]CategoryRule{}, GroupCapPoints: map[string]int64{}, Milestones: []Milestone{}}
	add("milestone beats rate", []Card{m, n}, Spend{OfflinePOS: 1_000_000}, false)

	x := Card{ID: "X", BaseEarnBps: 200, RedeemPaisePerPoint: 100, AnnualFeePaise: 500_000, FeeWaiverSpendPaise: 12_000_000,
		Rules: map[Category]CategoryRule{}, GroupCapPoints: map[string]int64{}, Milestones: []Milestone{}}
	y := Card{ID: "Y", BaseEarnBps: 150, RedeemPaisePerPoint: 100, Rules: map[Category]CategoryRule{}, GroupCapPoints: map[string]int64{}, Milestones: []Milestone{}}
	add("fee waiver", []Card{x, y}, Spend{OfflinePOS: 1_000_000}, false)

	rng := rand.New(rand.NewSource(101))
	for i := 0; i < 8; i++ {
		cards, spend := randomInstance(rng, 1+i%3)
		for k := range cards {
			if cards[k].Milestones == nil {
				cards[k].Milestones = []Milestone{}
			}
		}
		add("random "+string(rune('a'+i)), cards, spend, i%2 == 0)
	}

	for i := 0; i < 4; i++ {
		var catalog []Card
		for k := 0; k < 6; k++ {
			cs, _ := randomInstance(rng, 1)
			cs[0].ID = string(rune('A' + k))
			if cs[0].Milestones == nil {
				cs[0].Milestones = []Milestone{}
			}
			catalog = append(catalog, cs[0])
		}
		_, spend := randomInstance(rng, 1)
		current := []Card{catalog[0], catalog[1]}
		res := BestCombo(catalog, current, spend)
		g.Combos = append(g.Combos, comboCase{"catalog " + string(rune('a'+i)), jsonSafe(catalog), jsonSafe(current), spend, res.CardIDs, res.NetPaise, res.BaselinePaise})
	}
	return g
}

func TestGoldenSolverParity(t *testing.T) {
	got := buildSolverGolden()
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		b, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile(goldenSolverFile, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("golden_solver.json updated")
		return
	}
	b, err := os.ReadFile(goldenSolverFile)
	if err != nil {
		t.Fatal(err)
	}
	var want solverGolden
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	if len(got.Allocations) != len(want.Allocations) || len(got.Combos) != len(want.Combos) {
		t.Fatalf("scenario count changed: regenerate with UPDATE_GOLDEN=1")
	}
	for i, w := range want.Allocations {
		a := got.Allocations[i]
		if !reflect.DeepEqual(a.ExpectedAllocation, w.ExpectedAllocation) || a.ExpectedNetPaise != w.ExpectedNetPaise {
			t.Errorf("%s: solver output changed\n got %v / %d\nwant %v / %d", w.Name, a.ExpectedAllocation, a.ExpectedNetPaise, w.ExpectedAllocation, w.ExpectedNetPaise)
		}
	}
	for i, w := range want.Combos {
		c := got.Combos[i]
		if !reflect.DeepEqual(c.ExpectedCardIDs, w.ExpectedCardIDs) || c.ExpectedNetPaise != w.ExpectedNetPaise || c.ExpectedBaseline != w.ExpectedBaseline {
			t.Errorf("%s: best combo changed: got %v/%d, want %v/%d", w.Name, c.ExpectedCardIDs, c.ExpectedNetPaise, w.ExpectedCardIDs, w.ExpectedNetPaise)
		}
	}
}
