# Sample ingestion data

`*.sample.*` files are **fabricated** (fake scheme ISINs, made-up card terms) and exist to exercise the pipeline.
Real data comes from:

| File | Real source | Cadence |
|---|---|---|
| `funds.csv` | AMC TER disclosures + AMFI scheme master | weekly |
| `holdings.csv` | AMC monthly portfolio disclosures (due by the 10th) | monthly, days 11/15/20 |
| `cards.json` | issuer T&C / MITC pages, **human-reviewed** before commit | weekly check |

Try it locally:

```bash
docker compose -f infra/docker-compose.yml --profile data up -d postgres
export DATABASE_URL=postgres://wealth:wealth_dev_only@postgres:5432/leakproof
docker compose -f infra/docker-compose.yml --profile data run --rm ingest funds /data/funds.sample.csv
docker compose -f infra/docker-compose.yml --profile data run --rm ingest holdings /data/holdings.sample.csv --month 2025-03
docker compose -f infra/docker-compose.yml --profile data run --rm ingest cards /data/cards.sample.json
```

Validation gates (a failing record is rejected and the job exits 3, everything valid is still applied):
stock ISIN checksum, weights in (0,100], per-fund weights summing to 40-100.5%, no duplicate stocks, month-over-month
carry-over of at least 40% (else `--force`), TER within 0-500 bps, card caps/earn rates in sane ranges, and new card
versions must start after the current one.

Optional inputs:

- `funds.csv` may carry a `nav_2018_01_31` column (the fund NAV on 31-Jan-2018). With it, units bought before Feb 2018 get
  the grandfathered cost of acquisition; without it the engine uses actual cost and tells the user tax may be overstated.
  A blank value never erases a NAV that is already loaded.
- `cards.json` cards may carry `"milestones": [{"annualSpendPaise": 80000000, "bonusValuePaise": 1000000}]`: an annual
  eligible-spend bonus (up to 6, bonus no larger than the spend that unlocks it). Changing milestones creates a new card
  version like any other rule change.
- A fund-of-funds lists its underlying funds in `holdings.csv` using the underlying fund ISIN as `stock_isin`; the catalog
  loader expands them to the real stocks.
