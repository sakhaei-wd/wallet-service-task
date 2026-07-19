CREATE TABLE IF NOT EXISTS wallets (
    id UUID PRIMARY KEY,
    currency VARCHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    balance_minor BIGINT NOT NULL DEFAULT 0 CHECK (balance_minor >= 0),
    status VARCHAR(16) NOT NULL CHECK (status IN ('ACTIVE', 'FROZEN', 'CLOSED')),
    version BIGINT NOT NULL CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS financial_transactions (
    id UUID PRIMARY KEY,
    type VARCHAR(16) NOT NULL CHECK (type IN ('DEPOSIT', 'WITHDRAWAL', 'TRANSFER')),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency VARCHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    source_wallet_id UUID REFERENCES wallets(id),
    destination_wallet_id UUID REFERENCES wallets(id),
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT valid_transaction_participants CHECK (
        (type = 'DEPOSIT' AND source_wallet_id IS NULL AND destination_wallet_id IS NOT NULL) OR
        (type = 'WITHDRAWAL' AND source_wallet_id IS NOT NULL AND destination_wallet_id IS NULL) OR
        (type = 'TRANSFER' AND source_wallet_id IS NOT NULL AND destination_wallet_id IS NOT NULL AND source_wallet_id <> destination_wallet_id)
    )
);

CREATE TABLE IF NOT EXISTS ledger_entries (
    sequence BIGSERIAL PRIMARY KEY,
    transaction_id UUID NOT NULL REFERENCES financial_transactions(id),
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    direction VARCHAR(8) NOT NULL CHECK (direction IN ('DEBIT', 'CREDIT')),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    balance_before_minor BIGINT NOT NULL CHECK (balance_before_minor >= 0),
    balance_after_minor BIGINT NOT NULL CHECK (balance_after_minor >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (transaction_id, wallet_id),
    CONSTRAINT valid_entry_balance CHECK (
        (direction = 'DEBIT' AND balance_after_minor = balance_before_minor - amount_minor) OR
        (direction = 'CREDIT' AND balance_after_minor = balance_before_minor + amount_minor)
    )
);

CREATE TABLE IF NOT EXISTS idempotency_records (
    scope VARCHAR(128) NOT NULL,
    key VARCHAR(128) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    resource_type VARCHAR(16) NOT NULL CHECK (resource_type IN ('WALLET', 'TRANSACTION')),
    resource_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope, key)
);

CREATE INDEX IF NOT EXISTS idx_ledger_entries_wallet_history
    ON ledger_entries (wallet_id, sequence DESC);
CREATE INDEX IF NOT EXISTS idx_ledger_entries_transaction
    ON ledger_entries (transaction_id);
CREATE INDEX IF NOT EXISTS idx_transactions_source_created
    ON financial_transactions (source_wallet_id, created_at DESC)
    WHERE source_wallet_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_transactions_destination_created
    ON financial_transactions (destination_wallet_id, created_at DESC)
    WHERE destination_wallet_id IS NOT NULL;
