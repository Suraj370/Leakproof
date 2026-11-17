package catalog

import (
	"leakproof/internal/cards"
	"leakproof/internal/mf"
)

// Catalog is the read-only reference data. In production this is loaded from PostgreSQL (cached in
// Redis) by the ingestion jobs; the seed below is ILLUSTRATIVE DEMO DATA, not real fund or card terms.
type Catalog struct {
	Funds map[string]mf.Fund
	Cards []cards.Card
}

func Seed() *Catalog {
	names := map[string]string{
		"INE040A01034": "HDFC Bank", "INE090A01021": "ICICI Bank", "INE009A01021": "Infosys",
		"INE002A01018": "Reliance Industries", "INE467B01029": "TCS",
		"INE280A01028": "Titan Company", "INE021A01026": "Asian Paints",
	}
	fund := func(isin, name string, plan mf.PlanType, ter, direct int64, h map[string]int64) mf.Fund {
		return mf.Fund{ISIN: isin, Name: name, Plan: plan, TERBps: ter, DirectTERBps: direct,
			EquityOriented: true, Holdings: h, HoldingNames: names}
	}
	funds := []mf.Fund{
		fund("INFDEMO00001", "Demo Bluechip Fund (Regular)", mf.PlanRegular, 160, 70, map[string]int64{
			"INE040A01034": 900, "INE090A01021": 800, "INE009A01021": 700, "INE002A01018": 600, "INE467B01029": 500}),
		fund("INFDEMO00002", "Demo Large Cap Index Fund (Direct)", mf.PlanDirect, 60, 60, map[string]int64{
			"INE040A01034": 850, "INE090A01021": 750, "INE009A01021": 650, "INE002A01018": 550, "INE467B01029": 450}),
		fund("INFDEMO00003", "Demo Consumption Fund (Direct)", mf.PlanDirect, 90, 90, map[string]int64{
			"INE009A01021": 200, "INE280A01028": 600, "INE021A01026": 500}),
	}
	m := make(map[string]mf.Fund, len(funds))
	for _, f := range funds {
		m[f.ISIN] = f
	}

	return &Catalog{Funds: m, Cards: []cards.Card{
		{
			ID: "demo-cashback", Name: "Demo Cashback Card (5% online, capped)",
			AnnualFeePaise: 59_900, FeeWaiverSpendPaise: 20_000_000, BaseEarnBps: 100, RedeemPaisePerPoint: 100,
			Rules: map[cards.Category]cards.CategoryRule{
				cards.OnlineShopping: {EarnBps: 500, CapGroup: "online"},
				cards.Fuel:           {Excluded: true},
			},
			GroupCapPoints: map[string]int64{"online": 5000},
		},
		{
			ID: "demo-travel", Name: "Demo Travel Card (points, Rs1/pt on flights)",
			AnnualFeePaise: 1_000_000, FeeWaiverSpendPaise: 800_000_00, BaseEarnBps: 333, RedeemPaisePerPoint: 100,
			Rules: map[cards.Category]cards.CategoryRule{
				cards.FlightsHotels: {EarnBps: 1000, CapGroup: "travel"},
				cards.Fuel:          {Excluded: true},
				cards.Utilities:     {EarnBps: 100},
			},
			GroupCapPoints: map[string]int64{"travel": 15_000},
		},
		{
			ID: "demo-flat", Name: "Demo Flat 1.5% Card",
			AnnualFeePaise: 0, BaseEarnBps: 150, RedeemPaisePerPoint: 100,
			Rules:          map[cards.Category]cards.CategoryRule{cards.Fuel: {Excluded: true}},
			GroupCapPoints: map[string]int64{},
		},
		{
			ID: "demo-points-low", Name: "Demo Bank Points Card (Rs0.25/pt statement credit)",
			AnnualFeePaise: 50_000, FeeWaiverSpendPaise: 100_000_00, BaseEarnBps: 400, RedeemPaisePerPoint: 25,
			Rules:          map[cards.Category]cards.CategoryRule{},
			GroupCapPoints: map[string]int64{},
		},
	}}
}
