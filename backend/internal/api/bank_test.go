package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"leakproof/internal/statement"
)

func uploadBank(t *testing.T, name, content string) (int, []byte) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if name != "" {
		fw, _ := w.CreateFormFile("file", name)
		fw.Write([]byte(content))
	}
	w.Close()
	app := NewApp(&Server{Cat: SeedCatalog()})
	req := httptest.NewRequest("POST", "/api/v1/analyze/bank", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestAnalyzeBank(t *testing.T) {
	csv := "Date,Description,Debit\n01/01/2025,UPI/SWIGGY@ybl,\"1,500\"\n02/01/2025,CRED RENT,\"20,000\"\n"
	code, b := uploadBank(t, "stmt.csv", csv)
	if code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	var s statement.Summary
	_ = json.Unmarshal(b, &s)
	if s.Months != 1 || s.MonthlySpendPaise["DINING"] != 150_000 || s.MonthlySpendPaise["EXCLUDED"] != 2_000_000 {
		t.Fatalf("%s", b)
	}
}

func TestAnalyzeBankErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		file, content string
		want          int
	}{
		"no file":    {"", "", 400},
		"empty":      {"a.csv", "", 400},
		"no header":  {"a.csv", "foo,bar\n1,2\n", 422},
		"no spend":   {"a.csv", "Date,Description,Debit\n", 422},
	} {
		if code, b := uploadBank(t, tc.file, tc.content); code != tc.want {
			t.Errorf("%s: %d %s", name, code, b)
		}
	}
	_, b := uploadBank(t, "a.csv", "foo,SECRET-DESC\n")
	if strings.Contains(string(b), "SECRET-DESC") {
		t.Fatal("error echoed file content")
	}
}
