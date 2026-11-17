package api

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func reportApp() *fiber.App {
	return NewApp(&Server{Cat: SeedCatalog(), Now: func() time.Time { return time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC) }})
}

func doApp(t *testing.T, app *fiber.App, method, path, body string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

const goodReport = `{"rewardYieldBps":250,"overlapBps":3250,"annualLeakPaise":1500000,"cardLeakPaise":500000,"mfLeakPaise":1000000}`

func TestSaveAndFetchReport(t *testing.T) {
	app := reportApp()
	code, b := doApp(t, app, "POST", "/api/v1/reports", goodReport)
	if code != 201 {
		t.Fatalf("%d %s", code, b)
	}
	var saved struct{ Slug string }
	_ = json.Unmarshal(b, &saved)
	if len(saved.Slug) != 10 {
		t.Fatalf("%s", b)
	}

	code, b = doApp(t, app, "GET", "/api/v1/reports/"+saved.Slug, "")
	if code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	var r map[string]any
	_ = json.Unmarshal(b, &r)
	if r["overlapBps"].(float64) != 3250 || r["mfRulesetMonth"] != "2025-06" || r["cardRulesetVersion"] != "catalog-2025-06" {
		t.Fatalf("%s", b)
	}
}

func TestClientCannotChooseRulesetLabels(t *testing.T) {
	app := reportApp()
	body := `{"rewardYieldBps":1,"overlapBps":1,"annualLeakPaise":0,"cardLeakPaise":0,"mfLeakPaise":0,"mfRulesetMonth":"1999-01","cardRulesetVersion":"<script>"}`
	_, b := doApp(t, app, "POST", "/api/v1/reports", body)
	var s struct{ Slug string }
	_ = json.Unmarshal(b, &s)
	_, b = doApp(t, app, "GET", "/api/v1/reports/"+s.Slug, "")
	if strings.Contains(string(b), "1999") || strings.Contains(string(b), "script") {
		t.Fatalf("client-supplied labels stored: %s", b)
	}
}

func TestReportValidationAndNotFound(t *testing.T) {
	app := reportApp()
	for name, body := range map[string]string{
		"bad json":      `{`,
		"overlap>100%":  `{"overlapBps":10001}`,
		"parts mismatch": `{"annualLeakPaise":5,"cardLeakPaise":1,"mfLeakPaise":1}`,
	} {
		if code, b := doApp(t, app, "POST", "/api/v1/reports", body); code != 400 {
			t.Errorf("%s: %d %s", name, code, b)
		}
	}
	for _, slug := range []string{"aaaaaaaaaa", "short", "UPPERCASE1", "../etc/passwd"} {
		if code, _ := doApp(t, app, "GET", "/api/v1/reports/"+slug, ""); code != 404 {
			t.Errorf("slug %q: %d", slug, code)
		}
	}
}

func TestSaveIsRateLimited(t *testing.T) {
	app := reportApp()
	var last int
	for i := 0; i < 25; i++ {
		last, _ = doApp(t, app, "POST", "/api/v1/reports", goodReport)
	}
	if last != 429 {
		t.Fatalf("expected 429 after burst, got %d", last)
	}
}
