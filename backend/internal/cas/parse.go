// Package cas parses the TEXT of a CAMS / KFintech consolidated account statement into redacted
// positions (ISIN, folio hash, FIFO lots). PDF decryption and text extraction live in pdf.go.
//
// IMPORTANT: the layout handled here is modelled on the common "detailed" CAS format
// (Folio No / scheme line with ISIN / dated transaction rows / Closing Unit Balance line). It has been
// tested against synthetic fixtures only. Validate against real statements before trusting it.
package cas

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"leakproof/internal/mf"
)

type Result struct {
	Source    string        `json:"source"` // "CAMS" | "KFINTECH" | "UNKNOWN"
	Positions []mf.Position `json:"positions"`
	Warnings  []string      `json:"warnings"` // never contains PII: scheme ISINs and counts only
}

var (
	reFolio   = regexp.MustCompile(`(?i)Folio\s*No\.?\s*:\s*([0-9A-Za-z][0-9A-Za-z /]*?)(?:\s{2,}|\s+PAN\b|\s+KYC\b|$)`)
	reISIN    = regexp.MustCompile(`ISIN:\s*(INF[0-9A-Z]{9})`)
	reNum     = `\(?-?[\d,]+(?:\.\d+)?\)?`
	reTxn     = regexp.MustCompile(`^(\d{2}-[A-Za-z]{3}-\d{4})\s+(.*?)\s*(` + reNum + `)\s+(` + reNum + `)\s+(` + reNum + `)\s+(` + reNum + `)$`)
	reOpening = regexp.MustCompile(`(?i)Opening Unit Balance:\s*([\d,]+(?:\.\d+)?)`)
	reClosing = regexp.MustCompile(`(?i)Closing Unit Balance:\s*([\d,]+(?:\.\d+)?)`)
	reNAV     = regexp.MustCompile(`(?i)NAV on (\d{2}-[A-Za-z]{3}-\d{4}):\s*INR\s*([\d,]+(?:\.\d+)?)`)
	reSpaces  = regexp.MustCompile(`[ \t]+`)
)

func parseNum(s string) (decimal.Decimal, error) {
	neg := strings.HasPrefix(s, "(") || strings.HasPrefix(s, "-")
	s = strings.Trim(s, "()-")
	d, err := decimal.NewFromString(strings.ReplaceAll(s, ",", ""))
	if neg {
		d = d.Neg()
	}
	return d, err
}

func rupeesToPaise(d decimal.Decimal) int64 { return d.Mul(decimal.NewFromInt(100)).Round(0).IntPart() }

type lot struct {
	date  time.Time
	units decimal.Decimal
	cost  decimal.Decimal // rupees, for the units still held
}

type scheme struct {
	isin       string
	folio      string
	lots       []lot
	opening    decimal.Decimal
	closing    *decimal.Decimal
	navDate    time.Time
	nav        decimal.Decimal
	sawNAV     bool
	txnCount   int
	hasHistory bool
}

// Parse converts CAS text into positions. salt must be a per-request random salt (mf.NewSalt()).
func Parse(text string, salt []byte) (*Result, error) {
	res := &Result{Source: detectSource(text), Positions: []mf.Position{}, Warnings: []string{}}
	var folio string
	var cur *scheme
	var done []*scheme

	flush := func() {
		if cur != nil {
			done = append(done, cur)
			cur = nil
		}
	}

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(reSpaces.ReplaceAllString(raw, " "))
		if line == "" {
			continue
		}
		if m := reFolio.FindStringSubmatch(raw); m != nil {
			flush()
			folio = strings.TrimSpace(m[1])
			continue
		}
		if m := reISIN.FindStringSubmatch(line); m != nil && !reTxn.MatchString(line) {
			flush()
			cur = &scheme{isin: m[1], folio: folio}
			continue
		}
		if cur == nil {
			continue
		}
		if m := reOpening.FindStringSubmatch(line); m != nil {
			d, err := parseNum(m[1])
			if err != nil {
				return nil, fmt.Errorf("bad opening balance for %s", cur.isin)
			}
			cur.opening = d
			continue
		}
		if m := reTxn.FindStringSubmatch(line); m != nil {
			if err := cur.addTxn(m); err != nil {
				return nil, fmt.Errorf("scheme %s: %w", cur.isin, err)
			}
			continue
		}
		if m := reClosing.FindStringSubmatch(line); m != nil {
			d, err := parseNum(m[1])
			if err != nil {
				return nil, fmt.Errorf("bad closing balance for %s", cur.isin)
			}
			cur.closing = &d
		}
		if m := reNAV.FindStringSubmatch(line); m != nil {
			t, err := time.Parse("02-Jan-2006", m[1])
			n, err2 := parseNum(m[2])
			if err != nil || err2 != nil {
				return nil, fmt.Errorf("bad NAV line for %s", cur.isin)
			}
			cur.navDate, cur.nav, cur.sawNAV = t, n, true
		}
	}
	flush()

	for _, s := range done {
		pos, warn, err := s.toPosition(salt)
		if err != nil {
			return nil, err
		}
		res.Warnings = append(res.Warnings, warn...)
		if pos != nil {
			res.Positions = append(res.Positions, *pos)
		}
	}
	if len(done) == 0 {
		return nil, fmt.Errorf("no schemes found: unrecognised statement layout")
	}
	return res, nil
}

func detectSource(text string) string {
	up := strings.ToUpper(text)
	switch {
	case strings.Contains(up, "KFINTECH") || strings.Contains(up, "KFIN TECHNOLOGIES"):
		return "KFINTECH"
	case strings.Contains(up, "CAMS") || strings.Contains(up, "COMPUTER AGE MANAGEMENT"):
		return "CAMS"
	}
	return "UNKNOWN"
}

// addTxn applies one dated row: columns are amount, units, price(NAV), unit balance.
// Positive units add a FIFO lot; negative units (redemption / switch-out) consume the oldest lots.
func (s *scheme) addTxn(m []string) error {
	date, err := time.Parse("02-Jan-2006", m[1])
	if err != nil {
		return fmt.Errorf("bad date %q", m[1])
	}
	amount, e1 := parseNum(m[3])
	units, e2 := parseNum(m[4])
	if e1 != nil || e2 != nil {
		return fmt.Errorf("bad amount/units on %s", m[1])
	}
	s.txnCount++
	s.hasHistory = true
	switch {
	case units.IsPositive():
		s.lots = append(s.lots, lot{date: date, units: units, cost: amount.Abs()})
	case units.IsNegative():
		return s.consume(units.Abs())
	}
	return nil
}

func (s *scheme) consume(units decimal.Decimal) error {
	for units.IsPositive() {
		if len(s.lots) == 0 {
			// Redeeming units bought before the statement window: nothing to match, ignore the rest.
			return nil
		}
		l := &s.lots[0]
		if l.units.LessThanOrEqual(units) {
			units = units.Sub(l.units)
			s.lots = s.lots[1:]
			continue
		}
		keep := l.units.Sub(units)
		l.cost = l.cost.Mul(keep).Div(l.units)
		l.units = keep
		return nil
	}
	return nil
}

func (s *scheme) toPosition(salt []byte) (*mf.Position, []string, error) {
	var warn []string
	if s.closing == nil || !s.sawNAV {
		return nil, nil, fmt.Errorf("scheme %s: missing closing balance or NAV line", s.isin)
	}
	if s.closing.IsZero() {
		return nil, nil, nil // fully redeemed: nothing held, nothing to analyse
	}

	held := decimal.Zero
	for _, l := range s.lots {
		held = held.Add(l.units)
	}
	// Units the statement window doesn't explain (bought before it began). Their purchase date is
	// unknown, so date them at the valuation date: treated as short-term, which can only overstate tax.
	if gap := s.closing.Sub(held); gap.GreaterThan(decimal.RequireFromString("0.001")) {
		warn = append(warn, fmt.Sprintf("%s: %s units have no purchase history in this statement; "+
			"treated as recent purchases (tax may be overstated)", s.isin, gap.StringFixed(3)))
		s.lots = append([]lot{{date: s.navDate, units: gap, cost: gap.Mul(s.nav)}}, s.lots...)
	} else if gap.LessThan(decimal.RequireFromString("-0.001")) {
		return nil, nil, fmt.Errorf("scheme %s: transactions exceed closing balance; statement layout not understood", s.isin)
	}

	pos := &mf.Position{FundISIN: s.isin, FolioRef: mf.RedactFolio(s.folio, salt)}
	for _, l := range s.lots {
		pos.Lots = append(pos.Lots, mf.Lot{
			PurchaseDate: l.date,
			Units:        l.units,
			CostPaise:    rupeesToPaise(l.cost),
			ValuePaise:   rupeesToPaise(l.units.Mul(s.nav)),
		})
	}
	return pos, warn, nil
}
