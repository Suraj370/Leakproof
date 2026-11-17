package ingest

import (
	"strings"
	"testing"
)

func TestValidISIN(t *testing.T) {
	for _, ok := range []string{"INE040A01034", "INE009A01021", "INE002A01018", "INE467B01029", "INE090A01021"} {
		if !ValidISIN(ok) {
			t.Errorf("%s should be valid", ok)
		}
	}
	for _, bad := range []string{"INE040A01035", "INE040A0103", "ine040a01034", "", "INFDEMO00001", "1NE040A01034"} {
		if ValidISIN(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

const fundsCSV = `isin,amfi_code,name,amc,plan,category,equity_oriented,ter_bps,direct_ter_bps,exit_load_bps,exit_load_days,ter_as_of
INF000TEST01,111,Test Large Cap (Regular),Test AMC,regular,Large Cap,true,155,68,100,365,2025-03-31
INF000TEST02,112,Test Large Cap (Direct),Test AMC,DIRECT,Large Cap,true,68,,0,0,2025-03-31
BAD,1,x,y,DIRECT,z,true,10,,,,2025-03-31
INF000TEST03,113,Test Debt,Test AMC,DIRECT,Debt,false,900,,,,2025-03-31
INF000TEST01,114,Dup,Test AMC,DIRECT,Large Cap,true,50,,,,2025-03-31
INF000TEST04,115,Bad Date,Test AMC,DIRECT,Large Cap,true,50,,,,31-03-2025
`

func TestParseFundsCSV(t *testing.T) {
	rows, bad, err := ParseFundsCSV(strings.NewReader(fundsCSV))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || len(bad) != 4 {
		t.Fatalf("rows=%d bad=%v", len(rows), bad)
	}
	r := rows[0]
	if r.Plan != "REGULAR" || r.TERBps != 155 || r.DirectTERBps != 68 || r.ExitLoadBps != 100 || r.ExitLoadDays != 365 || !r.EquityOriented {
		t.Fatalf("%+v", r)
	}
	if rows[1].DirectTERBps != 68 { // blank direct_ter defaults to the fund's own TER
		t.Fatalf("%+v", rows[1])
	}
	for _, want := range []string{"bad ISIN", "ter_bps must be 0-500", "duplicate ISIN", "ter_as_of"} {
		found := false
		for _, b := range bad {
			found = found || strings.Contains(b, want)
		}
		if !found {
			t.Errorf("expected a %q message in %v", want, bad)
		}
	}
}

func TestParseFundsMissingColumn(t *testing.T) {
	if _, _, err := ParseFundsCSV(strings.NewReader("isin,name\nX,Y\n")); err == nil {
		t.Fatal("expected missing column error")
	}
}

func holdingsCSV(rows ...string) string {
	return "fund_isin,stock_isin,stock_name,weight_pct\n" + strings.Join(rows, "\n") + "\n"
}

func TestParseHoldingsHappyPath(t *testing.T) {
	csv := holdingsCSV(
		"INF000TEST01,INE040A01034,HDFC Bank,9.5",
		"INF000TEST01,INE090A01021,ICICI Bank,8.25",
		"INF000TEST01,INE009A01021,Infosys,30",
		"INF000TEST01,INE002A01018,Reliance,20",
	)
	funds, msgs, err := ParseHoldingsCSV(strings.NewReader(csv))
	if err != nil || len(msgs) != 0 {
		t.Fatalf("%v %v", err, msgs)
	}
	fh := funds["INF000TEST01"]
	if len(fh.Rows) != 4 || fh.SumBps != 6775 || fh.Rows[1].WeightBps != 825 {
		t.Fatalf("%+v", fh)
	}
}

func TestParseHoldingsGates(t *testing.T) {
	cases := map[string]struct {
		csv  string
		want string
	}{
		"bad stock isin": {holdingsCSV("INF000TEST01,INE040A01035,HDFC,50"), "invalid stock ISIN"},
		"weight zero":    {holdingsCSV("INF000TEST01,INE040A01034,HDFC,0"), "weight_pct"},
		"weight >100":    {holdingsCSV("INF000TEST01,INE040A01034,HDFC,101"), "weight_pct"},
		"duplicate":      {holdingsCSV("INF000TEST01,INE040A01034,HDFC,40", "INF000TEST01,INE040A01034,HDFC,40"), "duplicate"},
		"sum too low":    {holdingsCSV("INF000TEST01,INE040A01034,HDFC,10"), "weights sum to 10.00%"},
		"sum too high":   {holdingsCSV("INF000TEST01,INE040A01034,HDFC,60", "INF000TEST01,INE009A01021,Infosys,50"), "weights sum to 110.00%"},
		"empty name":     {holdingsCSV("INF000TEST01,INE040A01034,,50"), "stock_name required"},
	}
	for name, c := range cases {
		funds, msgs, err := ParseHoldingsCSV(strings.NewReader(c.csv))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, kept := funds["INF000TEST01"]; kept {
			t.Errorf("%s: fund should have been rejected", name)
		}
		if !strings.Contains(strings.Join(msgs, "\n"), c.want) {
			t.Errorf("%s: want %q in %v", name, c.want, msgs)
		}
	}
}

func TestOneBadRowRejectsOnlyThatFund(t *testing.T) {
	csv := holdingsCSV(
		"INF000TEST01,INE040A01034,HDFC Bank,50",
		"INF000TEST01,INE040A01099,Typo,10",
		"INF000TEST02,INE040A01034,HDFC Bank,50",
	)
	funds, _, _ := ParseHoldingsCSV(strings.NewReader(csv))
	if _, ok := funds["INF000TEST01"]; ok {
		t.Fatal("poisoned fund kept")
	}
	if _, ok := funds["INF000TEST02"]; !ok {
		t.Fatal("healthy fund dropped")
	}
}

func TestCarryOver(t *testing.T) {
	prev := map[string]int64{"A": 500, "B": 300}
	next := []HoldingRow{{"A", "a", 200}, {"B", "b", 400}, {"C", "c", 900}}
	if got := CarryOverBps(prev, next); got != 500 { // min(500,200)+min(300,400)
		t.Fatalf("%d", got)
	}
}

const amfiTxt = `Scheme Code;ISIN Div Payout/ ISIN Growth;ISIN Div Reinvestment;Scheme Name;Net Asset Value;Date

Open Ended Schemes(Equity Scheme - Large Cap Fund)

Demo Mutual Fund

119551;INF179K01AA1;-;Demo Bluechip Fund - Direct Plan - Growth;110.7;04-Nov-2025
119552;INF179K01AB9;INF179K01AC7;Demo Bluechip Fund - Regular Plan - IDCW;98.1;04-Nov-2025

Open Ended Schemes(Debt Scheme - Liquid Fund)

Other Mutual Fund

120001;;;Other Liquid Fund;1000.5;04-Nov-2025
notanumber;X;Y;Broken row;1;04-Nov-2025
`

func TestParseAMFI(t *testing.T) {
	es, err := ParseAMFI(strings.NewReader(amfiTxt))
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 3 {
		t.Fatalf("%d entries: %+v", len(es), es)
	}
	a, b, c := es[0], es[1], es[2]
	if a.Code != 119551 || a.Plan != "DIRECT" || a.AMC != "Demo Mutual Fund" || a.Category != "Equity Scheme - Large Cap Fund" ||
		len(a.ISINs) != 1 || a.ISINs[0] != "INF179K01AA1" {
		t.Fatalf("%+v", a)
	}
	if b.Plan != "REGULAR" || len(b.ISINs) != 2 {
		t.Fatalf("%+v", b)
	}
	if c.Category != "Debt Scheme - Liquid Fund" || c.AMC != "Other Mutual Fund" || len(c.ISINs) != 0 {
		t.Fatalf("%+v", c)
	}
	if _, err := ParseAMFI(strings.NewReader("hello\nworld\n")); err == nil {
		t.Fatal("expected error for non-AMFI file")
	}
}

const cardsJSON = `[{
  "slug": "test-cashback", "issuer": "Test Bank", "effectiveFrom": "2025-04-01", "changeNote": "cap cut",
  "card": {
    "name": "Test Cashback", "annualFeePaise": 59900, "feeWaiverSpendPaise": 20000000,
    "baseEarnBps": 100, "redeemPaisePerPoint": 100,
    "rules": {"ONLINE_SHOPPING": {"earnBps": 500, "excluded": false, "capGroup": "online"}, "FUEL": {"earnBps": 0, "excluded": true, "capGroup": ""}},
    "groupCapPoints": {"online": 2000}
  }
}]`

func TestParseCardsJSON(t *testing.T) {
	in, err := ParseCardsJSON(strings.NewReader(cardsJSON))
	if err != nil || len(in) != 1 {
		t.Fatalf("%v %+v", err, in)
	}
	if in[0].Card.ID != "test-cashback" || in[0].Card.GroupCapPoints["online"] != 2000 {
		t.Fatalf("%+v", in[0])
	}
}

func TestParseCardsRejects(t *testing.T) {
	bad := map[string]string{
		"unknown field":   strings.Replace(cardsJSON, `"issuer"`, `"bogus": 1, "issuer"`, 1),
		"bad slug":        strings.Replace(cardsJSON, `"test-cashback"`, `"Bad Slug"`, 1),
		"bad date":        strings.Replace(cardsJSON, `2025-04-01`, `01/04/2025`, 1),
		"unknown cat":     strings.Replace(cardsJSON, `ONLINE_SHOPPING`, `SPACE_TRAVEL`, 1),
		"cap w/o points":  strings.Replace(cardsJSON, `"online": 2000`, `"other": 1`, 1),
		"zero redeem":     strings.Replace(cardsJSON, `"redeemPaisePerPoint": 100`, `"redeemPaisePerPoint": 0`, 1),
		"earn too high":   strings.Replace(cardsJSON, `"earnBps": 500`, `"earnBps": 9000`, 1),
		"not json":        `[`,
	}
	for name, js := range bad {
		if _, err := ParseCardsJSON(strings.NewReader(js)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	dup := "[" + strings.Trim(cardsJSON, "[]") + "," + strings.Trim(cardsJSON, "[]") + "]"
	if _, err := ParseCardsJSON(strings.NewReader(dup)); err == nil {
		t.Error("duplicate slug accepted")
	}
}

func TestParseFundsGrandfatherNAV(t *testing.T) {
	csv := "isin,name,amc,plan,category,equity_oriented,ter_bps,ter_as_of,nav_2018_01_31\n" +
		"INF000TEST01,A,X,DIRECT,Large Cap,true,60,2025-03-31,41.2345\n" +
		"INF000TEST02,B,X,DIRECT,Large Cap,true,60,2025-03-31,\n" +
		"INF000TEST03,C,X,DIRECT,Large Cap,true,60,2025-03-31,-5\n" +
		"INF000TEST04,D,X,DIRECT,Large Cap,true,60,2025-03-31,abc\n"
	rows, bad, err := ParseFundsCSV(strings.NewReader(csv))
	if err != nil || len(rows) != 2 || len(bad) != 2 {
		t.Fatalf("%v rows=%d bad=%v", err, len(rows), bad)
	}
	if rows[0].GrandfatherNAV != "41.2345" || rows[1].GrandfatherNAV != "" {
		t.Fatalf("%+v", rows)
	}
}
