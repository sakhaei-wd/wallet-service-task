# Architecture and consistency model

## Boundaries

The service follows Clean Architecture with a small composition root:

```text
HTTP adapter → application use cases → domain
                         ↓
                 persistence ports
                         ↑
                PostgreSQL adapter
```

The domain owns arithmetic and wallet invariants. The application layer owns multi-aggregate orchestration and transaction boundaries. PostgreSQL owns durable atomicity, row serialization, and final integrity constraints. HTTP concerns never enter the domain.

## Deposit and withdrawal

```text
BEGIN
  reserve idempotency key
  SELECT wallet FOR UPDATE
  validate status, currency, and funds
  insert financial_transactions
  update wallets
  insert ledger_entries
COMMIT
```

The row lock is acquired before reading the balance used by the domain. A preflight balance read outside this transaction would be advisory only and is deliberately not used for correctness.

## Transfer

```text
BEGIN
  reserve idempotency key
  sort [source wallet ID, destination wallet ID]
  SELECT first wallet FOR UPDATE
  SELECT second wallet FOR UPDATE
  validate both wallets
  insert one transfer transaction
  debit source and credit destination
  insert the source debit and destination credit ledger entries
COMMIT
```

Sorting makes all transfers acquire shared wallet locks in the same order. This prevents the common `A → B` / `B → A` deadlock cycle. PostgreSQL deadlocks and serialization failures are still retried up to three times with bounded backoff.

## Invariants

The following must be true after every commit:

1. Every wallet balance is nonnegative.
2. A withdrawal debit does not exceed the locked balance.
3. Transfer debit and credit amounts are equal and have the same currency.
4. A transfer cannot address the same wallet twice.
5. Every financial transaction has its corresponding immutable wallet ledger effects.
6. A given idempotency key can affect balances no more than once within its caller scope.

The domain checks these rules for useful errors. Database checks independently reject negative balances, nonpositive amounts, invalid participants, and ledger arithmetic that does not reconcile with its before/after values.

## Isolation choice

`READ COMMITTED` plus explicit `FOR UPDATE` locks is used instead of making every transaction `SERIALIZABLE`. The lock protocol directly expresses the contested resources and avoids unnecessary serialization retries. The database constraint on `wallets.balance_minor` remains a final safety net.

## Ledger choice

`financial_transactions` records the business operation. `ledger_entries` records its immutable effect from each wallet's perspective, including the balance before and after the entry. `wallets.balance_minor` is the materialized operational balance, so balance reads do not require replaying an unbounded history.

Deposits and withdrawals currently represent external money entering or leaving the service boundary. If the service assumes settlement/accounting responsibility, internal clearing accounts should be introduced so those operations become full double-entry postings. That extension does not require changing the public REST contract.

## Idempotency

Mutation requests reserve `(scope, key)` inside the same database transaction as their resource. A SHA-256 request fingerprint prevents a key from being reused with different inputs. Concurrent requests using the same key are resolved by the primary key on `idempotency_records`; the loser reads and returns the already committed resource.

The demo obtains `scope` from `X-Client-ID`, defaulting to `public`. A production authentication adapter must derive it from the authenticated principal so callers cannot choose another caller's namespace.

## Scaling path

- Scale stateless API instances horizontally behind a load balancer.
- Use a transactional outbox for events and notifications.
- Direct history reads to replicas when replica lag is acceptable.
- Partition ledger entries only after their size or index maintenance requires it.
- Monitor lock wait duration and treat high-contention wallets as a distinct workload.
- Avoid sharding the write ledger until required; cross-shard transfers need distributed coordination and a materially different failure model.
