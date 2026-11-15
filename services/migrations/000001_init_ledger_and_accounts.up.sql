CREATE TABLE accounts (
    account_id   UUID PRIMARY KEY,
    user_id      UUID NOT NULL,
    asset        VARCHAR(16) NOT NULL CHECK (asset IN ('ETH', 'USDC', 'WBTC', 'USDT')),
    account_type VARCHAR(32) NOT NULL CHECK (account_type IN ('USER', 'SYSTEM_ESCROW_VAULT', 'SYSTEM_FEE_TREASURY')),
    available    NUMERIC(38, 18) NOT NULL DEFAULT 0,
    locked       NUMERIC(38, 18) NOT NULL DEFAULT 0,
    version      BIGINT NOT NULL DEFAULT 1,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_accounts_user_asset UNIQUE (user_id, asset, account_type),
    CONSTRAINT chk_accounts_non_negative_user CHECK (
        account_type = 'SYSTEM_ESCROW_VAULT' OR (available >= 0 AND locked >= 0)
    )
);

CREATE INDEX idx_accounts_user_id ON accounts (user_id);

CREATE TABLE ledger_entries (
    entry_id       BIGSERIAL PRIMARY KEY,
    journal_tx_id  UUID NOT NULL,
    account_id     UUID NOT NULL REFERENCES accounts (account_id),
    asset          VARCHAR(16) NOT NULL CHECK (asset IN ('ETH', 'USDC', 'WBTC', 'USDT')),
    entry_type     VARCHAR(32) NOT NULL CHECK (
        entry_type IN (
            'DEPOSIT',
            'ORDER_RESERVE',
            'TRADE_SETTLE',
            'FEE',
            'CANCEL_RELEASE',
            'WITHDRAW_LOCK',
            'WITHDRAW_FINAL',
            'REORG_REVERSAL'
        )
    ),
    direction      VARCHAR(8) NOT NULL CHECK (direction IN ('DEBIT', 'CREDIT')),
    amount         NUMERIC(38, 18) NOT NULL CHECK (amount > 0),
    balance_after  NUMERIC(38, 18) NOT NULL,
    reference_id   VARCHAR(128) NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ledger_entries_journal_tx ON ledger_entries (journal_tx_id);
CREATE INDEX idx_ledger_entries_account_created ON ledger_entries (account_id, created_at DESC);
CREATE INDEX idx_ledger_entries_reference ON ledger_entries (reference_id);

-- Upgrade E: Enforce append-only immutability on ledger_entries at the database layer.
CREATE OR REPLACE FUNCTION fn_ledger_entries_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is append-only and immutable';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_ledger_entries_immutable
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW
    EXECUTE FUNCTION fn_ledger_entries_immutable();

-- Upgrade E: Enforce double-entry conservation per journal_tx_id and asset at commit.
CREATE OR REPLACE FUNCTION fn_ledger_entries_balanced()
RETURNS TRIGGER AS $$
DECLARE
    net_balance NUMERIC(38, 18);
BEGIN
    SELECT COALESCE(SUM(CASE WHEN direction = 'CREDIT' THEN amount ELSE -amount END), 0)
    INTO net_balance
    FROM ledger_entries
    WHERE journal_tx_id = NEW.journal_tx_id
      AND asset = NEW.asset;

    IF net_balance <> 0 THEN
        RAISE EXCEPTION 'double-entry journal_tx_id % for asset % is unbalanced: net=%',
            NEW.journal_tx_id, NEW.asset, net_balance;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_ledger_entries_balanced
    AFTER INSERT ON ledger_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION fn_ledger_entries_balanced();
