// Package api is the HTTP layer. Guest mode is the only mode: request bodies are parsed in memory,
// analysed, answered, and dropped. Nothing here writes user data to disk, Redis or PostgreSQL.
package api

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"

	"leakproof/internal/cards"
	"leakproof/internal/mf"
)

type Server struct {
	Cat     *Catalog
	Now     func() time.Time
	Workers int
}

func NewApp(s *Server) *fiber.App {
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.Workers <= 0 {
		s.Workers = 4
	}
	app := fiber.New(fiber.Config{
		BodyLimit:             10 << 20,
		DisableStartupMessage: true,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			msg := "internal error" // never echo input back: it may contain PII
			var fe *fiber.Error
			if errors.As(err, &fe) {
				code, msg = fe.Code, fe.Message
			}
			return c.Status(code).JSON(fiber.Map{"error": msg})
		},
	})
	app.Use(func(c *fiber.Ctx) error {
		c.Set("Cache-Control", "no-store")
		c.Set("X-Content-Type-Options", "nosniff")
		return c.Next()
	})
	app.Get("/healthz", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })
	app.Get("/api/v1/catalog/cards", s.catalogCards)
	app.Post("/api/v1/analyze/mf", s.analyzeMF)
	app.Post("/api/v1/cards/solve", s.solveCards)
	app.Post("/api/v1/classify", s.classify)
	return app
}

func (s *Server) catalogCards(c *fiber.Ctx) error { return c.JSON(s.Cat.Cards) }

// ---- mutual funds ----

type analyzeReq struct {
	Positions         []mf.Position `json:"positions"`
	SlabBps           int64         `json:"slabBps"`
	RealisedLTCGPaise int64         `json:"realisedLtcgPaise"`
}

type FundSummary struct {
	ISIN       string `json:"isin"`
	Name       string `json:"name"`
	ValuePaise int64  `json:"valuePaise"`
	TERBps     int64  `json:"terBps"`
}

type MFReport struct {
	Funds               []FundSummary   `json:"funds"`
	Pairs               []mf.PairResult `json:"pairs"`
	AnnualLeakPaise     int64           `json:"annualLeakPaise"`
	PortfolioOverlapBps int64           `json:"portfolioOverlapBps"`
}

func (s *Server) analyzeMF(c *fiber.Ctx) error {
	var req analyzeReq
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	if len(req.Positions) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "positions required")
	}
	if req.SlabBps < 0 || req.SlabBps > 4800 || req.RealisedLTCGPaise < 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid slabBps or realisedLtcgPaise")
	}
	if req.SlabBps == 0 {
		req.SlabBps = 3000
	}

	// Merge folios of the same scheme; keep first-seen order so matrix indices are stable.
	var order []string
	merged := map[string]mf.Position{}
	for _, p := range req.Positions {
		if _, ok := s.Cat.Funds[p.FundISIN]; !ok {
			return fiber.NewError(fiber.StatusBadRequest, "unknown fund ISIN: "+p.FundISIN)
		}
		for _, l := range p.Lots {
			if l.CostPaise < 0 || l.ValuePaise < 0 {
				return fiber.NewError(fiber.StatusBadRequest, "negative lot amounts")
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

	funds := make([]mf.Fund, len(order))
	summaries := make([]FundSummary, len(order))
	for i, isin := range order {
		funds[i] = s.Cat.Funds[isin]
		summaries[i] = FundSummary{isin, funds[i].Name, merged[isin].ValuePaise(), funds[i].TERBps}
	}

	cfg := mf.DefaultTaxConfig(req.SlabBps)
	pairs, leak, err := mf.Analyze(c.Context(), funds, merged, cfg, s.Now(), req.RealisedLTCGPaise, s.Workers)
	if err != nil {
		return err
	}
	return c.JSON(MFReport{Funds: summaries, Pairs: pairs, AnnualLeakPaise: leak,
		PortfolioOverlapBps: weightedOverlap(pairs, summaries)})
}

// weightedOverlap is the pair overlap averaged with weight min(valueA, valueB), so a big overlapping
// holding moves the score more than two tiny ones.
func weightedOverlap(pairs []mf.PairResult, f []FundSummary) int64 {
	var num, den int64
	for _, p := range pairs {
		w := min(f[p.A].ValuePaise, f[p.B].ValuePaise)
		num += p.OverlapBps * (w / 100) // /100: keep products inside int64 for very large portfolios
		den += w / 100
	}
	if den == 0 {
		return 0
	}
	return num / den
}

// ---- credit cards ----

type solveReq struct {
	MonthlySpendPaise cards.Spend `json:"monthlySpendPaise"`
	CurrentCardIDs    []string    `json:"currentCardIds"`
}

func (s *Server) solveCards(c *fiber.Ctx) error {
	var req solveReq
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	for k, v := range req.MonthlySpendPaise {
		if !k.Valid() || v < 0 {
			return fiber.NewError(fiber.StatusBadRequest, "invalid spend category or amount")
		}
	}
	byID := map[string]cards.Card{}
	for _, cd := range s.Cat.Cards {
		byID[cd.ID] = cd
	}
	var current []cards.Card
	for _, id := range req.CurrentCardIDs {
		cd, ok := byID[id]
		if !ok {
			return fiber.NewError(fiber.StatusBadRequest, "unknown card id: "+id)
		}
		current = append(current, cd)
	}
	return c.JSON(cards.BestCombo(s.Cat.Cards, current, req.MonthlySpendPaise))
}

// ---- classifier ----

type classifyReq struct {
	Descriptions []string `json:"descriptions"`
}
type classifyItem struct {
	Description string         `json:"description"`
	Category    cards.Category `json:"category"`
	Confident   bool           `json:"confident"`
}

func (s *Server) classify(c *fiber.Ctx) error {
	var req classifyReq
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	if len(req.Descriptions) > 5000 {
		return fiber.NewError(fiber.StatusBadRequest, "too many descriptions")
	}
	out := make([]classifyItem, len(req.Descriptions))
	for i, d := range req.Descriptions {
		cl := cards.Classify(d)
		out[i] = classifyItem{d, cl.Category, cl.Confident}
	}
	return c.JSON(out)
}
