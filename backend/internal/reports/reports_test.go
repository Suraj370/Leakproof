package reports

import (
	"context"
	"errors"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func good() Report {
	return Report{RewardYieldBps: 250, OverlapBps: 3250, AnnualLeakPaise: 1_500_000, CardLeakPaise: 500_000,
		MFLeakPaise: 1_000_000, MFRulesetMonth: "2025-03", CardRulesetVersion: "demo-1"}
}

func TestValidate(t *testing.T) {
	if err := good().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*Report){
		"overlap >100%":    func(r *Report) { r.OverlapBps = 10_001 },
		"negative overlap": func(r *Report) { r.OverlapBps = -1 },
		"yield too big":    func(r *Report) { r.RewardYieldBps = 20_000 },
		"negative leak":    func(r *Report) { r.CardLeakPaise = -5; r.AnnualLeakPaise = r.MFLeakPaise - 5 },
		"absurd amount":    func(r *Report) { r.MFLeakPaise = maxPaise + 1; r.AnnualLeakPaise = r.CardLeakPaise + r.MFLeakPaise },
		"total != parts":   func(r *Report) { r.AnnualLeakPaise++ },
	}
	for name, mutate := range bad {
		r := good()
		mutate(&r)
		if !errors.Is(r.Validate(), ErrInvalid) {
			t.Errorf("%s: expected ErrInvalid", name)
		}
	}
}

func TestSlugShapeAndUniqueness(t *testing.T) {
	re := regexp.MustCompile(`^[a-z2-7]{10}$`)
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		s := NewSlug()
		if !re.MatchString(s) || seen[s] {
			t.Fatalf("bad or duplicate slug %q", s)
		}
		seen[s] = true
	}
}

func TestMemStoreSaveGetAndExpiry(t *testing.T) {
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	s := NewMemStore()
	s.Now = func() time.Time { return now }
	ctx := context.Background()

	slug, exp, err := s.Save(ctx, good())
	if err != nil || !exp.Equal(now.Add(TTL)) {
		t.Fatalf("%v %v", err, exp)
	}
	r, err := s.Get(ctx, slug)
	if err != nil || r.OverlapBps != 3250 {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := s.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	s.Now = func() time.Time { return now.Add(TTL + time.Second) }
	if _, err := s.Get(ctx, slug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired report still served: %v", err)
	}
	if _, _, err := s.Save(ctx, Report{OverlapBps: -1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
}

func TestIntegrationPgStore(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	st := PgStore{Pool: pool}

	slug, exp, err := st.Save(ctx, good())
	if err != nil || exp.Before(time.Now().Add(80*24*time.Hour)) {
		t.Fatalf("%v %v", err, exp)
	}
	r, err := st.Get(ctx, slug)
	if err != nil || r.AnnualLeakPaise != 1_500_000 || r.MFRulesetMonth != "2025-03" || r.CardRulesetVersion != "demo-1" {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := st.Get(ctx, "doesnotexist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}

	// expire it: Get must hide it and Reap must remove it
	if _, err := pool.Exec(ctx, "UPDATE user_reports SET expires_at = now() - interval '1 day' WHERE share_slug = $1", slug); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, slug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired row served: %v", err)
	}
	n, err := Reap(ctx, pool)
	if err != nil || n < 1 {
		t.Fatalf("reap %d %v", n, err)
	}
}
