# Wallet Service

A production-oriented RESTful wallet service implemented in Go and PostgreSQL. The service supports wallet creation, deposits, withdrawals, transfers, balance retrieval, and transaction history while preserving strict monetary consistency under concurrent requests.

The implementation is designed as a backend engineering assignment that demonstrates Clean Architecture, explicit dependency injection, immutable financial history, database transaction discipline, idempotent commands, and concurrency-safe balance updates.

## Project overview

The service provides the following capabilities:

- Create a single-currency wallet with a zero balance.
- Deposit funds into a wallet.
- Withdraw funds without allowing an overdraft.
- Transfer funds atomically between two wallets.
- Retrieve a wallet and its current balance.
- Retrieve an individual financial transaction.
- List wallet transaction history with cursor pagination and type filtering.
- Detect readiness and liveness for deployment orchestration.

Core guarantees:

- Amounts must be greater than zero.
- Wallet balances can never become negative.
- A transfer cannot target its source wallet.
- Transfers debit and credit atomically; partial transfers cannot commit.
- Every committed financial operation produces immutable transaction and ledger records.
- Retried mutation requests cannot apply the same operation more than once.
- Concurrent operations on the same wallets are serialized by PostgreSQL row locks.

## Tech stack

| Area | Technology |
|---|---|
| Language | Go 1.25+ |
| HTTP | Go standard library `net/http` router and server |
| Database | PostgreSQL 14+; PostgreSQL 17 in Docker Compose |
| Database driver | `pgx/v5` and `pgxpool` |
| Persistence | Explicit SQL without an ORM |
| API specification | OpenAPI 3.1 |
| Logging | Standard library `log/slog` with JSON output |
| Testing | Go `testing`, table-driven tests, transactional fakes, real PostgreSQL integration tests, race detector |
| Local infrastructure | Docker and Docker Compose |
| CI | GitHub Actions with a PostgreSQL service container |

## Architecture

The project follows Clean Architecture as a modular monolith. Dependencies point inward: transport and persistence adapters depend on application/domain contracts, while the domain does not depend on HTTP, PostgreSQL, or a framework.

```text
HTTP request
    │
    ▼
HTTP handlers, DTOs, validation, middleware
    │
    ▼
Application services and use-case orchestration
    │
    ├──────────────► Domain entities, values, and invariants
    │
    ▼
Application persistence ports
    │
    ▼
PostgreSQL repositories and transaction manager
```

The server entry point is the composition root. It constructs the PostgreSQL adapter, application service, clock and ID dependencies, HTTP handlers, and middleware explicitly.

### Consistency model

Each deposit or withdrawal runs in one PostgreSQL transaction:

```text
BEGIN
  reserve idempotency key
  SELECT wallet FOR UPDATE
  validate wallet, currency, amount, and available balance
  insert financial transaction
  update materialized wallet balance
  insert immutable ledger entry
COMMIT
```

A transfer locks both wallets before reading or changing either balance. Wallet UUIDs are normalized and sorted before locking, so opposing transfers acquire shared locks in the same order and avoid the common `A → B` / `B → A` deadlock cycle.

The service uses PostgreSQL `READ COMMITTED` isolation with explicit `SELECT ... FOR UPDATE` locking. Deadlocks and serialization failures are retried up to three times with bounded backoff.

Database constraints provide a final integrity layer for:

- Nonnegative wallet balances
- Positive transaction and ledger amounts
- Valid transaction participants
- Different source and destination wallets
- Mathematically correct debit and credit ledger entries

See [docs/architecture.md](docs/architecture.md) for detailed transaction flows, invariants, isolation choices, and scaling considerations.

## Folder structure

```text
.
├── api/
│   └── openapi.yaml                    OpenAPI 3.1 contract
├── cmd/
│   ├── migrate/main.go                 Migration executable
│   └── server/main.go                  Application composition root
├── docs/
│   └── architecture.md                 Architecture and consistency notes
├── internal/
│   ├── application/
│   │   ├── commands.go                 Application command DTOs
│   │   ├── ports.go                    Persistence abstractions
│   │   ├── service.go                  Use cases and transaction orchestration
│   │   └── service_test.go             Application unit tests
│   ├── config/
│   │   ├── config.go                   Environment configuration
│   │   └── config_test.go
│   ├── domain/
│   │   ├── errors.go                   Domain error taxonomy
│   │   ├── money.go                    Exact monetary parsing and formatting
│   │   ├── transaction.go              Transaction and ledger models
│   │   ├── wallet.go                   Wallet aggregate and invariants
│   │   └── *_test.go                   Table-driven domain tests
│   ├── infrastructure/postgres/
│   │   ├── migrations/                 Versioned PostgreSQL schema
│   │   ├── migrations.go               Embedded migration runner
│   │   ├── repository.go               SQL repository implementation
│   │   ├── store.go                    Pool and transaction management
│   │   └── *_test.go                   Repository and concurrency tests
│   ├── interfaces/httpapi/
│   │   ├── handler.go                  Routes and HTTP handlers
│   │   ├── dto.go                      HTTP request/response DTOs
│   │   ├── middleware.go               Logging, recovery, timeout, request ID
│   │   ├── problem.go                  Structured problem responses
│   │   ├── cursor.go                   Opaque pagination cursors
│   │   └── *_test.go                   API and middleware tests
│   └── platform/
│       ├── clock/                       Injectable clock
│       └── identity/                    UUID generation and validation
├── .github/workflows/ci.yaml           CI pipeline
├── .env.example                        Example environment configuration
├── compose.yaml                        Local PostgreSQL, migration, and API services
├── Dockerfile                          Multi-stage production image
├── Makefile                            Common development commands
├── go.mod
└── README.md
```

## Prerequisites

Choose either the containerized or native workflow.

### Containerized workflow

- Docker Engine or Docker Desktop
- Docker Compose v2

### Native workflow

- Go 1.25 or newer
- PostgreSQL 14 or newer
- A PostgreSQL database and credentials with schema creation privileges

## Running locally

### Option 1: Docker Compose

Build and start PostgreSQL, apply migrations, and start the API:

```bash
docker compose up --build
```

The migration container must complete successfully before the API starts. The service is then available at:

```text
http://localhost:8080
```

Verify it:

```bash
curl http://localhost:8080/health/live
curl http://localhost:8080/health/ready
```

Stop the containers with:

```bash
docker compose down
```

The PostgreSQL volume is retained by default.

### Option 2: Run Go processes directly

The application reads configuration from environment variables. It does not automatically load `.env` files.

Bash:

```bash
export APP_ENV=development
export HTTP_ADDRESS=:8080
export DATABASE_URL='postgres://wallet:wallet@localhost:5432/wallet?sslmode=disable'
export DATABASE_MAX_CONNECTIONS=20

go run ./cmd/migrate
go run ./cmd/server
```

PowerShell:

```powershell
$env:APP_ENV = "development"
$env:HTTP_ADDRESS = ":8080"
$env:DATABASE_URL = "postgres://wallet:wallet@localhost:5432/wallet?sslmode=disable"
$env:DATABASE_MAX_CONNECTIONS = "20"

go run ./cmd/migrate
go run ./cmd/server
```

Alternatively, set `RUN_MIGRATIONS=true` for local development and start only the server. Production deployments should run `cmd/migrate` as a separate release step; the configuration rejects automatic migrations when `APP_ENV=production`.

### Configuration

| Variable | Default | Description |
|---|---:|---|
| `DATABASE_URL` | Required | PostgreSQL connection URL |
| `APP_ENV` | `development` | Runtime environment |
| `HTTP_ADDRESS` | `:8080` | HTTP listen address |
| `DATABASE_MAX_CONNECTIONS` | `20` | Maximum pool connections; minimum is 2 |
| `RUN_MIGRATIONS` | `false` | Apply embedded migrations during server startup |
| `REQUEST_TIMEOUT` | `10s` | Per-request context deadline |
| `SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown deadline |
| `HTTP_READ_HEADER_TIMEOUT` | `5s` | Header read timeout |
| `HTTP_READ_TIMEOUT` | `15s` | Full request read timeout |
| `HTTP_WRITE_TIMEOUT` | `15s` | Response write timeout |
| `HTTP_IDLE_TIMEOUT` | `60s` | Keep-alive idle timeout |
| `LOG_LEVEL` | `info` | Set to `debug` for debug logging |

See [.env.example](.env.example) for a local configuration template.

## Running tests

### Unit and API tests

```bash
go test ./...
```

Run without cached results:

```bash
go test -count=1 ./...
```

Run static checks and the race detector:

```bash
go vet ./...
go test -race -count=1 ./...
```

The always-on suite includes:

- Table-driven money and wallet domain tests
- Application-service tests backed by a transaction-aware in-memory store
- Idempotency, cancellation, insufficient-funds, overflow, and rollback tests
- API success and validation tests
- Structured error, middleware, recovery, timeout, and pagination tests
- PostgreSQL retry-classification tests

### PostgreSQL integration tests

Database-backed tests are skipped unless `TEST_DATABASE_URL` is configured. Always use a dedicated test database.

Create one in the Compose PostgreSQL container:

```bash
docker compose up -d postgres
docker compose exec postgres createdb -U wallet wallet_test
```

Bash:

```bash
TEST_DATABASE_URL='postgres://wallet:wallet@localhost:5432/wallet_test?sslmode=disable' \
  go test -count=1 ./internal/infrastructure/postgres
```

PowerShell:

```powershell
$env:TEST_DATABASE_URL = "postgres://wallet:wallet@localhost:5432/wallet_test?sslmode=disable"
go test -count=1 ./internal/infrastructure/postgres
```

Integration coverage includes:

- Migration idempotence
- Repository round trips and database constraints
- Rollback after completed wallet, transaction, and ledger writes
- Rollback after constraint violations
- History pagination and filtering
- Concurrent requests sharing an idempotency key
- Concurrent withdrawals exceeding available funds
- Concurrent transfers in opposing directions
- Conservation of total money and nonnegative-balance invariants

The GitHub Actions workflow provisions PostgreSQL and runs the complete suite with the race detector.

## API endpoints

The complete contract is available in [api/openapi.yaml](api/openapi.yaml).

| Method | Path | Description | Success |
|---|---|---|---:|
| `GET` | `/health/live` | Process liveness | `200` |
| `GET` | `/health/ready` | PostgreSQL readiness | `200` |
| `POST` | `/v1/wallets` | Create a zero-balance wallet | `201` |
| `GET` | `/v1/wallets/{walletId}` | Retrieve wallet and balance | `200` |
| `POST` | `/v1/wallets/{walletId}/deposits` | Deposit funds | `201` |
| `POST` | `/v1/wallets/{walletId}/withdrawals` | Withdraw funds | `201` |
| `POST` | `/v1/transfers` | Transfer between wallets | `201` |
| `GET` | `/v1/transactions/{transactionId}` | Retrieve a financial transaction | `200` |
| `GET` | `/v1/wallets/{walletId}/transactions` | List wallet history | `200` |

### Request conventions

- Requests with JSON bodies require `Content-Type: application/json`.
- Mutation endpoints require an `Idempotency-Key` header of at most 128 characters.
- `X-Client-ID` optionally scopes idempotency keys; it defaults to `public` in this assignment.
- Amounts are decimal strings, not JSON numbers, to avoid floating-point interpretation.
- Supported currencies are `USD`, `EUR`, `GBP`, and `JPY`.
- Mutation responses return `201 Created`; a successful idempotent replay returns `200 OK` with the original resource.

### Create wallet

```http
POST /v1/wallets
Content-Type: application/json
Idempotency-Key: create-wallet-001
X-Client-ID: example-client

{
  "currency": "USD"
}
```

### Deposit or withdraw

```http
POST /v1/wallets/{walletId}/deposits
Content-Type: application/json
Idempotency-Key: deposit-001

{
  "amount": "100.00",
  "currency": "USD"
}
```

Use `/withdrawals` with the same request shape for a withdrawal.

### Transfer

```http
POST /v1/transfers
Content-Type: application/json
Idempotency-Key: transfer-001

{
  "sourceWalletId": "11111111-1111-4111-8111-111111111111",
  "destinationWalletId": "22222222-2222-4222-8222-222222222222",
  "amount": "25.00",
  "currency": "USD"
}
```

### Transaction history

```http
GET /v1/wallets/{walletId}/transactions?limit=50&cursor={opaqueCursor}&type=TRANSFER
```

- `limit` defaults to 50 and must be between 1 and 100.
- `cursor` is an opaque continuation token returned as `nextCursor`.
- `type` optionally filters by `DEPOSIT`, `WITHDRAWAL`, or `TRANSFER`.

### Error responses

Errors use `application/problem+json` with stable machine-readable codes and a request ID:

```json
{
  "type": "https://wallet.example/problems/INSUFFICIENT_FUNDS",
  "title": "Insufficient funds",
  "status": 422,
  "code": "INSUFFICIENT_FUNDS",
  "detail": "wallet has insufficient funds",
  "instance": "/v1/wallets/11111111-1111-4111-8111-111111111111/withdrawals",
  "requestId": "b618a705-2928-4f41-ab1f-c2fd95622887"
}
```

Common statuses:

| Status | Meaning |
|---:|---|
| `400` | Malformed request, invalid UUID, cursor, or query parameter |
| `404` | Wallet, transaction, or route not found |
| `409` | Inactive wallet or idempotency-key conflict |
| `413` | Request body exceeds 1 MiB |
| `415` | Unsupported media type |
| `422` | Invalid amount, unsupported/mismatched currency, insufficient funds, or same-wallet transfer |
| `503` | Database readiness failure, cancellation, or request deadline |

## Assumptions

- Each wallet holds exactly one currency.
- Wallets are created with a zero balance; initial funding must be a deposit so it appears in history.
- Monetary values are stored as signed 64-bit minor units and never as floating point.
- USD, EUR, and GBP use two fractional digits; JPY uses none.
- Transaction history contains successfully committed financial operations. Rejected requests are operational/security events, not ledger entries.
- Deposits and withdrawals represent money crossing the service boundary; external settlement is outside the assignment.
- The database is the authoritative consistency boundary for wallet balances and history.
- Authentication, wallet ownership, and authorization are outside the stated assignment scope.

## Design decisions

### Modular monolith instead of microservices

Wallet mutations and transfers require a strong transaction boundary. A modular monolith backed by one PostgreSQL database provides clear ACID behavior without distributed transactions or eventual-balance consistency.

### Integer minor units instead of floating point

The API accepts exact decimal strings and converts them to `int64` minor units. This prevents binary floating-point rounding errors and makes overflow handling explicit.

### Materialized balance plus immutable ledger

`wallets.balance_minor` supports efficient reads and row locking. Immutable `financial_transactions` and `ledger_entries` provide audit history and balance-before/balance-after evidence. Both are updated in the same transaction.

### Pessimistic locking

Financial commands use `SELECT ... FOR UPDATE`. This makes competing operations deterministic and prevents lost updates and overdraw races. Transfers lock both wallets in sorted UUID order.

### Explicit SQL and repository ports

The service uses `pgx` rather than an ORM. SQL behavior, row locking, transaction boundaries, indexes, and constraints remain visible and reviewable. The application layer depends only on persistence interfaces.

### Idempotent mutations

The `(scope, key)` pair is unique in PostgreSQL. Each record stores a SHA-256 request fingerprint and resource ID. Replaying the same request returns the existing resource; changing the payload under the same key returns `409 Conflict`.

### Cursor pagination

History uses ledger sequence cursors instead of offset pagination. Cursor pagination remains stable as new transactions arrive and avoids increasingly expensive offsets.

### Separate migration process

The repository includes a dedicated migration executable with an advisory lock and version table. Production deployments can migrate once as a release step instead of letting every API replica compete during startup.

## Current limitations

- There is no authentication, authorization, wallet ownership, or tenant model.
- `X-Client-ID` is accepted from the request and must not be trusted as identity in production.
- Currency support is intentionally limited to USD, EUR, GBP, and JPY.
- Deposits and withdrawals are not integrated with an external payment or settlement provider.
- Deposits and withdrawals have wallet ledger entries but do not yet post against internal clearing accounts for full double-entry accounting.
- Wallet status exists in the domain and schema, but there are no administrative freeze, unfreeze, or close endpoints.
- Idempotency records have no retention or archival policy.
- There is no asynchronous outbox, notification system, or event broker integration.
- There is no automated ledger-to-materialized-balance reconciliation job.
- Metrics, distributed tracing, rate limiting, and alert definitions are not included.
- The database is not partitioned or sharded, and the service uses one writable PostgreSQL primary.
- Integration tests require an externally supplied PostgreSQL test database.

## Future improvements

- Add authentication and derive wallet ownership and idempotency scope from the authenticated principal.
- Add authorization policies for wallet reads, transfers, deposits, and administrative operations.
- Introduce internal clearing and settlement accounts for full double-entry bookkeeping.
- Integrate deposits and withdrawals with payment-provider authorization, settlement, and reconciliation workflows.
- Add a transactional outbox for notifications, analytics, and downstream integrations.
- Implement reversal and compensating transactions rather than editing financial history.
- Add scheduled balance reconciliation and ledger integrity reporting.
- Add Prometheus metrics, OpenTelemetry traces, dashboards, and lock-contention alerts.
- Add rate limiting, abuse detection, audit-event retention, and security controls.
- Define an idempotency retention/archive policy appropriate for business and regulatory requirements.
- Add read replicas for history traffic when replica lag is acceptable.
- Partition ledger history after measured volume justifies it.
- Add account statements, date-range filtering, and export capabilities.
- Add multi-region and disaster-recovery procedures before introducing database sharding.

## License

This repository is provided as a technical-assignment implementation. Add the license required by the target organization before distribution.
