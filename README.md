# Double-Entry Ledger System

A Go and PostgreSQL ledger API for exploring double-entry accounting, transactional correctness, and idempotent payments. This learning project intentionally has no authentication.

## Run locally

Copy `.env.example` to `.env`, set the password in `DATABASE_URL` to match your local PostgreSQL role, and create the `ledger_app` role and `ledger_dev` database. The server applies the embedded, versioned SQL migrations before listening.

```sh
cp .env.example .env
# Edit DATABASE_URL in .env to use the password you set for ledger_app.
go run ./cmd/server
```

The application loads `.env` automatically when present. Variables already set in the process environment take precedence. In deployment, set environment variables directly; a `.env` file is optional.

Trace sampling defaults to 100% in development and 10% in production. Set `OTEL_TRACES_SAMPLER_ARG` to a value from `0` to `1` to override the ratio. Traces record HTTP method, route template, status, and trace context; request bodies and authorization headers are not recorded.

The startup log includes version and commit fields. Supply build metadata when building a release:

```sh
go build -ldflags "-X main.version=1.0.0 -X main.commit=$(git rev-parse --short HEAD)" -o ledger-api ./cmd/server
```

The server does not bind its HTTP listener unless PostgreSQL is reachable and all embedded migrations apply cleanly. Startup fails on a dirty schema or when the database migration version differs from the migrations embedded in this binary.

`configs/config.example.yaml` documents settings that may move into YAML configuration later. It is a reference only; the server currently reads environment variables, and database credentials should remain in `.env` or the deployment environment.

Amounts are signed 64-bit integers in the currency's minor unit (for example, cents); the API does not accept floating-point money values. Every account is denominated in one currency, and payments between accounts of different currencies are rejected.

## API

- `GET /healthz` — process liveness and API runtime information; does not depend on PostgreSQL.
- `GET /readyz` — API, PostgreSQL, and migration readiness; returns `503` unless every component is healthy.
- `GET /metrics` — Prometheus Go/process, PostgreSQL connection-pool, and HTTP request metrics. HTTP metrics use bounded route-pattern labels.
- `POST /v1/accounts` — create a zero-balance account. JSON: `{"name":"wallet","currency":"KES"}`.
- `GET /v1/accounts/{accountID}` — read an account and its current balance.
- `GET /v1/accounts/{accountID}/entries?limit=50&offset=0` — read signed journal entries; `limit` is capped at 100.
- `POST /v1/accounts/{accountID}/deposits` — simulate external funding. Requires `Idempotency-Key`; JSON: `{"amount_minor":10000}`.
- `POST /v1/payments` — transfer between accounts. Requires `Idempotency-Key`; JSON: `{"source_account_id":"...","destination_account_id":"...","amount_minor":2500}`.

Health responses include service name, Go runtime version, check timestamp, process uptime, overall status, and per-component status. `/readyz` also reports PostgreSQL ping latency and the applied, expected, and dirty migration state, for example:

```json
{
  "service": "double-entry-ledger",
  "go_version": "go1.27.1",
  "status": "healthy",
  "checked_at": "2026-10-28T10:00:00Z",
  "uptime_seconds": 42.5,
  "components": {
    "api": {
      "status": "healthy"
    },
    "database": {
      "status": "healthy",
      "latency_ms": 1
    },
    "migrations": {
      "status": "healthy",
      "latency_ms": 0,
      "version": 1,
      "expected_version": 1,
      "dirty": false
    }
  }
}
```

Deposits debit a per-currency external-clearing account and credit the user account. Payments debit the source and credit the destination. Both sides, the transfer record, and account balance updates commit in one PostgreSQL transaction. Retrying an identical request with the same idempotency key returns its original transfer; reusing the key with a different request returns `409 Conflict`.

The database enforces exactly two transfer legs in the same currency: one debit from the declared source and one credit to the declared destination. It rejects entries appended after posting, and prevents journal or transfer updates, deletes, and truncates. Deferred checks require each account balance projection to equal the sum of its journal entries at commit. Account currency and system-account identity cannot be changed after creation. Migration `000002` validates existing data before enabling these stricter rules.

Zap emits structured startup and per-request logs, including request IDs and trace/span IDs when available. OpenTelemetry creates HTTP server spans and propagates W3C Trace Context. Trace export is enabled when `OTEL_EXPORTER_OTLP_ENDPOINT` is set; without it, spans are created but not exported.

The migration source is embedded from `migrations/sql` by `migrations/embed.go`.

PostgreSQL invariant integration tests run when `LEDGER_TEST_DATABASE_URL` points to a dedicated test database whose user can create schemas. Each test creates and removes an isolated schema:

```sh
LEDGER_TEST_DATABASE_URL='postgres://user:password@localhost:5432/ledger_test?sslmode=disable' \
  go test -count=1 ./internal/storage/postgres
```
