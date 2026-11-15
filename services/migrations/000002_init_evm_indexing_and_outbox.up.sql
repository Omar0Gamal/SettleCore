CREATE TABLE indexed_blocks (
    block_number BIGINT NOT NULL,
    block_hash   CHAR(66) PRIMARY KEY,
    parent_hash  CHAR(66) NOT NULL,
    status       VARCHAR(16) NOT NULL DEFAULT 'CANONICAL' CHECK (status IN ('CANONICAL', 'ORPHANED')),
    indexed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uq_indexed_blocks_canonical_height
    ON indexed_blocks (block_number)
    WHERE status = 'CANONICAL';

CREATE TABLE onchain_deposits (
    deposit_id        UUID PRIMARY KEY,
    tx_hash           CHAR(66) NOT NULL,
    log_index         INTEGER NOT NULL CHECK (log_index >= 0),
    block_number      BIGINT NOT NULL,
    block_hash        CHAR(66) NOT NULL REFERENCES indexed_blocks (block_hash),
    depositor_address CHAR(42) NOT NULL,
    user_id           UUID NOT NULL,
    asset             VARCHAR(16) NOT NULL CHECK (asset IN ('ETH', 'USDC', 'WBTC', 'USDT')),
    raw_amount_wei    NUMERIC(78, 0) NOT NULL CHECK (raw_amount_wei > 0),
    credited_amount   NUMERIC(38, 18) NOT NULL CHECK (credited_amount > 0),
    status            VARCHAR(16) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'CONFIRMED', 'ORPHANED')),
    journal_tx_id     UUID,
    detected_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    confirmed_at      TIMESTAMPTZ,
    CONSTRAINT uq_onchain_deposits_tx_log_block UNIQUE (tx_hash, log_index, block_hash)
);

CREATE INDEX idx_onchain_deposits_pending_height
    ON onchain_deposits (block_number)
    WHERE status = 'PENDING';

CREATE TABLE withdrawal_outbox (
    withdrawal_id        UUID PRIMARY KEY,
    idempotency_key      UUID NOT NULL UNIQUE,
    user_id              UUID NOT NULL,
    recipient_address    CHAR(42) NOT NULL,
    asset                VARCHAR(16) NOT NULL CHECK (asset IN ('ETH', 'USDC', 'WBTC', 'USDT')),
    human_amount         NUMERIC(38, 18) NOT NULL CHECK (human_amount > 0),
    raw_amount_wei       NUMERIC(78, 0) NOT NULL CHECK (raw_amount_wei > 0),
    signing_tier         VARCHAR(16) NOT NULL CHECK (signing_tier IN ('HOT', 'COLD_COSIGNED')),
    voucher_nonce        BIGINT NOT NULL UNIQUE,
    relayer_eoa_nonce    BIGINT,
    last_tx_hash         CHAR(66),
    max_fee_per_gas_wei  NUMERIC(78, 0),
    max_priority_fee_wei NUMERIC(78, 0),
    gas_bump_count       INTEGER NOT NULL DEFAULT 0 CHECK (gas_bump_count >= 0),
    status               VARCHAR(24) NOT NULL DEFAULT 'QUEUED' CHECK (
        status IN ('QUEUED', 'SUBMITTED', 'CONFIRMED', 'FAILED_REFUNDED')
    ),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_withdrawal_outbox_poll
    ON withdrawal_outbox (status, created_at ASC)
    WHERE status IN ('QUEUED', 'SUBMITTED');
