package api

import (
	"errors"
	"regexp"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"

	"leakproof/internal/reports"
)

var slugRe = regexp.MustCompile(`^[a-z2-7]{10}$`)

func (s *Server) mountReports(app *fiber.App) {
	// Saving is the only write path in the whole API, so it gets a per-IP rate limit.
	app.Post("/api/v1/reports", limiter.New(limiter.Config{
		Max:        20,
		Expiration: time.Minute,
		LimitReached: func(c *fiber.Ctx) error {
			return fiber.NewError(fiber.StatusTooManyRequests, "too many saves, try again in a minute")
		},
	}), s.saveReport)
	app.Get("/api/v1/reports/:slug", s.getReport)
}

type saveResp struct {
	Slug      string    `json:"slug"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// saveReport stores ONLY the numeric score. The ruleset labels are set by the server, never the client.
func (s *Server) saveReport(c *fiber.Ctx) error {
	var r reports.Report
	if err := c.BodyParser(&r); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	now := s.Now()
	r.MFRulesetMonth = now.Format("2006-01")
	r.CardRulesetVersion = "catalog-" + now.Format("2006-01")
	r.ExpiresAt = time.Time{}

	slug, exp, err := s.Reports.Save(c.UserContext(), r)
	switch {
	case errors.Is(err, reports.ErrInvalid):
		return fiber.NewError(fiber.StatusBadRequest, "invalid report values")
	case err != nil:
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(saveResp{slug, exp})
}

func (s *Server) getReport(c *fiber.Ctx) error {
	slug := c.Params("slug")
	if !slugRe.MatchString(slug) {
		return fiber.NewError(fiber.StatusNotFound, "report not found")
	}
	r, err := s.Reports.Get(c.UserContext(), slug)
	switch {
	case errors.Is(err, reports.ErrNotFound):
		return fiber.NewError(fiber.StatusNotFound, "report not found")
	case err != nil:
		return err
	}
	return c.JSON(r)
}
