package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"leakproof/internal/mf"
)

func upload(t *testing.T, file, password string, extra map[string]string) (int, []byte) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if file != "" {
		data, err := os.ReadFile("../../testdata/" + file)
		if err != nil {
			t.Fatal(err)
		}
		fw, _ := w.CreateFormFile("file", "cas.pdf")
		fw.Write(data)
	}
	w.WriteField("password", password)
	for k, v := range extra {
		w.WriteField(k, v)
	}
	w.Close()

	app := NewApp(&Server{Cat: SeedCatalog(), Now: func() time.Time { return time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC) }})
	req := httptest.NewRequest("POST", "/api/v1/analyze/cas", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestAnalyzeCASEncryptedPDF(t *testing.T) {
	code, b := upload(t, "cas_sample_aes128.pdf", "userpw", nil)
	if code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	var r CASReport
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.Source != "CAMS" || len(r.Funds) != 2 || len(r.Pairs) != 1 {
		t.Fatalf("%s", b)
	}
	if r.Pairs[0].OverlapBps != 3250 || r.Pairs[0].Action.Verdict != "CONSOLIDATE_NOW" {
		t.Fatalf("%+v", r.Pairs[0])
	}
	if r.AnnualLeakPaise != 90_000 { // 1% of Rs90,000, no tax because gains sit inside the Rs1.25L exemption
		t.Fatalf("leak %d", r.AnnualLeakPaise)
	}
	if len(r.Warnings) != 1 || len(r.UnmatchedISINs) != 0 {
		t.Fatalf("%s", b)
	}
	for _, secret := range []string{"ABCDE1234F", "12345678", "someone@", "TEST USER"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("response leaked %q", secret)
		}
	}
}

func TestAnalyzeCASErrors(t *testing.T) {
	cases := []struct {
		name, file, pw string
		extra          map[string]string
		want           int
	}{
		{"wrong password", "cas_sample_aes128.pdf", "nope", nil, 401},
		{"missing password", "cas_sample_aes128.pdf", "", nil, 401},
		{"aes256 unsupported", "cas_sample_aes256.pdf", "userpw", nil, 415},
		{"no file", "", "x", nil, 400},
		{"not a pdf", "cas_sample.txt", "", nil, 400},
		{"bad slab", "cas_sample.pdf", "", map[string]string{"slabBps": "99999"}, 400},
		{"slab not a number", "cas_sample.pdf", "", map[string]string{"slabBps": "abc"}, 400},
	}
	for _, c := range cases {
		if code, b := upload(t, c.file, c.pw, c.extra); code != c.want {
			t.Errorf("%s: got %d want %d (%s)", c.name, code, c.want, b)
		}
	}
}

func TestBuildReportSkipsUnknownSchemes(t *testing.T) {
	s := &Server{Cat: SeedCatalog(), Now: func() time.Time { return time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC) }, Workers: 2}
	lot := mf.Lot{PurchaseDate: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), CostPaise: 100, ValuePaise: 200}
	pos := []mf.Position{
		{FundISIN: "INFDEMO00001", Lots: []mf.Lot{lot}},
		{FundISIN: "INF999999999", Lots: []mf.Lot{lot}},
		{FundISIN: "INF999999999", Lots: []mf.Lot{lot}},
	}
	rep, unmatched, err := s.buildReport(context.Background(), pos, 3000, 0, true)
	if err != nil || len(rep.Funds) != 1 || len(unmatched) != 1 || unmatched[0] != "INF999999999" {
		t.Fatalf("%v %+v %v", err, rep, unmatched)
	}
	if _, _, err := s.buildReport(context.Background(), pos[1:2], 3000, 0, true); err == nil {
		t.Fatal("expected error when nothing matches the catalog")
	}
	if _, _, err := s.buildReport(context.Background(), pos[1:2], 3000, 0, false); err == nil {
		t.Fatal("expected error for unknown ISIN in strict mode")
	}
}
