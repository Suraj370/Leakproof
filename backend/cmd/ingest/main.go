// Command ingest loads reference data into PostgreSQL. Run it from cron / Kubernetes CronJobs:
//
//	ingest funds    <funds.csv>
//	ingest holdings <holdings.csv> --month 2025-03 [--force]
//	ingest cards    <cards.json>
//	ingest amfi     [file | --url URL]
//	ingest reap
//
// Exit codes: 0 ok, 1 failure, 3 some records were rejected by validation (the rest were applied).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"leakproof/internal/catalog"
	"leakproof/internal/ingest"
	"leakproof/internal/reports"
)

const defaultAMFI = "https://portal.amfiindia.com/spages/NAVAll.txt"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errw, "usage: ingest funds|holdings|cards|amfi|reap [args]")
		return 1
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(errw, "DATABASE_URL is required")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(errw, "database:", err)
		return 1
	}
	defer pool.Close()

	cmd, rest := args[0], args[1:]
	code := 0
	switch cmd {
	case "funds":
		code = runFunds(ctx, pool, rest, out, errw)
	case "holdings":
		code = runHoldings(ctx, pool, rest, out, errw)
	case "cards":
		code = runCards(ctx, pool, rest, out, errw)
	case "amfi":
		code = runAMFI(ctx, pool, rest, out, errw)
	case "reap":
		n, err := reports.Reap(ctx, pool)
		if err != nil {
			fmt.Fprintln(errw, "reap:", err)
			return 1
		}
		fmt.Fprintf(out, "removed %d expired reports\n", n)
	default:
		fmt.Fprintln(errw, "unknown command", cmd)
		return 1
	}
	if code == 0 || code == 3 {
		invalidateCache(ctx, errw)
	}
	return code
}

func invalidateCache(ctx context.Context, errw io.Writer) {
	u := os.Getenv("REDIS_URL")
	if u == "" {
		return
	}
	opt, err := redis.ParseURL(u)
	if err != nil {
		fmt.Fprintln(errw, "redis url:", err)
		return
	}
	rc := redis.NewClient(opt)
	defer rc.Close()
	if err := (catalog.Cached{Redis: rc, Key: "leakproof:catalog:v1"}).Invalidate(ctx); err != nil {
		fmt.Fprintln(errw, "cache invalidate (non-fatal):", err)
	}
}

// resolveMonth parses YYYY-MM, or "last" = the previous calendar month (cron jobs cannot compute dates in args).
func resolveMonth(s string, now time.Time) (time.Time, error) {
	if s == "last" {
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0), nil
	}
	return time.Parse("2006-01", s)
}

func openArg(path string) (io.ReadCloser, error) {
	if path == "-" {
		return io.NopCloser(os.Stdin), nil
	}
	return os.Open(path)
}

func runFunds(ctx context.Context, pool *pgxpool.Pool, args []string, out, errw io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errw, "usage: ingest funds <funds.csv>")
		return 1
	}
	f, err := openArg(args[0])
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer f.Close()
	rows, bad, err := ingest.ParseFundsCSV(f)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	for _, b := range bad {
		fmt.Fprintln(errw, "rejected:", b)
	}
	if len(rows) > 0 {
		if err := ingest.UpsertFunds(ctx, pool, rows); err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
	}
	fmt.Fprintf(out, "funds: %d upserted, %d rejected\n", len(rows), len(bad))
	if len(bad) > 0 {
		return 3
	}
	return 0
}

func runHoldings(ctx context.Context, pool *pgxpool.Pool, args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("holdings", flag.ContinueOnError)
	fs.SetOutput(errw)
	monthStr := fs.String("month", "", "disclosure month YYYY-MM, or \"last\" for the previous calendar month (required)")
	force := fs.Bool("force", false, "accept holdings that differ >60% from last month")
	// allow the file before or after the flags
	var file string
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		file, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if file == "" && fs.NArg() == 1 {
		file = fs.Arg(0)
	}
	month, err := resolveMonth(*monthStr, time.Now().UTC())
	if err != nil || file == "" {
		fmt.Fprintln(errw, "usage: ingest holdings <holdings.csv> --month YYYY-MM [--force]")
		return 1
	}
	f, err := openArg(file)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer f.Close()
	funds, msgs, err := ingest.ParseHoldingsCSV(f)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	rejected := len(msgs)
	for _, m := range msgs {
		fmt.Fprintln(errw, "rejected:", m)
	}
	isins := make([]string, 0, len(funds))
	for k := range funds {
		isins = append(isins, k)
	}
	sort.Strings(isins)
	applied := 0
	for _, isin := range isins {
		if err := ingest.ReplaceHoldings(ctx, pool, month, funds[isin], *force); err != nil {
			fmt.Fprintf(errw, "rejected: fund %s: %v\n", isin, err)
			rejected++
			continue
		}
		applied++
	}
	fmt.Fprintf(out, "holdings %s: %d funds applied, %d problems\n", *monthStr, applied, rejected)
	if rejected > 0 {
		return 3
	}
	return 0
}

func runCards(ctx context.Context, pool *pgxpool.Pool, args []string, out, errw io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errw, "usage: ingest cards <cards.json>")
		return 1
	}
	f, err := openArg(args[0])
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer f.Close()
	in, err := ingest.ParseCardsJSON(f)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	res, err := ingest.ApplyCards(ctx, pool, in)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	fmt.Fprintf(out, "cards: %d created, %d new versions, %d unchanged\n", len(res.Created), len(res.Updated), len(res.Unchanged))
	for _, s := range res.Updated {
		fmt.Fprintln(out, "  new version:", s)
	}
	return 0
}

func runAMFI(ctx context.Context, pool *pgxpool.Pool, args []string, out, errw io.Writer) int {
	var r io.ReadCloser
	var err error
	if len(args) == 0 || args[0] == "--url" {
		url := defaultAMFI
		if len(args) == 2 {
			url = args[1]
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, herr := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
		if herr != nil {
			fmt.Fprintln(errw, "fetch:", herr)
			return 1
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			fmt.Fprintln(errw, "fetch: status", resp.StatusCode)
			return 1
		}
		r = resp.Body
	} else if r, err = openArg(args[0]); err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer r.Close()
	entries, err := ingest.ParseAMFI(r)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	matched, unmatched, err := ingest.EnrichFromAMFI(ctx, pool, entries)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	fmt.Fprintf(out, "amfi: %d schemes read, %d funds got an AMFI code, %d not matched (not in our catalog or already coded)\n",
		len(entries), matched, unmatched)
	return 0
}
