package api

import (
	"context"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"leakproof/internal/cas"
	"leakproof/internal/mf"
)

// buildReport merges folios of the same scheme (first-seen order keeps matrix indices stable), runs the
// overlap + tax analysis and returns the report. With skipUnknown, schemes missing from the catalog are
// returned in `unmatched` (ISINs only, never PII) instead of failing the request.
func (s *Server) buildReport(ctx context.Context, positions []mf.Position, slabBps, realisedLTCG int64,
	skipUnknown bool) (*MFReport, []string, error) {

	var order, unmatched []string
	merged := map[string]mf.Position{}
	for _, p := range positions {
		if _, ok := s.cat().Funds[p.FundISIN]; !ok {
			if skipUnknown {
				unmatched = appendUnique(unmatched, p.FundISIN)
				continue
			}
			return nil, nil, fiber.NewError(fiber.StatusBadRequest, "unknown fund ISIN: "+p.FundISIN)
		}
		for _, l := range p.Lots {
			if l.CostPaise < 0 || l.ValuePaise < 0 {
				return nil, nil, fiber.NewError(fiber.StatusBadRequest, "negative lot amounts")
			}
		}
		m, seen := merged[p.FundISIN]
		if !seen {
			order = append(order, p.FundISIN)
			m = mf.Position{FundISIN: p.FundISIN}
		}
		m.Lots = append(m.Lots, p.Lots...)
		merged[p.FundISIN] = m
	}
	if len(order) == 0 {
		return nil, unmatched, fiber.NewError(fiber.StatusUnprocessableEntity, "none of the schemes in the statement are in our catalog yet")
	}

	funds := make([]mf.Fund, len(order))
	summaries := make([]FundSummary, len(order))
	for i, isin := range order {
		funds[i] = s.cat().Funds[isin]
		summaries[i] = FundSummary{isin, funds[i].Name, merged[isin].ValuePaise(), funds[i].TERBps}
	}
	pairs, leak, err := mf.Analyze(ctx, funds, merged, mf.DefaultTaxConfig(slabBps), s.Now(), realisedLTCG, s.Workers)
	if err != nil {
		return nil, nil, err
	}
	return &MFReport{Funds: summaries, Pairs: pairs, AnnualLeakPaise: leak,
		PortfolioOverlapBps: weightedOverlap(pairs, summaries)}, unmatched, nil
}

func appendUnique(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

type CASReport struct {
	MFReport
	Source         string   `json:"source"`
	Warnings       []string `json:"warnings"`
	UnmatchedISINs []string `json:"unmatchedIsins"`
}

// analyzeCAS: multipart form with `file` (CAS PDF) and `password`; optional `slabBps`, `realisedLtcgPaise`.
// The PDF and password exist only in this request's memory and are zeroed before returning.
func (s *Server) analyzeCAS(c *fiber.Ctx) error {
	slab, err := optInt(c.FormValue("slabBps"), 3000)
	realised, err2 := optInt(c.FormValue("realisedLtcgPaise"), 0)
	if err != nil || err2 != nil || slab < 0 || slab > 4800 || realised < 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid slabBps or realisedLtcgPaise")
	}

	fh, err := c.FormFile("file")
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "file field required")
	}
	f, err := fh.Open()
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "cannot read upload")
	}
	data, err := io.ReadAll(io.LimitReader(f, 10<<20))
	_ = f.Close()
	defer mf.Zero(data)
	if err != nil || len(data) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "cannot read upload")
	}
	password := []byte(c.FormValue("password"))
	defer mf.Zero(password)

	ctx, cancel := context.WithTimeout(c.UserContext(), 20*time.Second)
	defer cancel()

	res, err := cas.ParsePDF(ctx, data, password)
	switch {
	case err == nil:
	case errors.Is(err, cas.ErrWrongPassword):
		return fiber.NewError(fiber.StatusUnauthorized, "incorrect PDF password")
	case errors.Is(err, cas.ErrUnsupportedEncryption):
		return fiber.NewError(fiber.StatusUnsupportedMediaType, "this PDF's encryption is not supported yet")
	case errors.Is(err, cas.ErrUnrecognised):
		return fiber.NewError(fiber.StatusUnprocessableEntity, "statement layout not recognised")
	case errors.Is(err, context.DeadlineExceeded):
		return fiber.NewError(fiber.StatusGatewayTimeout, "statement took too long to process")
	default:
		return fiber.NewError(fiber.StatusBadRequest, "file is not a readable PDF")
	}

	rep, unmatched, err := s.buildReport(ctx, res.Positions, slab, realised, true)
	if err != nil {
		return err
	}
	return c.JSON(CASReport{MFReport: *rep, Source: res.Source, Warnings: res.Warnings,
		UnmatchedISINs: nonNil(unmatched)})
}

func optInt(s string, def int64) (int64, error) {
	if s == "" {
		return def, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

func nonNil(x []string) []string {
	if x == nil {
		return []string{}
	}
	return x
}
