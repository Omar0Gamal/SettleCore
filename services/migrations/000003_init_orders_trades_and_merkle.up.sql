CREATE TABLE engine_checkpoints (
    market_id            VARCHAR(16) PRIMARY KEY CHECK (market_id IN ('ETH/USDC', 'BTC/USDC')),
    last_settled_wal_seq BIGINT NOT NULL DEFAULT 0 CHECK (last_settled_wal_seq >= 0),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE orders (
    order_id        BIGINT PRIMARY KEY,
    idempotency_key UUID NOT NULL UNIQUE,
    user_id         UUID NOT NULL,
    market_id       VARCHAR(16) NOT NULL CHECK (market_id IN ('ETH/USDC', 'BTC/USDC')),
    side            VARCHAR(4) NOT NULL CHECK (side IN ('BUY', 'SELL')),
    order_type      VARCHAR(8) NOT NULL CHECK (order_type IN ('LIMIT', 'MARKET')),
    time_in_force   VARCHAR(4) NOT NULL CHECK (time_in_force IN ('GTC', 'IOC', 'FOK')),
    price           NUMERIC(38, 18) NOT NULL CHECK (price >= 0),
    quantity        NUMERIC(38, 18) NOT NULL CHECK (quantity > 0),
    filled_quantity NUMERIC(38, 18) NOT NULL DEFAULT 0 CHECK (filled_quantity >= 0 AND filled_quantity <= quantity),
    locked_amount   NUMERIC(38, 18) NOT NULL CHECK (locked_amount >= 0),
    wal_seq         BIGINT,
    status          VARCHAR(24) NOT NULL CHECK (
        status IN (
            'RESERVED',
            'OPEN',
            'PARTIALLY_FILLED',
            'FILLED',
            'CANCELED',
            'REJECTED_RELEASED',
            'ABORTED_UNSUBMITTED'
        )
    ),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_orders_user_active
    ON orders (user_id, market_id, created_at DESC)
    WHERE status IN ('RESERVED', 'OPEN', 'PARTIALLY_FILLED');

CREATE TABLE trades (
    trade_id       BIGINT PRIMARY KEY,
    wal_seq        BIGINT NOT NULL UNIQUE,
    market_id      VARCHAR(16) NOT NULL CHECK (market_id IN ('ETH/USDC', 'BTC/USDC')),
    maker_order_id BIGINT NOT NULL REFERENCES orders (order_id),
    taker_order_id BIGINT NOT NULL REFERENCES orders (order_id),
    maker_user_id  UUID NOT NULL,
    taker_user_id  UUID NOT NULL,
    price          NUMERIC(38, 18) NOT NULL CHECK (price > 0),
    quantity       NUMERIC(38, 18) NOT NULL CHECK (quantity > 0),
    quote_notional NUMERIC(38, 18) NOT NULL CHECK (quote_notional > 0),
    maker_fee      NUMERIC(38, 18) NOT NULL DEFAULT 0 CHECK (maker_fee >= 0),
    taker_fee      NUMERIC(38, 18) NOT NULL DEFAULT 0 CHECK (taker_fee >= 0),
    executed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_trades_market_executed ON trades (market_id, executed_at DESC);

CREATE TABLE merkle_snapshots (
    snapshot_id            UUID PRIMARY KEY,
    merkle_root            CHAR(66) NOT NULL UNIQUE,
    evm_block_number       BIGINT NOT NULL,
    publish_tx_hash        CHAR(66),
    total_users            INTEGER NOT NULL CHECK (total_users >= 0),
    asset_liabilities      JSONB NOT NULL,
    onchain_vault_balances JSONB NOT NULL,
    invariant_ok           BOOLEAN NOT NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
