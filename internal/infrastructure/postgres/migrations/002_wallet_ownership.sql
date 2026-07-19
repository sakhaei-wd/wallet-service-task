ALTER TABLE wallets ADD COLUMN owner_id UUID;

-- Legacy wallets predate ownership. Assigning their wallet UUID as a synthetic
-- owner preserves every row and guarantees uniqueness during this migration.
UPDATE wallets SET owner_id = id WHERE owner_id IS NULL;

ALTER TABLE wallets ALTER COLUMN owner_id SET NOT NULL;
ALTER TABLE wallets ADD CONSTRAINT wallets_owner_id_key UNIQUE (owner_id);
