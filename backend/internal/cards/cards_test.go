package cards

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type golden struct {
	Cards              []Card                `json:"cards"`
	MonthlySpendPaise  Spend                 `json:"monthlySpendPaise"`
	ExpectedAllocation map[string]Spend      `json:"expectedAllocation"`
	ExpectedYields     []CardYield           `json:"expectedYields"`
}

func loadGolden(t *testing.T) golden {
	t.Helper()
	b, err := os.ReadFile("../../testdata/golden_cards.json")
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

// The same fixture is asserted by the TypeScript simulator tests: Go and TS must never drift.
func TestGoldenParityFixture(t *testing.T) {
	g := loadGolden(t)
	alloc := Allocate(g.Cards, g.MonthlySpendPaise)
	if !reflect.DeepEqual(alloc, g.ExpectedAllocation) {
		t.Fatalf("allocation\n got %v\nwant %v", alloc, g.ExpectedAllocation)
	}
	_, ys := evaluate(g.Cards, g.MonthlySpendPaise, false)
	if !reflect.DeepEqual(ys, g.ExpectedYields) {
		t.Fatalf("yields\n got %+v\nwant %+v", ys, g.ExpectedYields)
	}
}

func cashback(fee, waiver int64) Card {
	return Card{
		ID: "cb", AnnualFeePaise: fee, FeeWaiverSpendPaise: waiver, BaseEarnBps: 100, RedeemPaisePerPoint: 100,
		Rules: map[Category]CategoryRule{
			OnlineShopping: {EarnBps: 500, CapGroup: "online"},
			Fuel:           {Excluded: true},
		},
		GroupCapPoints: map[string]int64{"online": 5000},
	}
}

func TestMonthlyCapAndLostRewards(t *testing.T) {
	// Rs2,00,000 online => 10,000 pts raw, capped at 5,000 => Rs5,000/mo => Rs60,000/yr; Rs60,000 lost.
	y := NetYield(cashback(0, 0), Spend{OnlineShopping: 20_000_000}, false)
	if y.AnnualRewardPaise != 6_000_000 || y.LostToCapsPaise != 6_000_000 {
		t.Fatalf("%+v", y)
	}
}

func TestExcludedCategoriesEarnNothing(t *testing.T) {
	y := NetYield(cashback(0, 0), Spend{Fuel: 5_000_000, Excluded: 10_000_000}, false)
	if y.AnnualRewardPaise != 0 {
		t.Fatalf("%+v", y)
	}
}

func TestFeeWaiverThresholdUsesOnlyEligibleSpend(t *testing.T) {
	c := cashback(59_900, 20_000_000) // waive at Rs2L/yr eligible spend
	// 1L/mo online => 12L/yr >= 2L => waived
	if y := NetYield(c, Spend{OnlineShopping: 10_000_000}, false); y.FeePaise != 0 {
		t.Fatalf("expected waiver %+v", y)
	}
	// Only excluded fuel spend => eligible 0 => fee charged (card is used)
	if y := NetYield(c, Spend{Fuel: 50_000_000}, false); y.FeePaise != 59_900 {
		t.Fatalf("fuel must not count toward waiver %+v", y)
	}
	// Barely under: Rs15,000/mo = 1.8L/yr < 2L
	if y := NetYield(c, Spend{OnlineShopping: 1_500_000}, false); y.FeePaise != 59_900 {
		t.Fatalf("%+v", y)
	}
}

func TestHeldCardPaysFeeEvenUnused(t *testing.T) {
	c := cashback(59_900, 0)
	if y := NetYield(c, Spend{}, true); y.FeePaise != 59_900 || y.NetPaise != -59_900 {
		t.Fatalf("%+v", y)
	}
	if y := NetYield(c, Spend{}, false); y.FeePaise != 0 {
		t.Fatalf("%+v", y)
	}
}

func TestRedemptionValueScalesReward(t *testing.T) {
	c := Card{ID: "rp", BaseEarnBps: 333, RedeemPaisePerPoint: 25} // ~3.33 pts/Rs at Rs0.25 each
	y := NetYield(c, Spend{OfflinePOS: 10_000_000}, false)         // Rs1L/mo => 3330 pts/mo
	if y.AnnualRewardPaise != 3330*25*12 {
		t.Fatalf("%+v", y)
	}
}

func TestSharedCapConsumedByHigherRateCategoryFirst(t *testing.T) {
	c := Card{
		ID: "s", RedeemPaisePerPoint: 100,
		Rules: map[Category]CategoryRule{
			Dining:         {EarnBps: 500, CapGroup: "g"},
			OnlineShopping: {EarnBps: 200, CapGroup: "g"},
		},
		GroupCapPoints: map[string]int64{"g": 1000},
	}
	// Dining Rs20,000 => 1000 pts (fills cap); online gets nothing. If online went first we'd lose value.
	y := NetYield(c, Spend{Dining: 2_000_000, OnlineShopping: 2_000_000}, false)
	if y.AnnualRewardPaise != 1000*100*12 {
		t.Fatalf("%+v", y)
	}
}

func TestAllocateSplitsAtCap(t *testing.T) {
	a := cashback(0, 0)
	a.ID = "A"
	b := Card{ID: "B", BaseEarnBps: 150, RedeemPaisePerPoint: 100}
	alloc := Allocate([]Card{a, b}, Spend{OnlineShopping: 20_000_000})
	if alloc["A"][OnlineShopping] != 10_000_000 || alloc["B"][OnlineShopping] != 10_000_000 {
		t.Fatalf("%v", alloc)
	}
}

func TestAllocateNoCards(t *testing.T) {
	if out := Allocate(nil, Spend{Dining: 1}); len(out) != 0 {
		t.Fatalf("%v", out)
	}
}

func TestBestComboBeatsCurrentWallet(t *testing.T) {
	a := cashback(0, 0)
	a.ID = "A"
	b := Card{ID: "B", BaseEarnBps: 150, RedeemPaisePerPoint: 100}
	bad := Card{ID: "BAD", AnnualFeePaise: 1_000_000, BaseEarnBps: 50, RedeemPaisePerPoint: 100}
	spend := Spend{OnlineShopping: 20_000_000, Dining: 2_000_000}

	res := BestCombo([]Card{a, b, bad}, []Card{bad}, spend)
	if len(res.CardIDs) != 2 || res.CardIDs[0] != "A" || res.CardIDs[1] != "B" {
		t.Fatalf("expected A+B, got %v", res.CardIDs)
	}
	// baseline: BAD alone => 0.5% of Rs22L/mo... minus fee; combo must be strictly better
	if res.GainPaise <= 0 || res.GainPaise != res.NetPaise-res.BaselinePaise {
		t.Fatalf("%+v", res)
	}
	// 2-card combo must be at least as good as either single card
	for _, c := range []Card{a, b} {
		if n, _ := evaluate([]Card{c}, spend, false); n > res.NetPaise {
			t.Fatalf("single %s (%d) beats combo (%d)", c.ID, n, res.NetPaise)
		}
	}
}

func TestBestComboEmptyCatalog(t *testing.T) {
	res := BestCombo(nil, nil, Spend{Dining: 100})
	if res.GainPaise != 0 || res.NetPaise != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		in   string
		want Category
		conf bool
	}{
		{"UPI/SWIGGY@ybl/4021", Dining, true},
		{"UPI-ZOMATO-ORDER 99812", Dining, true},
		{"SWIGGY INSTAMART BANGALORE", Groceries, true},
		{"BLINKIT", Groceries, true},
		{"AMAZON PAY INDIA", Excluded, true},
		{"AMAZON.IN MARKETPLACE", OnlineShopping, true},
		{"INDIGO AIRLINES 6E-221", FlightsHotels, true},
		{"AIR INDIA LIMITED", FlightsHotels, true},
		{"CRED RENT PAYMENT", Excluded, true},
		{"NOBROKER RENT", Excluded, true},
		{"LIC PREMIUM", Excluded, true},
		{"POS 4912 IOCL PETROL PUMP", Fuel, true},
		{"BPCL FUEL STN", Fuel, true},
		{"BESCOM ELECTRICITY BILL", Utilities, true},
		{"POS RANDOM KIRANA STORE", OfflinePOS, true},
		{"UPI/RAMESH TEA STALL", OfflinePOS, false},
	}
	for _, c := range cases {
		got := Classify(c.in)
		if got.Category != c.want || got.Confident != c.conf {
			t.Errorf("%q => %+v, want %s/%v", c.in, got, c.want, c.conf)
		}
	}
}
