// Package mf implements mutual-fund overlap detection and Indian tax-aware consolidation verdicts.
// All money is int64 paise, all rates are int64 basis points. shopspring/decimal is used for the
// percentage math so rounding is explicit (half away from zero to whole paise).
package mf

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

type Verdict string

const (
	ConsolidateNow Verdict = "CONSOLIDATE_NOW"
	HoldUntilLTCG  Verdict = "HOLD_UNTIL_LTCG_DATE"
	Keep           Verdict = "KEEP"
)

// FlagThresholdBps: pairs with MORE than 30% overlap are flagged.
const FlagThresholdBps = 3000

type PlanType string

const (
	PlanDirect  PlanType = "DIRECT"
	PlanRegular PlanType = "REGULAR"
)

type Fund struct {
	ISIN           string            `json:"isin"`
	Name           string            `json:"name"`
	Plan           PlanType          `json:"plan"`
	TERBps         int64             `json:"terBps"`
	DirectTERBps   int64             `json:"directTerBps"`
	ExitLoadBps    int64             `json:"exitLoadBps"`
	ExitLoadDays   int               `json:"exitLoadDays"`
	EquityOriented bool              `json:"equityOriented"`
	// GrandfatherNAV is the fund NAV on 31-Jan-2018 (zero = unknown), used to step up the cost of pre-Feb-2018 units.
	GrandfatherNAV decimal.Decimal `json:"-"`
	Holdings       map[string]int64  `json:"-"` // stock ISIN -> weight bps
	HoldingNames   map[string]string `json:"-"`
}

type Lot struct {
	PurchaseDate time.Time       `json:"purchaseDate"`
	Units        decimal.Decimal `json:"units"`
	CostPaise    int64           `json:"costPaise"`
	ValuePaise   int64           `json:"valuePaise"`
}

type Position struct {
	FundISIN string `json:"fundIsin"`
	FolioRef string `json:"folioRef"` // salted hash, never the real folio
	Lots     []Lot  `json:"lots"`
}

func (p Position) ValuePaise() (v int64) {
	for _, l := range p.Lots {
		v += l.ValuePaise
	}
	return
}

// StatementParser turns a CAS PDF into REDACTED positions. Implementations own decryption; the caller
// zeroes the password (see Zero) after Parse returns. Guest mode must never persist pdf or password.
type StatementParser interface {
	Parse(ctx context.Context, pdf []byte, password []byte) (*ParsedStatement, error)
}

type ParsedStatement struct {
	Source    string     `json:"source"` // "CAMS" | "KFINTECH"
	Positions []Position `json:"positions"`
}
