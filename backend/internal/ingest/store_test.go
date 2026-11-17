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

	"leakproof/internal/cards"
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

func TestIntegrationGrandfatherNAVRoundTrip(t *testing.T) {
	pool := tempDB(t)
	ctx := context.Background()
	withNAV := strings.Replace(goodFunds, "ter_as_of\n", "ter_as_of,nav_2018_01_31\n", 1)
	withNAV = strings.Replace(withNAV, "2025-03-31\nINF179", "2025-03-31,41.2345\nINF179", 1)
	withNAV = strings.Replace(withNAV, "0,0,2025-03-31\n", "0,0,2025-03-31,\n", 1)
	if err := UpsertFunds(ctx, pool, mustFunds(t, withNAV)); err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Load(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Funds["INF000TEST01"].GrandfatherNAV.String(); got != "41.2345" {
		t.Fatalf("nav = %q", got)
	}
	if !c.Funds["INF179K01AA1"].GrandfatherNAV.IsZero() {
		t.Fatal("blank NAV must stay unknown")
	}
	// re-importing without the column must not erase a known NAV
	if err := UpsertFunds(ctx, pool, mustFunds(t, goodFunds)); err != nil {
		t.Fatal(err)
	}
	c, _ = catalog.Load(ctx, pool)
	if c.Funds["INF000TEST01"].GrandfatherNAV.String() != "41.2345" {
		t.Fatal("NAV erased by a later import that omitted it")
	}
}

// luhnISIN returns prefix11 plus the check digit that makes it a valid ISIN.
func luhnISIN(t *testing.T, prefix11 string) string {
	t.Helper()
	for d := 0; d <= 9; d++ {
		if s := prefix11 + string(rune('0'+d)); ValidISIN(s) {
			return s
		}
	}
	t.Fatalf("no check digit for %s", prefix11)
	return ""
}

func TestIntegrationFundOfFundsIsLookedThroughOnLoad(t *testing.T) {
	pool := tempDB(t)
	ctx := context.Background()
	under, fof := luhnISIN(t, "INF000UND01"), luhnISIN(t, "INF000FOF01")

	funds := "isin,name,amc,plan,category,equity_oriented,ter_bps,ter_as_of\n" +
		under + ",Underlying Equity,X,DIRECT,Large Cap,true,50,2025-03-31\n" +
		fof + ",Fund of Funds,X,REGULAR,FoF,true,140,2025-03-31\n"
	if err := UpsertFunds(ctx, pool, mustFunds(t, funds)); err != nil {
		t.Fatal(err)
	}

	// A fund ISIN is a legal "stock" row in a FoF's holdings file.
	csv := holdingsCSV(
		under+",INE040A01034,HDFC Bank,30",
		under+",INE090A01021,ICICI Bank,20",
		under+",INE009A01021,Infosys,10",
		fof+","+under+",Underlying Equity,90",
		fof+",INE002A01018,Reliance,5",
	)
	parsed, msgs, err := ParseHoldingsCSV(strings.NewReader(csv))
	if err != nil || len(msgs) != 0 || len(parsed) != 2 {
		t.Fatalf("%v %v %d", err, msgs, len(parsed))
	}
	mar := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	for _, fh := range parsed {
		if err := ReplaceHoldings(ctx, pool, mar, fh, false); err != nil {
			t.Fatal(err)
		}
	}

	c, err := catalog.Load(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	h := c.Funds[fof].Holdings
	if _, opaque := h[under]; opaque {
		t.Fatalf("FoF still holds the fund as an opaque position: %v", h)
	}
	want := map[string]int64{"INE040A01034": 2700, "INE090A01021": 1800, "INE009A01021": 900, "INE002A01018": 500}
	for k, v := range want {
		if h[k] != v {
			t.Errorf("%s = %d, want %d (%v)", k, h[k], v, h)
		}
	}
	if c.Funds[fof].HoldingNames["INE040A01034"] != "HDFC Bank" {
		t.Fatalf("names: %v", c.Funds[fof].HoldingNames)
	}
}

func TestIntegrationCardMilestonesVersioned(t *testing.T) {
	pool := tempDB(t)
	ctx := context.Background()
	apply := func(js string) CardResult {
		in, err := ParseCardsJSON(strings.NewReader(js))
		if err != nil {
			t.Fatal(err)
		}
		res, err := ApplyCards(ctx, pool, in)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	// thresholds given out of order: stored and compared sorted
	v1 := withMilestones(`[{"annualSpendPaise": 200000000, "bonusValuePaise": 500000},{"annualSpendPaise": 80000000, "bonusValuePaise": 1000000}]`)
	if r := apply(v1); len(r.Created) != 1 {
		t.Fatalf("%+v", r)
	}
	if r := apply(v1); len(r.Unchanged) != 1 {
		t.Fatalf("re-apply must be a no-op: %+v", r)
	}
	// same milestones, different order in the file: still unchanged
	reordered := withMilestones(`[{"annualSpendPaise": 80000000, "bonusValuePaise": 1000000},{"annualSpendPaise": 200000000, "bonusValuePaise": 500000}]`)
	if r := apply(reordered); len(r.Unchanged) != 1 {
		t.Fatalf("order must not matter: %+v", r)
	}

	c, _ := catalog.Load(ctx, pool)
	var ms []cards.Milestone
	for _, cd := range c.Cards {
		if cd.ID == "test-cashback" {
			ms = cd.Milestones
		}
	}
	if len(ms) != 2 || ms[0].AnnualSpendPaise != 80_000_000 || ms[0].BonusValuePaise != 1_000_000 {
		t.Fatalf("%+v", ms)
	}

	// Devalued milestone (voucher cut) is a new version effective later.
	cut := strings.Replace(v1, `"bonusValuePaise": 1000000`, `"bonusValuePaise": 600000`, 1)
	cut = strings.Replace(cut, `2025-04-01`, `2025-06-01`, 1)
	if r := apply(cut); len(r.Updated) != 1 {
		t.Fatalf("milestone change must create a new version: %+v", r)
	}
	c, _ = catalog.Load(ctx, pool)
	for _, cd := range c.Cards {
		if cd.ID == "test-cashback" && cd.Milestones[0].BonusValuePaise != 600_000 {
			t.Fatalf("old milestone still served: %+v", cd.Milestones)
		}
	}
}
