package catalog

import (
	"context"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
)

func TestMarshalRoundTripKeepsHoldings(t *testing.T) {
	seed := Seed()
	b, err := Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, seed) {
		t.Fatal("round trip changed the catalog")
	}
	if len(got.Funds["INFDEMO00001"].Holdings) != 5 {
		t.Fatal("holdings lost")
	}
}

func TestCachedReadThroughAndInvalidate(t *testing.T) {
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	loads := 0
	c := Cached{Redis: rc, Key: "catalog:v1", TTL: time.Minute, Load: func(context.Context) (*Catalog, error) {
		loads++
		return Seed(), nil
	}}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := c.Get(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if loads != 1 {
		t.Fatalf("loader ran %d times, want 1 (cache hit expected)", loads)
	}
	if err := c.Invalidate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx); err != nil || loads != 2 {
		t.Fatalf("loads=%d err=%v after invalidate", loads, err)
	}
}

func TestCachedSurvivesRedisOutage(t *testing.T) {
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	mr.Close() // Redis goes away
	c := Cached{Redis: rc, Key: "k", TTL: time.Minute, Load: func(context.Context) (*Catalog, error) { return Seed(), nil }}
	cat, err := c.Get(context.Background())
	if err != nil || len(cat.Funds) != 3 {
		t.Fatalf("%v %+v", err, cat)
	}
}

func TestCachedWithoutRedis(t *testing.T) {
	c := Cached{Load: func(context.Context) (*Catalog, error) { return Seed(), nil }}
	if _, err := c.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Invalidate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Integration: needs a PostgreSQL with migrations applied. Run via `docker compose --profile itest ...`.
func TestIntegrationLoadMatchesSeed(t *testing.T) {
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

	got, err := Load(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	want := Seed()

	if len(got.Funds) != len(want.Funds) {
		t.Fatalf("funds %d want %d", len(got.Funds), len(want.Funds))
	}
	for isin, w := range want.Funds {
		g, ok := got.Funds[isin]
		if !ok {
			t.Fatalf("missing fund %s", isin)
		}
		if g.Name != w.Name || g.Plan != w.Plan || g.TERBps != w.TERBps || g.DirectTERBps != w.DirectTERBps ||
			g.ExitLoadBps != w.ExitLoadBps || g.ExitLoadDays != w.ExitLoadDays || g.EquityOriented != w.EquityOriented {
			t.Errorf("%s fund fields differ:\n got %+v\nwant %+v", isin, g, w)
		}
		if !reflect.DeepEqual(g.Holdings, w.Holdings) {
			t.Errorf("%s holdings differ: %v vs %v", isin, g.Holdings, w.Holdings)
		}
		for stock := range w.Holdings {
			if g.HoldingNames[stock] != w.HoldingNames[stock] {
				t.Errorf("%s name for %s: %q vs %q", isin, stock, g.HoldingNames[stock], w.HoldingNames[stock])
			}
		}
	}

	sort.Slice(got.Cards, func(i, j int) bool { return got.Cards[i].ID < got.Cards[j].ID })
	sort.Slice(want.Cards, func(i, j int) bool { return want.Cards[i].ID < want.Cards[j].ID })
	if len(got.Cards) != len(want.Cards) {
		t.Fatalf("cards %d want %d", len(got.Cards), len(want.Cards))
	}
	for i := range want.Cards {
		g, w := got.Cards[i], want.Cards[i]
		if g.ID != w.ID || g.Name != w.Name || g.AnnualFeePaise != w.AnnualFeePaise || g.FeeWaiverSpendPaise != w.FeeWaiverSpendPaise ||
			g.BaseEarnBps != w.BaseEarnBps || g.RedeemPaisePerPoint != w.RedeemPaisePerPoint {
			t.Errorf("card %s fields differ:\n got %+v\nwant %+v", w.ID, g, w)
		}
		if !reflect.DeepEqual(g.Milestones, w.Milestones) {
			t.Errorf("card %s milestones differ: %+v vs %+v", w.ID, g.Milestones, w.Milestones)
		}
		if !reflect.DeepEqual(g.Rules, w.Rules) || !reflect.DeepEqual(g.GroupCapPoints, w.GroupCapPoints) {
			t.Errorf("card %s rules/caps differ:\n got %+v %v\nwant %+v %v", w.ID, g.Rules, g.GroupCapPoints, w.Rules, w.GroupCapPoints)
		}
	}
}

func TestCacheRoundTripKeepsGrandfatherNAV(t *testing.T) {
	seed := Seed()
	f := seed.Funds["INFDEMO00001"]
	f.GrandfatherNAV = decimal.RequireFromString("123.4567")
	seed.Funds["INFDEMO00001"] = f
	b, err := Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Funds["INFDEMO00001"].GrandfatherNAV.Equal(f.GrandfatherNAV) || !got.Funds["INFDEMO00002"].GrandfatherNAV.IsZero() {
		t.Fatalf("NAV not preserved: %v", got.Funds["INFDEMO00001"].GrandfatherNAV)
	}
}
