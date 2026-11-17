// Package contract keeps the TypeScript interfaces in frontend/src honest. This test reflects over the Go
// API structs and compares their JSON field names with backend/testdata/contract_fields.json; the
// frontend's contracts.test.ts compares the same JSON with the interfaces in contracts.ts. Change a Go
// struct without updating the TS (or vice versa) and one of the two suites fails.
//
// Regenerate after an intentional change:  UPDATE_CONTRACT=1 go test ./internal/contract
package contract

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"leakproof/internal/api"
	"leakproof/internal/cards"
	"leakproof/internal/mf"
	"leakproof/internal/reports"
	"leakproof/internal/statement"
)

const file = "../../testdata/contract_fields.json"

// Key = TypeScript interface name, value = the Go type it mirrors.
var mirrored = map[string]reflect.Type{
	"ExitCost":       reflect.TypeOf(mf.ExitCost{}),
	"PairAction":     reflect.TypeOf(mf.PairAction{}),
	"SharedHolding":  reflect.TypeOf(mf.SharedHolding{}),
	"PairResult":     reflect.TypeOf(mf.PairResult{}),
	"Lot":            reflect.TypeOf(mf.Lot{}),
	"Position":       reflect.TypeOf(mf.Position{}),
	"CreditCard":     reflect.TypeOf(cards.Card{}),
	"Milestone":      reflect.TypeOf(cards.Milestone{}),
	"CategoryRule":   reflect.TypeOf(cards.CategoryRule{}),
	"CardYield":      reflect.TypeOf(cards.CardYield{}),
	"FundSummary":    reflect.TypeOf(api.FundSummary{}),
	"MFReport":       reflect.TypeOf(api.MFReport{}),
	"CASReport":      reflect.TypeOf(api.CASReport{}),
	"BankItem":       reflect.TypeOf(statement.Item{}),
	"BankSummary":    reflect.TypeOf(statement.Summary{}),
	"SavedScore":     reflect.TypeOf(reports.Report{}),
}

// fields returns the sorted JSON field names of a struct, flattening untagged embedded structs the way
// encoding/json does.
func fields(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "-" {
			continue
		}
		if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
			out = append(out, fields(f.Type)...)
			continue
		}
		if name == "" {
			name = f.Name
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func current() map[string][]string {
	m := map[string][]string{}
	for name, t := range mirrored {
		m[name] = fields(t)
	}
	return m
}

func TestGoStructsMatchContractFile(t *testing.T) {
	cur := current()
	if os.Getenv("UPDATE_CONTRACT") == "1" {
		b, _ := json.MarshalIndent(cur, "", "  ")
		if err := os.WriteFile(file, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("contract_fields.json updated")
		return
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string][]string
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cur, want) {
		for name, got := range cur {
			if !reflect.DeepEqual(got, want[name]) {
				t.Errorf("%s: Go has %v but contract file has %v", name, got, want[name])
			}
		}
		t.Fatal("Go API structs changed: update frontend/src/contracts.ts, then run UPDATE_CONTRACT=1 go test ./internal/contract")
	}
}

func TestEmbeddedStructsAreFlattened(t *testing.T) {
	got := fields(reflect.TypeOf(api.CASReport{}))
	for _, want := range []string{"funds", "pairs", "annualLeakPaise", "source", "warnings", "unmatchedIsins"} {
		found := false
		for _, g := range got {
			found = found || g == want
		}
		if !found {
			t.Errorf("CASReport missing %q in %v", want, got)
		}
	}
}
