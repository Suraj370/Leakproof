# Leakproof

Mutual fund overlap / tax-aware consolidation and Indian credit card reward optimiser, framed as
"annual rupees left on the table". Guest mode only: request bodies are processed in memory and never stored.

```
infra/     docker-compose.yml (Dockerfiles live beside the code in backend/ and frontend/)
backend/   Go 1.22 + Fiber  (internal/mf, internal/cards, internal/api, migrations/)
frontend/  React 19 + TS strict + Tailwind + Zustand + Vitest
```

All money is integer paise, all rates are basis points. `backend/testdata/golden_cards.json` is asserted by
both the Go and TypeScript test suites so the server and the client simulator can't drift.

## Run (Docker)

```bash
docker compose -f infra/docker-compose.yml up --build            # UI on http://localhost:3000, API on :8080
docker compose -f infra/docker-compose.yml --profile data up -d postgres redis   # optional: schema in backend/migrations is auto-applied
```

## Tests

```bash
docker compose -f infra/docker-compose.yml --profile test run --rm --build backend-test    # go vet + go test
docker compose -f infra/docker-compose.yml --profile test run --rm --build frontend-test   # tsc + vitest
```

Without Docker: `cd backend && go mod tidy && go test ./...` and `cd frontend && pnpm install && pnpm test`.

## What is and isn't built

Built: CAS PDF upload (RC4 / AES-128 decryption, in-memory text extraction, FIFO lot reconstruction, PII-free
output), overlap engine (bounded errgroup pool), STCG/LTCG/exit-load verdicts (FY-boundary and loss set-off aware),
merchant classifier, card reward engine with caps / exclusions / redemption value / fee waivers, 1-and-2-card
solver (marginal-greedy heuristic), stateless API, schema, heatmap UI, live simulator, share card.

**CAS parser caveat:** it is tested against synthetic statements (`backend/testdata/`; regenerate the PDFs with
`gen_pdf_fixtures.sh`) modelled on the usual CAMS/KFintech detailed layout. It has not been validated on real
statements, and AES-256 encrypted PDFs are rejected with a clear error (the PDF library only supports up to AES-128).

Not built yet: bank-statement upload feeding the classifier, the PostgreSQL/Redis-backed catalog (the API serves an
in-memory **illustrative** seed catalog; schemes not in it are skipped and reported), saving a score, generated TS
types, and the AMFI / card-rule ingestion cron jobs. Seed fund and card data is demo data, not real terms.
