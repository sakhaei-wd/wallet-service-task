# Wallet Service Design

## Purpose and scope

This document explains the major design decisions behind the wallet service, how the architecture should evolve at several million financial operations per day, and the most important limitation of the current implementation.

The service is currently designed as a strongly consistent modular monolith backed by PostgreSQL. It supports wallet creation, deposits, withdrawals, transfers, balance reads, and transaction history. The primary correctness requirements are:

- An amount must be greater than zero.
- A wallet balance must never become negative.
- A transfer cannot use the same source and destination wallet.
- A transfer must debit and credit atomically.
- Every committed financial operation must have immutable history.
- Retrying a request must not apply the financial effect more than once.

Correctness is prioritized over availability during database failure. If the service cannot prove that an operation committed, it returns an error and relies on idempotent client retry rather than guessing or applying an eventually consistent balance change.

## 1. Major design decisions

### 1.1 Modular monolith with Clean Architecture

The service is implemented as one deployable application with distinct domain, application, HTTP, and PostgreSQL boundaries.

```text
HTTP adapter
    ↓
Application use cases
    ↓
Domain rules
    ↓
Persistence ports
    ↓
PostgreSQL adapter
```

The domain has no dependency on HTTP, PostgreSQL, or `pgx`. The application layer coordinates use cases through persistence interfaces. The server entry point explicitly injects the PostgreSQL store, clock, ID generator, service, and HTTP handlers.

Wallet ownership is explicit: every wallet stores a non-null external `owner_id`, and a unique constraint enforces at most one wallet per owner. The application authorizes wallet reads and mutations against that owner. Transfers require ownership of the source wallet while allowing a destination owned by another user. The assignment's `X-User-ID` header represents a principal asserted by a trusted upstream; it is an integration seam, not credential authentication.

Why this was chosen:

- Deposits, withdrawals, transfers, balances, and history share one strong transaction boundary.
- Business rules can be tested without HTTP or a database.
- PostgreSQL behavior remains isolated behind application-owned interfaces.
- One deployment is easier to operate and reason about than several distributed services.
- Package boundaries preserve the option to extract components later without paying the distributed-systems cost now.

Tradeoffs:

- All write capabilities scale and deploy together.
- A defect or resource leak in one module can affect the whole process.
- The single application and database form a larger failure domain than independently deployed services.

Those costs are acceptable at the current scope. Splitting each operation into a separate microservice would make transfers require distributed coordination while providing little practical benefit.

### 1.2 PostgreSQL is the consistency boundary

Each financial operation executes in one PostgreSQL transaction. Balance changes, the business transaction, ledger entries, and the idempotency record either all commit or all roll back.

For a transfer, the transaction is conceptually:

```text
BEGIN
  reserve idempotency key
  lock source and destination wallets
  validate both wallets and available funds
  insert financial transaction
  update both balances
  insert debit and credit ledger entries
COMMIT
```

Why this was chosen:

- PostgreSQL already provides durable ACID transactions, row locks, constraints, crash recovery, and replication.
- There is no application-managed compensation path for partial transfers.
- The database can enforce final safeguards even if an application bug bypasses a domain check.

Tradeoffs:

- Write availability is tied to the writable PostgreSQL primary.
- Cross-database or cross-shard transfers are not supported by this transaction model.
- Long transactions or excessive connection counts can reduce throughput.

The transaction is deliberately short: it does not perform network calls, publish events, or wait for external systems while holding wallet locks.

### 1.3 Materialized wallet balance plus immutable ledger entries

The `wallets` table stores the current operational balance. `financial_transactions` records the business operation, while `ledger_entries` records the effect from each wallet's perspective, including balance before and after the entry.

Why this was chosen:

- Balance reads are constant-time and do not replay an unbounded history.
- A wallet row is a natural concurrency-control point.
- Immutable history supports audit, debugging, statements, and reconciliation.
- Balance-before and balance-after fields make a wallet's progression inspectable.

Tradeoffs:

- The balance is duplicated state that must remain consistent with the ledger.
- Correctness depends on all writers using the same transaction path.
- Reconciliation is necessary in a mature financial system even when the transaction design is correct.

The schema uses constraints to prevent negative balances and mathematically invalid ledger entries. Historical entries are never edited; corrections should eventually be represented as reversals or compensating transactions.

### 1.4 Integer minor units for money

Amounts are accepted as decimal strings and converted to signed 64-bit minor units. Floating-point types are never used for financial arithmetic.

Examples:

- `"10.25" USD` becomes `1025` minor units.
- `"100" JPY` becomes `100` minor units.

Why this was chosen:

- Integer arithmetic is exact and easy to validate.
- PostgreSQL `BIGINT` constraints and comparisons are straightforward.
- Overflow can be detected explicitly before a balance update.

Tradeoffs:

- The service must know the fractional exponent of every supported currency.
- A signed 64-bit range is finite and may be unsuitable for extremely large aggregate or settlement accounts.
- Supporting unusual assets or changing currency metadata requires a controlled currency registry rather than a hard-coded map.

The current currency set is deliberately limited to USD, EUR, GBP, and JPY.

### 1.5 Pessimistic row locking

Deposits and withdrawals lock the wallet row with `SELECT ... FOR UPDATE`. Transfers lock both wallet rows before reading or modifying their balances.

Why this was chosen:

- Concurrent withdrawals cannot both spend the same balance.
- The balance validated by the domain is the locked, current balance.
- Contention is resolved by waiting rather than repeated optimistic failures.
- The model is straightforward to explain and verify.

Transfers normalize UUIDs and acquire locks in ascending order. Deterministic ordering prevents the common deadlock in which `A → B` locks A first while `B → A` locks B first.

Tradeoffs:

- Operations on one wallet are serialized, so a single hot wallet has a hard throughput limit.
- Lock waits increase tail latency under contention.
- Every code path that mutates a wallet must obey the same lock protocol.

This serialization is not accidental overhead: two operations cannot safely update one spendable balance simultaneously. A different architecture may move the serialization point, but it cannot remove the underlying ordering requirement.

### 1.6 `READ COMMITTED` with explicit locks

The service uses PostgreSQL `READ COMMITTED` isolation and explicit row locks rather than making every operation `SERIALIZABLE`.

Why this was chosen:

- The contested resources are known wallet rows, so the locking requirement can be expressed directly.
- It avoids unnecessary serialization failures for unrelated wallets.
- The behavior is easier to reason about than relying on implicit predicate conflict detection.

Deadlocks and serialization failures are still treated as transient and retried up to three times with bounded backoff.

Tradeoffs:

- Correctness depends on disciplined locking rather than the isolation level detecting every anomaly.
- Future queries that introduce range-based invariants may require stronger isolation or additional locks.

### 1.7 Idempotency is part of the database transaction

Every mutation requires an idempotency key. The database uniquely identifies a request by `(scope, key)`, where `scope` is derived internally from the normalized owner UUID, and stores a SHA-256 fingerprint of the normalized request plus the created resource ID.

Behavior:

- Same key and same request: return the existing wallet or transaction without applying another balance change.
- Same key and different request: return `409 Conflict`.
- Concurrent requests with the same key: PostgreSQL permits one reservation; the others read the committed resource.

Why this was chosen:

- Clients can safely retry after timeouts and ambiguous network failures.
- Idempotency is atomic with the financial effect.
- It works across multiple stateless API instances.

Tradeoffs:

- Idempotency records grow indefinitely in the current design.
- The owner principal must come from verified identity in production.
- A retention policy must be longer than the maximum legitimate retry window and aligned with audit requirements.

The current `X-User-ID` contract is an assignment-level trusted-upstream identity assertion, not an authentication mechanism. A public deployment must remove or overwrite caller-supplied values after validating credentials.

### 1.8 Explicit SQL with `pgx`

The persistence adapter uses `pgx` and handwritten SQL instead of an ORM.

Why this was chosen:

- Transaction boundaries and row locks remain visible.
- SQL constraints, indexes, query plans, and PostgreSQL-specific behavior are not hidden by an abstraction.
- The schema is small enough that ORM convenience would not outweigh the loss of control.

Tradeoffs:

- Mapping and query code is more verbose.
- Schema changes require repository updates and integration tests.
- Compile-time SQL validation would require adding `sqlc` or an equivalent generation step.

`sqlc` would be a reasonable future improvement if the query surface grows. It would not change the transaction or locking design.

### 1.9 Cursor-based history pagination

Wallet history is ordered by a monotonically increasing ledger sequence. Clients receive an opaque cursor and request entries before that sequence.

Why this was chosen:

- Results remain stable as new transactions arrive.
- Queries can use the `(wallet_id, sequence DESC)` index.
- Performance does not degrade with an increasingly large offset.

Tradeoffs:

- Clients cannot jump directly to an arbitrary page number.
- Cursor formats must remain backward compatible.
- Sequence-based pagination provides ordering, not an externally meaningful timestamp.

### 1.10 REST API and structured failures

The API uses resource-oriented endpoints, decimal-string amounts, proper success codes, `Location` headers for new resources, and RFC 9457-style `application/problem+json` responses.

Why this was chosen:

- Clients receive stable error codes rather than parsing human-readable messages.
- Separate deposit, withdrawal, and transfer endpoints are easier to authorize and observe than one polymorphic command endpoint.
- Request IDs connect responses to structured logs.

Tradeoffs:

- Adding new operation types introduces new endpoints or expands the transaction model.
- Versioning and compatibility must be managed as the API evolves.

### 1.11 Operational behavior is explicit

The service includes:

- Environment-based configuration with strict parsing
- Bounded PostgreSQL connection pooling
- Request, read, write, idle, and graceful-shutdown timeouts
- Structured JSON access and error logging
- Liveness and database-backed readiness endpoints
- A separate migration executable with an advisory lock
- Docker Compose for local operation
- PostgreSQL-backed CI integration tests

The migration process is separate in production because schema rollout and application rollout have different safety requirements. Automatic startup migration remains available for local development only.

## 2. Architecture at several million transactions per day

### 2.1 Start with capacity math, not an automatic rewrite

Several million operations per day sounds large, but the daily number alone does not justify sharding or microservices:

| Daily operations | Average operations/second |
|---:|---:|
| 1 million | approximately 12 |
| 5 million | approximately 58 |
| 10 million | approximately 116 |

Real traffic is not uniform. A system averaging 58 operations per second may see peaks of 500–1,500 operations per second, and one popular wallet may receive a disproportionate share. Transfers also create two ledger entries, so five million business operations can generate between five and ten million wallet ledger rows per day.

A well-sized PostgreSQL primary can handle this class of workload when transactions are short, indexes are controlled, storage is fast, and connections are bounded. The first scaling work should therefore preserve the current consistency model and remove measured bottlenecks.

### 2.2 Horizontally scale the stateless API

The API instances are already stateless. Multiple replicas can run behind a load balancer without sticky sessions because balances, locks, transactions, and idempotency are stored in PostgreSQL.

Required changes:

- Size each instance's connection pool as part of a global database connection budget.
- Introduce PgBouncer if connection churn or replica count becomes significant.
- Add load shedding and bounded request queues rather than allowing unlimited lock waiters.
- Use autoscaling signals that include latency, lock wait time, and database saturation—not CPU alone.
- Make clients retry only transient failures and require idempotency on every retry.

Adding API replicas improves request handling and availability, but it does not increase the per-wallet write rate. PostgreSQL row locks remain the serialization point.

### 2.3 Separate synchronous correctness from asynchronous work

The balance transaction should continue to contain only the minimum authoritative writes. Notifications, analytics, webhook delivery, search indexing, statement generation, and downstream events should not run while wallet locks are held.

Add a transactional outbox:

```text
wallet transaction
  ├── balance and ledger writes
  └── outbox event
          ↓ after commit
     background publisher
          ↓
     broker and consumers
```

The outbox row commits with the financial transaction. A worker publishes it at least once, and consumers deduplicate by event ID.

Tradeoff:

- Side effects become eventually consistent.
- Consumers must be idempotent.
- The core balance remains strongly consistent and does not depend on broker availability.

### 2.4 Partition high-growth tables

At five million operations per day, `financial_transactions`, `ledger_entries`, idempotency records, and outbox rows become the dominant storage and maintenance concern. Even when throughput is manageable, billions of historical rows affect indexes, vacuuming, backups, and retention.

Recommended evolution:

- Keep the small, frequently updated `wallets` table unpartitioned initially.
- Time-partition immutable transaction and ledger data, usually monthly or weekly depending on volume.
- Ensure history queries can prune partitions using a time range when possible.
- Retain the wallet/sequence index needed for cursor pagination.
- Move expired idempotency records to time partitions with an explicit retention policy.
- Archive cold history to lower-cost storage while keeping auditable retrieval available.
- Use online index creation and expand/contract migrations.

Partitioning has operational cost. Too many partitions increase planning and maintenance overhead, while time partitioning alone does not solve a query that searches one wallet across its entire lifetime. Partition size and indexes must be chosen from observed query plans.

### 2.5 Split the read path without splitting the authoritative write path

Transaction history is an append-heavy read workload and can be scaled independently from balance mutations.

Options, in increasing order of complexity:

1. Optimize and partition history tables on the primary.
2. Serve older history from PostgreSQL read replicas.
3. Build an asynchronous wallet-history read model from outbox events.
4. Store cold statement data in an analytical or archival system.

Balance reads immediately following a mutation should remain on the primary unless the API explicitly tolerates replica lag. A client that deposits and then reads from a lagging replica must not see the old balance without a documented consistency contract.

A read model can be rebuilt from the authoritative ledger, but it must never become the source used to authorize a withdrawal.

### 2.6 Operate PostgreSQL as critical financial infrastructure

At this volume, database operations matter as much as application code:

- Use provisioned IOPS or equivalent low-latency durable storage.
- Monitor transaction latency, WAL generation, lock wait time, deadlocks, checkpoint pressure, vacuum lag, table/index growth, connection utilization, and replica lag.
- Tune autovacuum per high-write table rather than relying only on global defaults.
- Maintain point-in-time recovery, tested backups, and documented recovery objectives.
- Use an automated primary failover design and regularly test it.
- Consider a synchronous standby only after evaluating its latency and availability cost.
- Run reconciliation continuously and alert on any balance/ledger discrepancy.

More indexes are not always better. Each index on a high-volume ledger increases WAL, storage, and insert cost. Every index should correspond to a measured query requirement.

### 2.7 Treat hot-wallet contention as a distinct problem

Aggregate throughput and per-wallet throughput are different constraints. Millions of operations distributed over millions of wallets are relatively easy. Thousands of operations per second against one merchant or system wallet are not.

The current row-lock model intentionally serializes changes to one balance. Possible responses include:

- Rate-limit or backpressure requests for a single wallet.
- Use dedicated internal accounts or balance buckets so unrelated flows do not contend on one row.
- Serialize hot-account commands through a partitioned command stream where one consumer owns an account partition.
- Use atomic conditional SQL updates for simple debit operations to reduce round trips, while retaining ledger atomicity.
- Move high-volume aggregation out of a single end-user wallet and settle aggregated positions periodically.

Optimistic locking is not automatically better for hot wallets. Under heavy contention it turns lock waiting into repeated transaction retries and can consume more resources.

Queue-based serialization can improve control and smoothing, but it introduces queue lag and changes the API from immediate completion toward accepted/pending semantics. It is appropriate only if the product can expose that behavior.

### 2.8 Add accounting and reconciliation before extreme scale

Before increasing volume materially, the ledger should evolve to full double-entry accounting:

- Represent wallet, clearing, settlement, fee, and suspense accounts explicitly.
- Require every journal transaction to have balanced postings per currency.
- Track external provider references and settlement state.
- Support pending, posted, reversed, and failed lifecycle states.
- Reconcile internal positions against banks and payment providers.
- Keep cached balances but verify them continuously from postings.

Scaling a simplified one-sided ledger only makes accounting gaps larger and more expensive to repair.

### 2.9 Do not shard until the primary is a demonstrated limit

Sharding by wallet ID can increase aggregate write capacity, but a transfer can involve wallets on different shards. A normal PostgreSQL transaction can no longer atomically lock and update both.

Cross-shard options all introduce significant tradeoffs:

- **Co-locate related wallets:** simple when relationships are predictable, but arbitrary transfers still cross shards.
- **Distributed transactions/two-phase commit:** preserves atomic semantics but adds blocking, coordinator recovery, and operational complexity.
- **Durable transfer coordinator with escrow:** moves value through explicit pending and settlement states; recoverable, but no longer a single immediate database transaction.
- **Globally distributed SQL database:** offers a familiar transaction model, but adds cost, latency, vendor constraints, and different failure behavior.
- **Ledger-first command platform:** assigns accounts to ordered partitions and coordinates cross-partition journals; powerful but effectively a specialized financial database.

The correct choice depends on product semantics, regional requirements, latency targets, and acceptable pending states. A naïve saga that debits one shard and later credits another is not adequate for money: compensation can fail, and users can observe inconsistent funds.

### 2.10 Likely service decomposition

At several million daily transactions, service boundaries may become useful for team ownership and workload isolation, but the authoritative ledger should still have one owner.

A realistic decomposition is:

```text
Wallet API / command service
        ↓
Authoritative ledger and balance store
        ↓ transactional outbox
Event broker
  ├── history/read-model service
  ├── notifications/webhooks
  ├── reconciliation and settlement
  ├── statements/reporting
  └── risk and analytics
```

This is not a deposit microservice, withdrawal microservice, and transfer microservice. Those commands all mutate the same accounting state and should not have independent write ownership.

## 3. Biggest limitation of the current design

### The current ledger is not a complete double-entry accounting system

For a production system that holds or represents real funds, this is more important than the current single-database scalability limit.

Transfers create a debit entry for one wallet and an equal credit entry for another, so value is conserved within the service. Deposits and withdrawals, however, create only the user-wallet side of the movement. There is no internal clearing, bank, settlement, fee, or suspense account representing the other side.

Consequences:

- The system cannot prove globally that total debits equal total credits for every currency.
- A deposit endpoint creates wallet value without proving that external funds settled.
- A withdrawal removes wallet value without modeling whether an external payout succeeded.
- There is no distinction between pending, available, and settled funds.
- There is no first-class reversal or compensating-journal workflow.
- The materialized balance and ledger are transactionally updated, but there is no continuous reconciliation process to detect corruption, manual database changes, or historical defects.
- Financial reporting and external settlement reconciliation cannot be derived rigorously from the current schema alone.

This is acceptable for the stated wallet-service assignment, where deposit and withdrawal are defined as trusted commands at the system boundary. It is not sufficient for a regulated wallet, payment processor, or stored-value product.

### Recommended correction

Introduce a general journal model:

```text
accounts
  - wallet accounts
  - bank/clearing accounts
  - settlement accounts
  - fee accounts
  - suspense accounts

journal_transactions
  - business reference
  - currency
  - pending/posted/reversed state
  - external settlement reference

postings
  - journal transaction
  - account
  - debit or credit
  - amount
```

Every posted journal must balance per currency:

```text
sum(debits) = sum(credits)
```

Examples:

- Deposit: debit external-clearing asset account, credit user-wallet liability account.
- Withdrawal: debit user-wallet liability account, credit payout-clearing account.
- Transfer: debit source-wallet liability, credit destination-wallet liability.
- Fee: debit user wallet, credit fee-revenue account.
- Reversal: create an equal and opposite journal; never edit the original posting.

The spendable balance can remain materialized for performance, but it should be a cache of balanced postings and continuously reconciled. External funding should move through pending and settled states based on provider evidence rather than trusting a public deposit command.

Tradeoffs of this correction:

- More tables, postings, states, and writes per operation
- More complex domain language and operational tooling
- Explicit treatment of pending funds, reversals, fees, and settlement failures
- More demanding migration and reconciliation requirements

Those costs are justified when the system represents real money because they make conservation, audit, recovery, and financial reporting explicit.

### Other important limitations

The absence of credential authentication is an immediate production launch blocker, even though it was outside the assignment scope. Ownership authorization is implemented, but a public client must not be trusted to assert `X-User-ID`; verified authentication middleware must supply the principal. Rate limiting, audit-event retention, metrics, tracing, alerting, disaster-recovery testing, and external settlement integration are also required before production use.

If the service remains a closed, trusted wallet simulator rather than a real-money system, then the biggest technical scaling limitation becomes the single writable PostgreSQL primary and the per-wallet row lock. At several million well-distributed daily operations, that limit is manageable. At extreme aggregate volume or extreme hot-wallet contention, it requires the staged changes described above rather than an immediate rewrite.

## Summary

The current design deliberately chooses strong consistency and operational simplicity:

- One authoritative PostgreSQL transaction per financial operation
- Exact integer money
- Explicit row locking
- Immutable history plus a materialized balance
- Database-backed idempotency
- A stateless, horizontally scalable API

Several million daily operations should first be addressed by capacity planning, API replication, database operations, table partitioning, asynchronous side effects, read scaling, and hot-wallet controls. Microservices and sharding should follow measured constraints, not transaction-count headlines.

For real funds, the most important architectural improvement is a balanced double-entry ledger with clearing accounts, settlement states, reversals, and continuous reconciliation. Without that, scaling the existing system would increase throughput without achieving complete financial correctness.
