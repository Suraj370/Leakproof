package cas

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/ledongthuc/pdf"

	"leakproof/internal/mf"
)

var (
	ErrWrongPassword         = errors.New("incorrect PDF password")
	ErrUnsupportedEncryption = errors.New("unsupported PDF encryption (only RC4 and AES-128 are supported)")
	ErrNotPDF                = errors.New("file is not a readable PDF")
	ErrTooLarge              = errors.New("PDF has too many pages")
)

const maxPages = 300

type extracted struct {
	text string
	err  error
}

// ExtractText decrypts (if needed) and extracts the text of every page, in memory only.
// The PDF library is third-party code parsing untrusted input, so it runs behind a recover() and
// the caller's context deadline. A timed-out extraction goroutine is abandoned, not killed: run the
// API with a request timeout and memory limit.
func ExtractText(ctx context.Context, data []byte, password []byte) (string, error) {
	ch := make(chan extracted, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- extracted{err: ErrNotPDF}
			}
		}()
		t, err := extract(data, password)
		ch <- extracted{t, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		return r.text, r.err
	}
}

func extract(data []byte, password []byte) (string, error) {
	calls := 0
	pw := func() string {
		// Offer the password once, then "" so the library gives up instead of retrying forever.
		calls++
		if calls == 1 {
			return string(password)
		}
		return ""
	}
	r, err := pdf.NewReaderEncrypted(bytes.NewReader(data), int64(len(data)), pw)
	if err != nil {
		return "", classify(err)
	}
	if r.NumPage() > maxPages {
		return "", ErrTooLarge
	}

	var sb strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		sb.WriteString(pageText(r.Page(i)))
	}
	return sb.String(), nil
}

type run struct {
	x float64
	b strings.Builder
}

// pageText rebuilds lines from positioned glyphs. The library reports each glyph with its row Y and the
// X where its text run started, so: bucket by Y, split into runs by X (emission order kept inside a run),
// order rows top-to-bottom and runs left-to-right, and join runs with a space.
func pageText(p pdf.Page) string {
	rows := map[int64][]*run{}
	for _, t := range p.Content().Text {
		y := int64(math.Round(t.Y))
		rs := rows[y]
		if n := len(rs); n == 0 || rs[n-1].x != t.X {
			rs = append(rs, &run{x: t.X})
		}
		rs[len(rs)-1].b.WriteString(t.S)
		rows[y] = rs
	}
	ys := make([]int64, 0, len(rows))
	for y := range rows {
		ys = append(ys, y)
	}
	sort.Slice(ys, func(a, b int) bool { return ys[a] > ys[b] }) // PDF Y grows upward
	var sb strings.Builder
	for _, y := range ys {
		rs := rows[y]
		sort.SliceStable(rs, func(a, b int) bool { return rs[a].x < rs[b].x })
		for i, r := range rs {
			if i > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(r.b.String())
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func classify(err error) error {
	switch {
	case errors.Is(err, pdf.ErrInvalidPassword):
		return ErrWrongPassword
	case strings.Contains(err.Error(), "unsupported PDF: encryption") || strings.Contains(err.Error(), "256-bit encryption key"):
		return ErrUnsupportedEncryption
	}
	return ErrNotPDF
}

// ParsePDF is the full guest-mode pipeline: PDF bytes + password -> redacted positions.
// Nothing is written anywhere; the caller owns (and should mf.Zero) data and password.
func ParsePDF(ctx context.Context, data, password []byte) (*Result, error) {
	text, err := ExtractText(ctx, data, password)
	if err != nil {
		return nil, err
	}
	res, err := Parse(text, mf.NewSalt())
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnrecognised, mf.ScrubPII(err.Error()))
	}
	return res, nil
}

var ErrUnrecognised = errors.New("statement layout not recognised")
