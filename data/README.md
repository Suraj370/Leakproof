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
