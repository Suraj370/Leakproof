package ingest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"leakproof/internal/catalog"
)

// tempDB creates a throwaway database, applies every migration, and drops it afterwards, so these
// integration tests never interfere with other packages sharing the same server.
func tempDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	name := "ingest_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close()
	})

	files, _ := filepath.Glob("../../migrations/*.sql")
	sort.Strings(files)
	if len(files) < 2 {
		t.Fatalf("migrations not found: %v", files)
	}
	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	return pool
}

func mustFunds(t *testing.T, csv string) []FundRow {
	t.Helper()
	rows, bad, err := ParseFundsCSV(strings.NewReader(csv))
	if err != nil || len(bad) != 0 {
		t.Fatalf("%v %v", err, bad)
	}
	return rows
}

const goodFunds = `isin,amfi_code,name,amc,plan,category,equity_oriented,ter_bps,direct_ter_bps,exit_load_bps,exit_load_days,ter_as_of
INF000TEST01,,Test Large Cap (Regular),Test AMC,REGULAR,Large Cap,true,155,68,100,365,2025-03-31
INF179K01AA1,,Demo Bluechip - Direct,Demo MF,DIRECT,Large Cap,true,68,,0,0,2025-03-31
`

func TestIntegrationFundsUpsertAndUpdate(t *testing.T) {
	pool := tempDB(t)
	ctx := context.Background()
	if err := UpsertFunds(ctx, pool, mustFunds(t, goodFunds)); err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Load(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	f := c.Funds["INF000TEST01"]
	if f.TERBps != 155 || f.DirectTERBps != 68 || f.ExitLoadBps != 100 || f.ExitLoadDays != 365 || string(f.Plan) != "REGULAR" {
		t.Fatalf("%+v", f)
	}

	// TER cut next month: same ISIN updates in place, no duplicate row.
	updated := strings.Replace(goodFunds, "REGULAR,Large Cap,true,155", "REGULAR,Large Cap,true,140", 1)
	if err := UpsertFunds(ctx, pool, mustFunds(t, updated)); err != nil {
		t.Fatal(err)
	}
	c, _ = catalog.Load(ctx, pool)
	if c.Funds["INF000TEST01"].TERBps != 140 || len(c.Funds) != 3+2 { // 3 demo funds from the seed migration + 2 new
		t.Fatalf("ter=%d funds=%d", c.Funds["INF000TEST01"].TERBps, len(c.Funds))
	}
}

func holdings(isinWeights ...any) *FundHoldings {
	fh := &FundHoldings{FundISIN: "INF000TEST01"}
	for i := 0; i < len(isinWeights); i += 2 {
		w := int64(isinWeights[i+1].(int))
		fh.Rows = append(fh.Rows, HoldingRow{isinWeights[i].(string), "n-" + isinWeights[i].(string), w})
		fh.SumBps += w
	}
	return fh
}

func TestIntegrationHoldingsReplaceAndGates(t *testing.T) {
	pool := tempDB(t)
	ctx := context.Background()
	if err := UpsertFunds(ctx, pool, mustFunds(t, goodFunds)); err != nil {
		t.Fatal(err)
	}
	mar, apr := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)

	if err := ReplaceHoldings(ctx, pool, mar, holdings("INE040A01034", 3000, "INE090A01021", 2000, "INE009A01021", 1000), false); err != nil {
		t.Fatal(err)
	}
	// idempotent re-run for the same month
	if err := ReplaceHoldings(ctx, pool, mar, holdings("INE040A01034", 3000, "INE090A01021", 2000, "INE009A01021", 1000), false); err != nil {
		t.Fatal(err)
	}

	// April book is almost entirely different: blocked without --force
	wild := holdings("INE467B01029", 4000, "INE002A01018", 2000)
	if err := ReplaceHoldings(ctx, pool, apr, wild, false); !errors.Is(err, ErrBigChange) {
		t.Fatalf("expected ErrBigChange, got %v", err)
	}
	c, _ := catalog.Load(ctx, pool)
	if c.Funds["INF000TEST01"].Holdings["INE040A01034"] != 3000 {
		t.Fatal("rejected month must not replace the catalog's holdings")
	}

	if err := ReplaceHoldings(ctx, pool, apr, wild, true); err != nil {
		t.Fatalf("force should allow: %v", err)
	}
	c, _ = catalog.Load(ctx, pool)
	h := c.Funds["INF000TEST01"].Holdings
	if len(h) != 2 || h["INE467B01029"] != 4000 { // loader serves the LATEST month only
		t.Fatalf("%v", h)
	}

	unknown := holdings("INE040A01034", 5000)
	unknown.FundISIN = "INF000NOPE001"
	if err := ReplaceHoldings(ctx, pool, mar, unknown, false); !errors.Is(err, ErrUnknownFund) {
		t.Fatalf("%v", err)
	}
}

func TestIntegrationCardVersioning(t *testing.T) {
	pool := tempDB(t)
	ctx := context.Background()
	in, err := ParseCardsJSON(strings.NewReader(cardsJSON))
	if err != nil {
		t.Fatal(err)
	}

	res, err := ApplyCards(ctx, pool, in)
	if err != nil || len(res.Created) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	res, err = ApplyCards(ctx, pool, in)
	if err != nil || len(res.Unchanged) != 1 || len(res.Created)+len(res.Updated) != 0 {
		t.Fatalf("re-apply must be a no-op: %v %+v", err, res)
	}

	// Devaluation: cap 2000 -> 1000 effective 2025-05-01 closes v1 and opens v2.
	cut := strings.Replace(cardsJSON, `"online": 2000`, `"online": 1000`, 1)
	cut = strings.Replace(cut, `2025-04-01`, `2025-05-01`, 1)
	in2, _ := ParseCardsJSON(strings.NewReader(cut))
	res, err = ApplyCards(ctx, pool, in2)
	if err != nil || len(res.Updated) != 1 {
		t.Fatalf("%v %+v", err, res)
	}

	var versions int
	var closed time.Time
	if err := pool.QueryRow(ctx, `SELECT count(*), max(effective_to) FROM credit_cards WHERE slug='test-cashback'`).Scan(&versions, &closed); err != nil {
		t.Fatal(err)
	}
	if versions != 2 || closed.Format("2006-01-02") != "2025-05-01" {
		t.Fatalf("versions=%d closed=%v", versions, closed)
	}
	c, _ := catalog.Load(ctx, pool)
	var found bool
	for _, cd := range c.Cards {
		if cd.ID == "test-cashback" {
			found = true
			if cd.GroupCapPoints["online"] != 1000 {
				t.Fatalf("loader served the old version: %+v", cd.GroupCapPoints)
			}
		}
	}
	if !found {
		t.Fatal("card missing from catalog")
	}

	// A future-dated change must NOT take effect yet.
	future := strings.Replace(cut, `"online": 1000`, `"online": 500`, 1)
	future = strings.Replace(future, `2025-05-01`, `2999-01-01`, 1)
	in3, _ := ParseCardsJSON(strings.NewReader(future))
	if _, err := ApplyCards(ctx, pool, in3); err != nil {
		t.Fatal(err)
	}
	c, _ = catalog.Load(ctx, pool)
	for _, cd := range c.Cards {
		if cd.ID == "test-cashback" && cd.GroupCapPoints["online"] != 1000 {
			t.Fatalf("future-dated version already live: %+v", cd.GroupCapPoints)
		}
	}

	// Backdated change is refused (effectiveFrom not after the current version's).
	back := strings.Replace(future, `"online": 500`, `"online": 300`, 1)
	back = strings.Replace(back, `2999-01-01`, `2025-01-01`, 1)
	in4, _ := ParseCardsJSON(strings.NewReader(back))
	if _, err := ApplyCards(ctx, pool, in4); err == nil {
		t.Fatal("backdated version accepted")
	}
}

func TestIntegrationAMFIEnrich(t *testing.T) {
	pool := tempDB(t)
	ctx := context.Background()
	if err := UpsertFunds(ctx, pool, mustFunds(t, goodFunds)); err != nil {
		t.Fatal(err)
	}
	entries, err := ParseAMFI(strings.NewReader(amfiTxt))
	if err != nil {
		t.Fatal(err)
	}
	matched, unmatched, err := EnrichFromAMFI(ctx, pool, entries)
	if err != nil || matched != 1 || unmatched != 1 { // Bluechip direct matches; the regular IDCW scheme's ISINs are not ours
		t.Fatalf("%v matched=%d unmatched=%d", err, matched, unmatched)
	}
	var code int
	if err := pool.QueryRow(ctx, `SELECT amfi_code FROM mutual_funds WHERE isin='INF179K01AA1'`).Scan(&code); err != nil || code != 119551 {
		t.Fatalf("%v code=%d", err, code)
	}
	// second run is a no-op
	matched, _, _ = EnrichFromAMFI(ctx, pool, entries)
	if matched != 0 {
		t.Fatalf("second run matched %d", matched)
	}
}
