// Package ingest loads reference data (funds, TER, monthly holdings, card rules) into PostgreSQL.
// Every loader validates before writing and is transactional and idempotent, so a bad file can never
// leave the catalog half-updated.
package ingest

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"leakproof/internal/cards"
)

// ---- ISIN ----

var isinShape = regexp.MustCompile(`^[A-Z]{2}[A-Z0-9]{9}[0-9]$`)

// ValidISIN checks shape and the Luhn check digit (letters expand to 10..35).
func ValidISIN(s string) bool {
	if !isinShape.MatchString(s) {
		return false
	}
	var digits []int
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			v := int(r-'A') + 10
			digits = append(digits, v/10, v%10)
		} else {
			digits = append(digits, int(r-'0'))
		}
	}
	sum, double := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		n := digits[i]
		if double {
			if n *= 2; n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return sum%10 == 0
}

// fundISIN only checks shape: scheme ISINs are issued by NSDL and we don't want to reject real ones on a checksum edge.
func shapeOK(s string) bool { return isinShape.MatchString(s) }

// ---- CSV helpers ----

func readCSV(r io.Reader, required ...string) (idx map[string]int, rows [][]string, err error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("empty or unreadable CSV: %w", err)
	}
	idx = map[string]int{}
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, c := range required {
		if _, ok := idx[c]; !ok {
			return nil, nil, fmt.Errorf("missing column %q", c)
		}
	}
	for {
		row, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return idx, rows, nil
		}
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, row)
	}
}

func col(row []string, idx map[string]int, name string) string {
	if i, ok := idx[name]; ok && i < len(row) {
		return strings.TrimSpace(row[i])
	}
	return ""
}

// ---- funds ----

type FundRow struct {
	ISIN           string
	AMFICode       int
	Name           string
	AMC            string
	Plan           string // DIRECT | REGULAR
	Category       string
	EquityOriented bool
	TERBps         int64
	DirectTERBps   int64
	ExitLoadBps    int64
	ExitLoadDays   int
	TERAsOf        time.Time
	GrandfatherNAV string // optional NAV on 31-Jan-2018 (decimal text); "" = unknown
}

// ParseFundsCSV columns: isin,amfi_code,name,amc,plan,category,equity_oriented,ter_bps,direct_ter_bps,
// exit_load_bps,exit_load_days,ter_as_of(YYYY-MM-DD) and optionally nav_2018_01_31. Returns rows plus one error string per bad row.
func ParseFundsCSV(r io.Reader) ([]FundRow, []string, error) {
	idx, rows, err := readCSV(r, "isin", "name", "amc", "plan", "category", "equity_oriented", "ter_bps", "ter_as_of")
	if err != nil {
		return nil, nil, err
	}
	var out []FundRow
	var bad []string
	seen := map[string]bool{}
	for i, row := range rows {
		line := i + 2
		f, err := parseFundRow(row, idx)
		switch {
		case err != nil:
			bad = append(bad, fmt.Sprintf("line %d: %v", line, err))
		case seen[f.ISIN]:
			bad = append(bad, fmt.Sprintf("line %d: duplicate ISIN %s", line, f.ISIN))
		default:
			seen[f.ISIN] = true
			out = append(out, f)
		}
	}
	return out, bad, nil
}

func parseFundRow(row []string, idx map[string]int) (FundRow, error) {
	var f FundRow
	var err error
	f.ISIN = strings.ToUpper(col(row, idx, "isin"))
	f.Name, f.AMC, f.Category = col(row, idx, "name"), col(row, idx, "amc"), col(row, idx, "category")
	f.Plan = strings.ToUpper(col(row, idx, "plan"))
	switch {
	case !shapeOK(f.ISIN):
		return f, fmt.Errorf("bad ISIN %q", f.ISIN)
	case f.Name == "" || f.AMC == "" || f.Category == "":
		return f, errors.New("name, amc and category are required")
	case f.Plan != "DIRECT" && f.Plan != "REGULAR":
		return f, fmt.Errorf("plan must be DIRECT or REGULAR, got %q", f.Plan)
	}
	if f.EquityOriented, err = strconv.ParseBool(strings.ToLower(col(row, idx, "equity_oriented"))); err != nil {
		return f, errors.New("equity_oriented must be true/false")
	}
	if f.TERBps, err = strconv.ParseInt(col(row, idx, "ter_bps"), 10, 64); err != nil || f.TERBps < 0 || f.TERBps > 500 {
		return f, errors.New("ter_bps must be 0-500")
	}
	f.DirectTERBps = f.TERBps
	if v := col(row, idx, "direct_ter_bps"); v != "" {
		if f.DirectTERBps, err = strconv.ParseInt(v, 10, 64); err != nil || f.DirectTERBps < 0 || f.DirectTERBps > f.TERBps && f.Plan == "REGULAR" {
			return f, errors.New("direct_ter_bps must be 0-500 and not above the regular TER")
		}
	}
	if v := col(row, idx, "exit_load_bps"); v != "" {
		if f.ExitLoadBps, err = strconv.ParseInt(v, 10, 64); err != nil || f.ExitLoadBps < 0 || f.ExitLoadBps > 500 {
			return f, errors.New("exit_load_bps must be 0-500")
		}
	}
	if v := col(row, idx, "exit_load_days"); v != "" {
		if d, err := strconv.Atoi(v); err != nil || d < 0 || d > 3650 {
			return f, errors.New("exit_load_days must be 0-3650")
		} else {
			f.ExitLoadDays = d
		}
	}
	if v := col(row, idx, "amfi_code"); v != "" {
		if f.AMFICode, err = strconv.Atoi(v); err != nil {
			return f, errors.New("amfi_code must be an integer")
		}
	}
	if f.TERAsOf, err = time.Parse("2006-01-02", col(row, idx, "ter_as_of")); err != nil {
		return f, errors.New("ter_as_of must be YYYY-MM-DD")
	}
	if v := col(row, idx, "nav_2018_01_31"); v != "" {
		if n, err := decimal.NewFromString(v); err != nil || !n.IsPositive() || n.GreaterThan(decimal.NewFromInt(1_000_000)) {
			return f, errors.New("nav_2018_01_31 must be a positive decimal")
		}
		f.GrandfatherNAV = v
	}
	return f, nil
}

// ---- holdings ----

type HoldingRow struct {
	StockISIN string
	StockName string
	WeightBps int64
}

type FundHoldings struct {
	FundISIN string
	Rows     []HoldingRow
	SumBps   int64
}

const (
	MinHoldingsSumBps = 4000  // a fund disclosing <40% of its book is almost certainly a truncated file
	MaxHoldingsSumBps = 10050 // allow tiny rounding over 100%
	MinCarryOverBps   = 4000  // month-over-month: <40% overlap with last month needs --force
)

// ParseHoldingsCSV columns: fund_isin,stock_isin,stock_name,weight_pct. Row-level problems and fund-level
// gate failures are returned as messages; rejected funds are NOT in the result.
func ParseHoldingsCSV(r io.Reader) (map[string]*FundHoldings, []string, error) {
	idx, rows, err := readCSV(r, "fund_isin", "stock_isin", "stock_name", "weight_pct")
	if err != nil {
		return nil, nil, err
	}
	funds := map[string]*FundHoldings{}
	dupe := map[string]bool{}
	badFund := map[string]bool{}
	var msgs []string
	for i, row := range rows {
		line := i + 2
		fi := strings.ToUpper(col(row, idx, "fund_isin"))
		si := strings.ToUpper(col(row, idx, "stock_isin"))
		name := col(row, idx, "stock_name")
		w, werr := decimal.NewFromString(col(row, idx, "weight_pct"))
		var problem string
		switch {
		case !shapeOK(fi):
			problem = fmt.Sprintf("bad fund ISIN %q", fi)
		case !ValidISIN(si):
			problem = fmt.Sprintf("invalid stock ISIN %q", si)
		case name == "":
			problem = "stock_name required"
		case werr != nil || !w.IsPositive() || w.GreaterThan(decimal.NewFromInt(100)):
			problem = "weight_pct must be in (0, 100]"
		case dupe[fi+"|"+si]:
			problem = fmt.Sprintf("duplicate stock %s in fund %s", si, fi)
		}
		if problem != "" {
			msgs = append(msgs, fmt.Sprintf("line %d: %s", line, problem))
			if shapeOK(fi) {
				badFund[fi] = true // any bad row poisons the fund: partial books corrupt overlap math
			}
			continue
		}
		dupe[fi+"|"+si] = true
		fh := funds[fi]
		if fh == nil {
			fh = &FundHoldings{FundISIN: fi}
			funds[fi] = fh
		}
		bps := w.Mul(decimal.NewFromInt(100)).Round(0).IntPart()
		fh.Rows = append(fh.Rows, HoldingRow{si, name, bps})
		fh.SumBps += bps
	}
	for isin, fh := range funds {
		switch {
		case badFund[isin]:
			msgs = append(msgs, fmt.Sprintf("fund %s rejected: file had invalid rows", isin))
			delete(funds, isin)
		case fh.SumBps < MinHoldingsSumBps || fh.SumBps > MaxHoldingsSumBps:
			msgs = append(msgs, fmt.Sprintf("fund %s rejected: weights sum to %.2f%% (expected %.0f-%.2f%%)",
				isin, float64(fh.SumBps)/100, float64(MinHoldingsSumBps)/100, float64(MaxHoldingsSumBps)/100))
			delete(funds, isin)
		}
	}
	return funds, msgs, nil
}

// CarryOverBps is sum(min(prev, next)) in bps: how much of last month's book survives.
func CarryOverBps(prev map[string]int64, next []HoldingRow) int64 {
	var t int64
	for _, r := range next {
		if p, ok := prev[r.StockISIN]; ok {
			t += min(p, r.WeightBps)
		}
	}
	return t
}

// ---- AMFI NAVAll.txt ----

type AMFIEntry struct {
	Code     int
	ISINs    []string // growth / payout and reinvestment ISINs that are present
	Name     string
	AMC      string
	Category string // e.g. "Equity Scheme - Large Cap Fund"
	Plan     string // DIRECT | REGULAR
}

var reCategory = regexp.MustCompile(`^(?:Open|Close|Interval)\s+Ended Schemes?\((.+)\)\s*$`)

// ParseAMFI reads the semicolon-separated NAVAll.txt: AMC and category appear as unseparated header lines.
func ParseAMFI(r io.Reader) ([]AMFIEntry, error) {
	b, err := io.ReadAll(io.LimitReader(r, 64<<20))
	if err != nil {
		return nil, err
	}
	var out []AMFIEntry
	var amc, category string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Scheme Code") {
			continue
		}
		parts := strings.Split(line, ";")
		if len(parts) < 6 {
			if m := reCategory.FindStringSubmatch(line); m != nil {
				category = m[1]
			} else {
				amc = line
			}
			continue
		}
		code, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			continue
		}
		e := AMFIEntry{Code: code, Name: strings.TrimSpace(parts[3]), AMC: amc, Category: category, Plan: "REGULAR"}
		for _, p := range parts[1:3] {
			if p = strings.ToUpper(strings.TrimSpace(p)); shapeOK(p) {
				e.ISINs = append(e.ISINs, p)
			}
		}
		if strings.Contains(strings.ToLower(e.Name), "direct") {
			e.Plan = "DIRECT"
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, errors.New("no schemes found: not an AMFI NAVAll file")
	}
	return out, nil
}

// ---- card rules ----

type CardInput struct {
	Slug          string     `json:"slug"`
	Issuer        string     `json:"issuer"`
	EffectiveFrom string     `json:"effectiveFrom"` // YYYY-MM-DD; may be in the future for announced devaluations
	ChangeNote    string     `json:"changeNote"`
	Card          cards.Card `json:"card"`
}

var slugShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,60}$`)

func ParseCardsJSON(r io.Reader) ([]CardInput, error) {
	var in []CardInput
	dec := json.NewDecoder(io.LimitReader(r, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("invalid cards JSON: %w", err)
	}
	seen := map[string]bool{}
	for i := range in {
		c := &in[i]
		if err := c.validate(); err != nil {
			return nil, fmt.Errorf("card %q: %w", c.Slug, err)
		}
		if seen[c.Slug] {
			return nil, fmt.Errorf("duplicate slug %q", c.Slug)
		}
		seen[c.Slug] = true
		c.Card.ID = c.Slug
		if c.Card.Rules == nil {
			c.Card.Rules = map[cards.Category]cards.CategoryRule{}
		}
		if c.Card.GroupCapPoints == nil {
			c.Card.GroupCapPoints = map[string]int64{}
		}
	}
	return in, nil
}

func (c CardInput) validate() error {
	k := c.Card
	switch {
	case !slugShape.MatchString(c.Slug):
		return errors.New("slug must be lowercase letters, digits and dashes")
	case c.Issuer == "" || k.Name == "":
		return errors.New("issuer and card.name are required")
	case k.AnnualFeePaise < 0 || k.FeeWaiverSpendPaise < 0:
		return errors.New("fees must be >= 0")
	case k.BaseEarnBps < 0 || k.BaseEarnBps > 3000:
		return errors.New("baseEarnBps must be 0-3000")
	case k.RedeemPaisePerPoint <= 0 || k.RedeemPaisePerPoint > 500:
		return errors.New("redeemPaisePerPoint must be 1-500")
	}
	if _, err := time.Parse("2006-01-02", c.EffectiveFrom); err != nil {
		return errors.New("effectiveFrom must be YYYY-MM-DD")
	}
	for cat, r := range k.Rules {
		switch {
		case !cat.Valid():
			return fmt.Errorf("unknown category %q", cat)
		case r.EarnBps < 0 || r.EarnBps > 3000:
			return fmt.Errorf("%s: earnBps must be 0-3000", cat)
		case r.CapGroup != "" && k.GroupCapPoints[r.CapGroup] <= 0:
			return fmt.Errorf("%s: cap group %q has no positive groupCapPoints entry", cat, r.CapGroup)
		}
	}
	return nil
}
