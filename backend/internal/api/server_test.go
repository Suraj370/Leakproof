package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func do(t *testing.T, method, path, body string) (*http.Response, []byte) {
	t.Helper()
	app := NewApp(&Server{Cat: SeedCatalog(), Now: func() time.Time { return time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC) }})
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

const demoPositions = `{"positions":[
 {"fundIsin":"INFDEMO00001","lots":[{"purchaseDate":"2023-01-10T00:00:00Z","units":"100","costPaise":80000000,"valuePaise":100000000}]},
 {"fundIsin":"INFDEMO00002","lots":[{"purchaseDate":"2023-01-10T00:00:00Z","units":"100","costPaise":9000000,"valuePaise":10000000}]},
 {"fundIsin":"INFDEMO00003","lots":[{"purchaseDate":"2024-02-10T00:00:00Z","units":"100","costPaise":9000000,"valuePaise":10000000}]}
]}`

func TestHealthzAndNoStore(t *testing.T) {
	resp, _ := do(t, "GET", "/healthz", "")
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
}

func TestAnalyzeMF(t *testing.T) {
	resp, b := do(t, "POST", "/api/v1/analyze/mf", demoPositions)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	var r MFReport
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Funds) != 3 || len(r.Pairs) != 3 {
		t.Fatalf("%+v", r)
	}
	p := r.Pairs[0] // bluechip regular x index fund
	if p.OverlapBps != 3250 {
		t.Fatalf("overlap %d", p.OverlapBps)
	}
	if p.Action.Verdict != "CONSOLIDATE_NOW" || p.Action.ExitFundIndex != 0 {
		t.Fatalf("%+v", p.Action)
	}
	if r.AnnualLeakPaise != 1_000_000 {
		t.Fatalf("leak %d", r.AnnualLeakPaise)
	}
	if r.PortfolioOverlapBps <= 0 {
		t.Fatalf("overlap score %d", r.PortfolioOverlapBps)
	}
}

func TestAnalyzeMergesFoliosOfSameScheme(t *testing.T) {
	body := `{"positions":[
	 {"fundIsin":"INFDEMO00001","folioRef":"a","lots":[{"purchaseDate":"2023-01-10T00:00:00Z","units":"1","costPaise":100,"valuePaise":200}]},
	 {"fundIsin":"INFDEMO00001","folioRef":"b","lots":[{"purchaseDate":"2023-01-10T00:00:00Z","units":"1","costPaise":100,"valuePaise":300}]}]}`
	resp, b := do(t, "POST", "/api/v1/analyze/mf", body)
	var r MFReport
	_ = json.Unmarshal(b, &r)
	if resp.StatusCode != 200 || len(r.Funds) != 1 || r.Funds[0].ValuePaise != 500 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}

func TestAnalyzeValidation(t *testing.T) {
	cases := map[string]string{
		"bad json":    `{`,
		"empty":       `{"positions":[]}`,
		"unknown isin": `{"positions":[{"fundIsin":"NOPE","lots":[]}]}`,
		"neg slab":    `{"slabBps":-1,"positions":[{"fundIsin":"INFDEMO00001","lots":[]}]}`,
		"neg lot":     `{"positions":[{"fundIsin":"INFDEMO00001","lots":[{"purchaseDate":"2023-01-10T00:00:00Z","units":"1","costPaise":-1,"valuePaise":1}]}]}`,
	}
	for name, body := range cases {
		if resp, b := do(t, "POST", "/api/v1/analyze/mf", body); resp.StatusCode != 400 {
			t.Errorf("%s: %d %s", name, resp.StatusCode, b)
		}
	}
}

func TestErrorsDoNotEchoPII(t *testing.T) {
	_, b := do(t, "POST", "/api/v1/analyze/mf", `{"positions":[{"fundIsin":"ABCDE1234F"}`)
	if strings.Contains(string(b), "ABCDE1234F") {
		t.Fatalf("error echoed input: %s", b)
	}
}

func TestSolveCards(t *testing.T) {
	body := `{"monthlySpendPaise":{"ONLINE_SHOPPING":20000000,"DINING":2000000,"FLIGHTS_HOTELS":5000000},"currentCardIds":["demo-points-low"]}`
	resp, b := do(t, "POST", "/api/v1/cards/solve", body)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	var r struct {
		CardIDs   []string `json:"cardIds"`
		GainPaise int64    `json:"netAnnualRupeesGainedPaise"`
	}
	_ = json.Unmarshal(b, &r)
	if len(r.CardIDs) == 0 || len(r.CardIDs) > 2 || r.GainPaise <= 0 {
		t.Fatalf("%s", b)
	}
}

func TestSolveCardsValidation(t *testing.T) {
	for name, body := range map[string]string{
		"bad category": `{"monthlySpendPaise":{"NOPE":1}}`,
		"negative":     `{"monthlySpendPaise":{"DINING":-5}}`,
		"unknown card": `{"monthlySpendPaise":{"DINING":5},"currentCardIds":["x"]}`,
	} {
		if resp, b := do(t, "POST", "/api/v1/cards/solve", body); resp.StatusCode != 400 {
			t.Errorf("%s: %d %s", name, resp.StatusCode, b)
		}
	}
}

func TestClassifyEndpoint(t *testing.T) {
	resp, b := do(t, "POST", "/api/v1/classify", `{"descriptions":["UPI/SWIGGY@ybl","CRED RENT"]}`)
	var out []classifyItem
	_ = json.Unmarshal(b, &out)
	if resp.StatusCode != 200 || len(out) != 2 || out[0].Category != "DINING" || out[1].Category != "EXCLUDED" {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}

func TestCatalogCards(t *testing.T) {
	resp, b := do(t, "GET", "/api/v1/catalog/cards", "")
	var cs []map[string]any
	_ = json.Unmarshal(b, &cs)
	if resp.StatusCode != 200 || len(cs) != 4 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}
