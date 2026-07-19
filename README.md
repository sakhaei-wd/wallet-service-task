# Wallet Service

A production-oriented REST wallet service written in idiomatic Go. It uses Clean Architecture, explicit dependency injection, PostgreSQL transactions, pessimistic row locking, immutable ledger entries, and idempotent mutations.

## Correctness guarantees

- Monetary values are parsed from decimal strings and stored as `BIGINT` minor units. Floating point arithmetic is never used.
- Every committed deposit, withdrawal, and transfer creates an immutable financial transaction and ledger entry in the same database transaction as the balance update.
- PostgreSQL `SELECT ... FOR UPDATE` serializes mutations to a wallet.
- Transfers lock both wallets in ascending UUID order to reduce deadlocks.
- PostgreSQL constraints reject negative wallet balances and invalid ledger arithmetic.
- Mutation endpoints require `Idempotency-Key`. Reusing a key with the same request returns the original resource; using it with different input returns `409 Conflict`.
- Deadlocks and serialization failures are retried a bounded number of times.

## Architecture

```text
cmd/server                    composition root and process lifecycle
internal/interfaces/httpapi  REST handlers, DTOs, middleware, presentation
internal/application         use cases and persistence ports
internal/domain              wallet, money, transaction, and ledger rules
internal/infrastructure      PostgreSQL adapters and migrations
internal/platform            clock and identity abstractions
```

Dependencies point inward: HTTP and PostgreSQL depend on application/domain contracts, while the domain has no framework or database dependency. The server composition root wires concrete adapters into the application service.

The service is intentionally a modular monolith. Wallet balances and transfers require a strong consistency boundary; splitting the write path across services would add distributed transaction failure modes without helping the stated workload.

See [docs/architecture.md](docs/architecture.md) for the transaction flows, invariants, isolation choice, and scaling path.

## Prerequisites

- Go 1.25 or newer
- PostgreSQL 14 or newer, or Docker Compose

## Run locally

The fastest route is Docker Compose:

```bash
docker compose up --build
```

The API listens on `http://localhost:8080`. Compose runs the migration job before starting the API.

To run the processes directly:

```bash
cp .env.example .env
export DATABASE_URL='postgres://wallet:wallet@localhost:5432/wallet?sslmode=disable'
go run ./cmd/migrate
go run ./cmd/server
```

Configuration is environment-based. See [.env.example](.env.example). In production, run `cmd/migrate` as a release step and leave `RUN_MIGRATIONS=false` on API instances.

## API examples

All request amounts are decimal strings. Supported currencies in this version are `USD`, `EUR`, `GBP`, and `JPY`.

Create a wallet:

```bash
curl -i http://localhost:8080/v1/wallets \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: create-wallet-001' \
  -H 'X-Client-ID: interview-client' \
  -d '{"currency":"USD"}'
```

Deposit:

```bash
curl -i http://localhost:8080/v1/wallets/{walletId}/deposits \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: deposit-001' \
  -H 'X-Client-ID: interview-client' \
  -d '{"amount":"100.00","currency":"USD"}'
```

Transfer:

```bash
curl -i http://localhost:8080/v1/transfers \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: transfer-001' \
  -H 'X-Client-ID: interview-client' \
  -d '{"sourceWalletId":"...","destinationWalletId":"...","amount":"25.00","currency":"USD"}'
```

Transaction history uses opaque cursor pagination:

```text
GET /v1/wallets/{walletId}/transactions?limit=50&cursor=...&type=TRANSFER
```

See [api/openapi.yaml](api/openapi.yaml) for the complete contract.

## Status and error behavior

- New resources return `201 Created` and a `Location` header.
- An idempotent replay returns `200 OK` with the original resource.
- Malformed requests return `400 Bad Request`.
- Missing resources return `404 Not Found`.
- Reused idempotency keys with different payloads return `409 Conflict`.
- Invalid money, currency mismatch, same-wallet transfer, and insufficient funds return `422 Unprocessable Entity`.
- Errors use RFC 9457-style `application/problem+json` documents with stable codes and request IDs.

`X-Client-ID` is used only as the idempotency namespace in this assignment. In production it should be derived from authenticated identity, not trusted directly from a public header.

## Tests

Run unit tests and static checks:

```bash
go test ./...
go vet ./...
```

The PostgreSQL concurrency test is skipped unless `TEST_DATABASE_URL` is present:

```bash
TEST_DATABASE_URL='postgres://wallet:wallet@localhost:5432/wallet_test?sslmode=disable' \
  go test ./internal/infrastructure/postgres -run TestConcurrent -count=1
```

That test submits more concurrent withdrawals than the wallet can fund, then verifies the balance remains zero or positive and history contains only committed operations.

## Operational notes

- `/health/live` checks process liveness.
- `/health/ready` checks PostgreSQL connectivity.
- Logs are structured JSON and include request IDs, status, latency, and response size.
- The HTTP server has read, write, idle, request, and graceful shutdown timeouts.
- Database pool size is bounded through `DATABASE_MAX_CONNECTIONS`.

For external event publication, add an outbox row in the existing transaction and publish it asynchronously. Do not publish directly inside the balance transaction. For full accounting, add internal clearing accounts so deposits and withdrawals also become balanced double-entry postings.
