// Package reports stores the anonymised "share my score" aggregates. It holds numbers only: no names,
// PAN, folios, fund or card identities. Guest mode never calls this unless the user taps Save.
package reports

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const TTL = 90 * 24 * time.Hour

type Report struct {
	RewardYieldBps     int64     `json:"rewardYieldBps"`
	OverlapBps         int64     `json:"overlapBps"`
	AnnualLeakPaise    int64     `json:"annualLeakPaise"`
	CardLeakPaise      int64     `json:"cardLeakPaise"`
	MFLeakPaise        int64     `json:"mfLeakPaise"`
	MFRulesetMonth     string    `json:"mfRulesetMonth"`     // "2025-03"
	CardRulesetVersion string    `json:"cardRulesetVersion"` // e.g. "demo-1"
	ExpiresAt          time.Time `json:"expiresAt"`
}

var (
	ErrInvalid  = errors.New("invalid report values")
	ErrNotFound = errors.New("report not found")
)

const maxPaise = 1_000_000_000_000_00 // Rs 1 trillion: anything above is garbage input

// Validate rejects out-of-range numbers so the table can't be filled with junk or absurd values.
func (r Report) Validate() error {
	inPaise := func(v int64) bool { return v >= 0 && v <= maxPaise }
	switch {
	case r.RewardYieldBps < -10_000 || r.RewardYieldBps > 10_000:
	case r.OverlapBps < 0 || r.OverlapBps > 10_000:
	case !inPaise(r.AnnualLeakPaise), !inPaise(r.CardLeakPaise), !inPaise(r.MFLeakPaise):
	case r.AnnualLeakPaise != r.CardLeakPaise+r.MFLeakPaise:
	default:
		return nil
	}
	return ErrInvalid
}

type Store interface {
	Save(ctx context.Context, r Report) (slug string, expires time.Time, err error)
	Get(ctx context.Context, slug string) (Report, error)
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSlug returns a 10-char unguessable lowercase slug (50 bits of randomness).
func NewSlug() string {
	b := make([]byte, 7)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return strings.ToLower(b32.EncodeToString(b))[:10]
}

// ---- in-memory store (used when DATABASE_URL is not set) ----

type MemStore struct {
	mu  sync.Mutex
	m   map[string]Report
	Now func() time.Time
}

func NewMemStore() *MemStore { return &MemStore{m: map[string]Report{}, Now: time.Now} }

func (s *MemStore) Save(_ context.Context, r Report) (string, time.Time, error) {
	if err := r.Validate(); err != nil {
		return "", time.Time{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r.ExpiresAt = s.Now().Add(TTL)
	slug := NewSlug()
	s.m[slug] = r
	return slug, r.ExpiresAt, nil
}

func (s *MemStore) Get(_ context.Context, slug string) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.m[slug]
	if !ok || !r.ExpiresAt.After(s.Now()) {
		return Report{}, ErrNotFound
	}
	return r, nil
}

// ---- PostgreSQL store ----

type PgStore struct{ Pool *pgxpool.Pool }

func (s PgStore) Save(ctx context.Context, r Report) (string, time.Time, error) {
	if err := r.Validate(); err != nil {
		return "", time.Time{}, err
	}
	month, err := time.Parse("2006-01", r.MFRulesetMonth)
	if err != nil {
		return "", time.Time{}, ErrInvalid
	}
	slug := NewSlug()
	var exp time.Time
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO user_reports (reward_yield_bps, overlap_bps, annual_leak_paise, card_leak_paise, mf_leak_paise,
		                          mf_ruleset_month, card_ruleset_version, share_slug, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now() + make_interval(secs => $9))
		RETURNING expires_at`,
		r.RewardYieldBps, r.OverlapBps, r.AnnualLeakPaise, r.CardLeakPaise, r.MFLeakPaise,
		month, r.CardRulesetVersion, slug, TTL.Seconds()).Scan(&exp)
	return slug, exp, err
}

func (s PgStore) Get(ctx context.Context, slug string) (Report, error) {
	var r Report
	var month time.Time
	err := s.Pool.QueryRow(ctx, `
		SELECT reward_yield_bps, overlap_bps, annual_leak_paise, card_leak_paise, mf_leak_paise,
		       mf_ruleset_month, card_ruleset_version, expires_at
		FROM user_reports WHERE share_slug = $1 AND expires_at > now()`, slug).
		Scan(&r.RewardYieldBps, &r.OverlapBps, &r.AnnualLeakPaise, &r.CardLeakPaise, &r.MFLeakPaise,
			&month, &r.CardRulesetVersion, &r.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Report{}, ErrNotFound
	}
	if err != nil {
		return Report{}, err
	}
	r.MFRulesetMonth = month.Format("2006-01")
	return r, nil
}

// Reap deletes expired reports and returns how many were removed. Run daily by cmd/ingest.
func Reap(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM user_reports WHERE expires_at < now()`)
	return tag.RowsAffected(), err
}
