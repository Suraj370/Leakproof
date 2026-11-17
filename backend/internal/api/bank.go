package api

import (
	"bytes"
	"errors"
	"io"

	"github.com/gofiber/fiber/v2"

	"leakproof/internal/mf"
	"leakproof/internal/statement"
)

// analyzeBank: multipart `file` = bank / credit-card CSV export. Returns the average monthly spend per
// category. The CSV is held in memory only and zeroed before returning; descriptions are returned to
// the caller (so they can fix low-confidence rows) but never logged or stored.
func (s *Server) analyzeBank(c *fiber.Ctx) error {
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

	sum, err := statement.ParseCSV(bytes.NewReader(data))
	switch {
	case err == nil:
		return c.JSON(sum)
	case errors.Is(err, statement.ErrNoHeader):
		return fiber.NewError(fiber.StatusUnprocessableEntity, "could not find date, description and amount columns")
	default:
		return fiber.NewError(fiber.StatusUnprocessableEntity, "no spending transactions found in this file")
	}
}
