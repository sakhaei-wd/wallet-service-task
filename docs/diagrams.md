# Project Diagrams

These diagrams describe the current Wallet Service implementation. Package names, database objects, transaction behavior, and deployment relationships match the repository as implemented.

## 1. High-Level System Architecture

```mermaid
flowchart LR
    Client[API Client]

    subgraph Process[Wallet Service Process]
        API[REST API<br/>net/http middleware and handlers]
        Service[Service Layer<br/>internal/application.Service]
        Repository[Repository Layer<br/>internal/infrastructure/postgres.Store]
    end

    Database[(PostgreSQL)]

    Client -->|HTTP and JSON| API
    API -->|Use-case calls with context.Context| Service
    Service -->|application.Store interfaces| Repository
    Repository -->|pgx/v5 and explicit SQL| Database
```

The HTTP adapter translates requests and errors, the application service owns use-case orchestration, and the PostgreSQL adapter implements the persistence interfaces declared by the application layer.

## 2. Request Flow

```mermaid
flowchart TB
    Request[HTTP Request]
    Server[net/http Server]
    Middleware[Middleware Chain<br/>request ID, recovery, logging, timeout]
    Router[Router<br/>http.ServeMux]
    Handler[Handler<br/>decode DTO, validate transport input]
    Service[Service<br/>authorize and enforce use-case rules]
    Repository[PostgreSQL Store and Repository<br/>transaction boundary and explicit SQL]
    Database[(PostgreSQL)]
    Response[HTTP Response<br/>JSON DTO or problem+json]

    Request --> Server --> Middleware --> Router --> Handler
    Handler -->|context.Context| Service
    Service -->|application.Store| Repository
    Repository -->|SQL through pgx/v5| Database

    Database -.->|rows or error| Repository
    Repository -.->|domain result or error| Service
    Service -.->|domain result or error| Handler
    Handler -.->|status, headers, and body| Middleware
    Middleware -.->|final response| Response
```

## 3. Entity Relationship Diagram

```mermaid
erDiagram
    WALLETS o|--o{ FINANCIAL_TRANSACTIONS : "is optional source of"
    WALLETS o|--o{ FINANCIAL_TRANSACTIONS : "is optional destination of"
    WALLETS ||--o{ LEDGER_ENTRIES : "owns"
    FINANCIAL_TRANSACTIONS ||--o{ LEDGER_ENTRIES : "records"

    WALLETS {
        UUID id PK
        UUID owner_id UK
        VARCHAR currency
        BIGINT balance_minor
        VARCHAR status
        BIGINT version
        TIMESTAMPTZ created_at
        TIMESTAMPTZ updated_at
    }

    FINANCIAL_TRANSACTIONS {
        UUID id PK
        VARCHAR type
        BIGINT amount_minor
        VARCHAR currency
        UUID source_wallet_id FK "nullable"
        UUID destination_wallet_id FK "nullable"
        TIMESTAMPTZ created_at
    }

    LEDGER_ENTRIES {
        BIGSERIAL sequence PK
        UUID transaction_id FK
        UUID wallet_id FK
        VARCHAR direction
        BIGINT amount_minor
        BIGINT balance_before_minor
        BIGINT balance_after_minor
        TIMESTAMPTZ created_at
    }

    IDEMPOTENCY_RECORDS {
        VARCHAR scope PK
        VARCHAR key PK
        CHAR request_hash
        VARCHAR resource_type
        UUID resource_id
        TIMESTAMPTZ created_at
    }
```

`financial_transactions.source_wallet_id` and `destination_wallet_id` both reference `wallets.id` and are nullable according to transaction type. Each committed financial operation has one or two ledger entries. The database also enforces uniqueness of `(transaction_id, wallet_id)` in `ledger_entries`.

`idempotency_records` uses the composite primary key `(scope, key)`. Its `resource_id` can identify either a wallet or a financial transaction, so the current schema intentionally does not define a foreign key for it. There is no local users table; `wallets.owner_id` stores the externally managed user UUID and has a unique constraint.

## 4. Successful Transfer Sequence

```mermaid
sequenceDiagram
    autonumber
    actor Client
    participant API as HTTP API Handler
    participant Service as application.Service
    participant DBTx as PostgreSQL Repository and Transaction
    participant History as financial_transactions and ledger_entries

    Client->>API: POST /v1/transfers<br/>X-User-ID and Idempotency-Key
    API->>API: Decode JSON and validate IDs, headers, and amount
    API->>Service: Transfer(ctx, command)
    Service->>Service: Validate owner, different wallet IDs, and idempotency
    Service->>DBTx: WithinTransaction(ctx): BEGIN READ COMMITTED
    DBTx->>DBTx: Reserve idempotency record
    Service->>DBTx: SELECT both wallets FOR UPDATE<br/>in sorted UUID order
    DBTx-->>Service: Locked source and destination wallets
    Service->>Service: Authorize source and validate currency and funds<br/>Debit source and credit destination
    Service->>DBTx: CreateFinancialTransaction
    DBTx->>History: INSERT financial_transactions
    Service->>DBTx: Update source and destination wallets
    DBTx->>DBTx: UPDATE wallets with new balances
    Service->>DBTx: Create debit and credit ledger entries
    DBTx->>History: INSERT two ledger_entries
    Service-->>DBTx: Transaction callback succeeds
    DBTx->>DBTx: COMMIT
    DBTx-->>Service: WithinTransaction returns
    Service-->>API: OperationResult
    API-->>Client: 201 Created with operation JSON
```

The idempotency record, both balance changes, the financial transaction, and both ledger entries commit or roll back together. Stable wallet-lock ordering prevents the common opposing-transfer deadlock cycle; retryable PostgreSQL deadlock and serialization errors are retried up to three times.

## 5. Package and Layer Dependencies

```mermaid
flowchart TB
    Server[cmd/server<br/>composition root]
    Migrator[cmd/migrate<br/>migration entry point]
    OpenAPICommand[cmd/openapi<br/>contract generator]
    Config[internal/config]
    Handler[internal/interfaces/httpapi<br/>handler, DTOs, middleware]
    Service[internal/application<br/>service and persistence ports]
    Repository[internal/infrastructure/postgres<br/>store, repository, migrations]
    Domain[internal/domain<br/>wallet, money, transaction, errors]
    Platform[internal/platform<br/>clock and identity]
    APIDocs[api<br/>OpenAPI contract and embedded Swagger UI]
    Database[(PostgreSQL)]

    Server --> Config
    Server --> Handler
    Server --> Service
    Server --> Repository
    Server --> Platform
    Migrator --> Config
    Migrator --> Repository
    OpenAPICommand -.->|validates and generates OpenAPI JSON| APIDocs

    Handler --> Service
    Handler --> Domain
    Handler --> Platform
    Handler --> APIDocs
    Service --> Domain
    Service --> Platform
    Repository --> Service
    Repository --> Domain
    Repository -.->|SQL through pgx/v5| Database
```

Solid arrows show compile-time package dependencies. Dashed arrows show build-time OpenAPI generation and the runtime database connection. The PostgreSQL package depends on `internal/application` because it implements the `Store` and `TransactionStore` ports declared there; the application package does not import the PostgreSQL adapter.

## 6. Local Development Deployment

```mermaid
flowchart LR
    Client[Client or Browser]

    subgraph DockerHost[Docker Host]
        subgraph Compose[Docker Compose Network]
            API[api container<br/>wallet-service<br/>port 8080]
            Migrate[migrate container<br/>wallet-migrate<br/>one-shot process]
            PostgreSQL[(postgres container<br/>PostgreSQL 17<br/>port 5432)]
        end

        Volume[(wallet-postgres-data<br/>named volume)]
    end

    Client -->|HTTP localhost:8080<br/>REST API and Swagger UI| API
    API -->|pgx connection<br/>postgres:5432| PostgreSQL
    Migrate -->|Apply embedded SQL migrations| PostgreSQL
    PostgreSQL -->|Persist database files| Volume
    PostgreSQL -.->|service healthy| Migrate
    Migrate -.->|completed successfully| API
```

Docker Compose starts PostgreSQL, runs the migration image after the database becomes healthy, and starts the API only after migration completion. The API and migration executables are built from the same project image, while PostgreSQL data persists in the named volume.
