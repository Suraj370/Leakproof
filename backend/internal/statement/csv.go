// Package statement parses bank / credit-card CSV exports into a monthly spend profile by category.
// Banks all use different headers, so columns are found by name, and the header row is searched for
// because exports often start with account-info preamble lines.
package statement

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"leakproof/internal/cards"
)

type Item struct {
	Description string         `json:"description"`
	AmountPaise int64          `json:"amountPaise"`
	Category    cards.Category `json:"category"`
}

type Summary struct {
	MonthlySpendPaise cards.Spend `json:"monthlySpendPaise"`
	Months            int         `json:"months"`
	Transactions      int         `json:"transactions"`
	LowConfidence     []Item      `json:"lowConfidence"` // top 20 by amount that the classifier was unsure about
}

var ErrNoHeader = errors.New("could not find a header row with date and description columns")

var dateLayouts = []string{
	"02/01/2006", "02-01-2006", "2006-01-02", "02 Jan 2006", "02-Jan-2006", "02/01/06", "02-01-06", "2 Jan 2006", "02 Jan 06",
}

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseMoney(s string) (int64, bool) {
	s = strings.TrimSpace(strings.NewReplacer(",", "", "₹", "", "INR", "", "Rs.", "", "Rs", "").Replace(s))
	if s == "" || s == "-" {
		return 0, false
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return 0, false
	}
	return d.Mul(decimal.NewFromInt(100)).Round(0).IntPart(), true
}

type cols struct{ date, desc, debit, amount, kind int }

func find(header []string, names ...string) int {
	for i, h := range header {
		h = strings.ToLower(strings.TrimSpace(h))
		for _, n := range names {
			if h == n || strings.Contains(h, n) {
				return i
			}
		}
	}
	return -1
}

func locate(header []string) (cols, bool) {
	c := cols{
		date:   find(header, "txn date", "transaction date", "value date", "posting date", "date"),
		desc:   find(header, "description", "narration", "particulars", "remarks", "details", "merchant"),
		debit:  find(header, "debit", "withdrawal", "dr amount"),
		amount: find(header, "amount"),
		kind:   find(header, "dr/cr", "cr/dr", "type"),
	}
	return c, c.date >= 0 && c.desc >= 0 && (c.debit >= 0 || c.amount >= 0)
}

func cell(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return row[i]
}

// ParseCSV sums debits per category and averages them over the number of distinct calendar months seen.
func ParseCSV(r io.Reader) (*Summary, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true

	var c cols
	found := false
	total := map[cards.Category]int64{}
	months := map[string]bool{}
	var low []Item
	n := 0

	for rowNum := 0; ; rowNum++ {
		row, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			continue // a malformed line (e.g. footer) must not sink the whole file
		}
		if !found {
			if rowNum > 40 {
				return nil, ErrNoHeader
			}
			c, found = locate(row)
			continue
		}
		date, ok := parseDate(cell(row, c.date))
		if !ok {
			continue // footer, blank or wrapped line
		}
		var amt int64
		switch {
		case c.debit >= 0:
			amt, _ = parseMoney(cell(row, c.debit))
		default: // single signed amount column, optionally with a Dr/Cr marker
			v, ok := parseMoney(cell(row, c.amount))
			if !ok {
				continue
			}
			k := strings.ToLower(strings.TrimSpace(cell(row, c.kind)))
			switch {
			case v < 0:
				amt = -v
			case k == "dr" || k == "debit" || k == "d":
				amt = v
			}
		}
		if amt <= 0 {
			continue // credits, refunds and zero rows are not spend
		}
		desc := strings.TrimSpace(cell(row, c.desc))
		cl := cards.Classify(desc)
		total[cl.Category] += amt
		months[date.Format("2006-01")] = true
		n++
		if !cl.Confident {
			low = append(low, Item{desc, amt, cl.Category})
		}
	}
	if !found {
		return nil, ErrNoHeader
	}
	if n == 0 {
		return nil, fmt.Errorf("no spend transactions found")
	}

	m := int64(len(months))
	spend := cards.Spend{}
	for cat, v := range total {
		spend[cat] = (v + m/2) / m // rounded monthly average
	}
	sort.Slice(low, func(i, j int) bool { return low[i].AmountPaise > low[j].AmountPaise })
	if len(low) > 20 {
		low = low[:20]
	}
	return &Summary{MonthlySpendPaise: spend, Months: int(m), Transactions: n, LowConfidence: nonNil(low)}, nil
}

func nonNil(x []Item) []Item {
	if x == nil {
		return []Item{}
	}
	return x
}
