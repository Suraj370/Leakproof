package cas

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"leakproof/internal/mf"
)

func sample(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../testdata/cas_sample.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseSample(t *testing.T) {
	r, err := Parse(sample(t), mf.NewSalt())
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != "CAMS" {
		t.Fatalf("source %s", r.Source)
	}
	if len(r.Positions) != 2 { // the fully redeemed scheme is dropped
		t.Fatalf("positions %d", len(r.Positions))
	}

	p := r.Positions[0]
	if p.FundISIN != "INFDEMO00001" || len(p.Lots) != 2 {
		t.Fatalf("%+v", p)
	}
	// FIFO: the 150-unit redemption eats the oldest lot: 500 -> 350 units, cost 50,000 * 350/500 = 35,000.
	l0, l1 := p.Lots[0], p.Lots[1]
	if l0.Units.String() != "350" || l0.CostPaise != 3_500_000 || l0.ValuePaise != 7_000_000 ||
		!l0.PurchaseDate.Equal(time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("lot0 %+v", l0)
	}
	if l1.Units.String() != "100" || l1.CostPaise != 1_000_000 || l1.ValuePaise != 2_000_000 {
		t.Fatalf("lot1 %+v", l1)
	}
	if p.ValuePaise() != 9_000_000 { // matches the statement's Market Value Rs90,000
		t.Fatalf("value %d", p.ValuePaise())
	}
}

func TestParseUnexplainedOpeningUnitsAreDatedConservatively(t *testing.T) {
	r, err := Parse(sample(t), mf.NewSalt())
	if err != nil {
		t.Fatal(err)
	}
	p := r.Positions[1]
	if p.FundISIN != "INFDEMO00002" || len(p.Lots) != 2 {
		t.Fatalf("%+v", p)
	}
	gap := p.Lots[0]
	if gap.Units.String() != "100" || !gap.PurchaseDate.Equal(time.Date(2025, 3, 31, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("gap lot %+v", gap) // dated at the valuation date => short-term => tax can only be overstated
	}
	if p.ValuePaise() != 1_320_000 { // matches Market Value Rs13,200
		t.Fatalf("value %d", p.ValuePaise())
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "INFDEMO00002") {
		t.Fatalf("warnings %v", r.Warnings)
	}
}

func TestParseResultContainsNoPII(t *testing.T) {
	r, err := Parse(sample(t), mf.NewSalt())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	for _, secret := range []string{"ABCDE1234F", "12345678", "someone@", "9876543210", "TEST USER", "ARN-0000"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("leaked %q in %s", secret, b)
		}
	}
	if len(r.Positions[0].FolioRef) != 12 || r.Positions[0].FolioRef == r.Positions[1].FolioRef {
		t.Fatalf("folio refs %q %q", r.Positions[0].FolioRef, r.Positions[1].FolioRef)
	}
}

func TestParseSaltMakesFolioRefUnlinkable(t *testing.T) {
	a, _ := Parse(sample(t), mf.NewSalt())
	b, _ := Parse(sample(t), mf.NewSalt())
	if a.Positions[0].FolioRef == b.Positions[0].FolioRef {
		t.Fatal("same folio ref across requests")
	}
}

func TestParseKFintechSource(t *testing.T) {
	txt := strings.Replace(sample(t), "CAMS - Computer Age Management Services", "KFINTECH Consolidated", 1)
	txt = strings.ReplaceAll(txt, "Registrar : CAMS", "Registrar : KFINTECH")
	r, err := Parse(txt, mf.NewSalt())
	if err != nil || r.Source != "KFINTECH" {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"empty":           "",
		"not a statement": "hello world\nnothing here",
		"missing closing": "Folio No: 1\nFund ISIN: INFDEMO00001\n10-Jan-2023 Purchase 100.00 1.000 100.0000 1.000\n",
		"txns exceed closing": "Folio No: 1\nFund ISIN: INFDEMO00001\n" +
			"10-Jan-2023 Purchase 100.00 10.000 10.0000 10.000\n" +
			"Closing Unit Balance: 1.000 NAV on 31-Mar-2025: INR 10.00\n",
	}
	for name, txt := range cases {
		if _, err := Parse(txt, mf.NewSalt()); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseNumHandlesParensAndCommas(t *testing.T) {
	d, err := parseNum("(1,234.50)")
	if err != nil || d.String() != "-1234.5" {
		t.Fatalf("%v %v", d, err)
	}
}
