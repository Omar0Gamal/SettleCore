-- name: GetEngineCheckpointForUpdate :one
SELECT market_id, last_settled_wal_seq, updated_at
FROM engine_checkpoints
WHERE market_id = $1
FOR UPDATE;

-- name: UpsertEngineCheckpoint :one
INSERT INTO engine_checkpoints (market_id, last_settled_wal_seq, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (market_id) DO UPDATE
SET last_settled_wal_seq = EXCLUDED.last_settled_wal_seq,
    updated_at = NOW()
RETURNING market_id, last_settled_wal_seq, updated_at;

-- name: InsertOrder :one
INSERT INTO orders (
    order_id, idempotency_key, user_id, market_id, side, order_type,
    time_in_force, price, quantity, filled_quantity, locked_amount, wal_seq, status
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING order_id, idempotency_key, user_id, market_id, side, order_type,
          time_in_force, price, quantity, filled_quantity, locked_amount, wal_seq, status, created_at, updated_at;

-- name: GetOrderByID :one
SELECT order_id, idempotency_key, user_id, market_id, side, order_type,
       time_in_force, price, quantity, filled_quantity, locked_amount, wal_seq, status, created_at, updated_at
FROM orders
WHERE order_id = $1;

-- name: GetOrderByIdempotencyKey :one
SELECT order_id, idempotency_key, user_id, market_id, side, order_type,
       time_in_force, price, quantity, filled_quantity, locked_amount, wal_seq, status, created_at, updated_at
FROM orders
WHERE idempotency_key = $1;

-- name: ListOrdersByUser :many
SELECT order_id, idempotency_key, user_id, market_id, side, order_type,
       time_in_force, price, quantity, filled_quantity, locked_amount, wal_seq, status, created_at, updated_at
FROM orders
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: ListReservedOrders :many
SELECT order_id, idempotency_key, user_id, market_id, side, order_type,
       time_in_force, price, quantity, filled_quantity, locked_amount, wal_seq, status, created_at, updated_at
FROM orders
WHERE status = 'RESERVED'
ORDER BY created_at ASC;

-- name: UpdateOrderExec :one
UPDATE orders
SET filled_quantity = $2,
    locked_amount = $3,
    wal_seq = COALESCE($4, wal_seq),
    status = $5,
    updated_at = NOW()
WHERE order_id = $1
RETURNING order_id, idempotency_key, user_id, market_id, side, order_type,
          time_in_force, price, quantity, filled_quantity, locked_amount, wal_seq, status, created_at, updated_at;

-- name: InsertTrade :one
INSERT INTO trades (
    trade_id, wal_seq, market_id, maker_order_id, taker_order_id,
    maker_user_id, taker_user_id, price, quantity, quote_notional, maker_fee, taker_fee
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (wal_seq) DO NOTHING
RETURNING trade_id, wal_seq, market_id, maker_order_id, taker_order_id,
          maker_user_id, taker_user_id, price, quantity, quote_notional, maker_fee, taker_fee, executed_at;

-- name: ListRecentTradesByMarket :many
SELECT trade_id, wal_seq, market_id, maker_order_id, taker_order_id,
       maker_user_id, taker_user_id, price, quantity, quote_notional, maker_fee, taker_fee, executed_at
FROM trades
WHERE market_id = $1
ORDER BY executed_at DESC
LIMIT $2;

-- name: InsertMerkleSnapshot :one
INSERT INTO merkle_snapshots (
    snapshot_id, merkle_root, evm_block_number, publish_tx_hash,
    total_users, asset_liabilities, onchain_vault_balances, invariant_ok
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING snapshot_id, merkle_root, evm_block_number, publish_tx_hash,
          total_users, asset_liabilities, onchain_vault_balances, invariant_ok, created_at;

-- name: GetLatestMerkleSnapshot :one
SELECT snapshot_id, merkle_root, evm_block_number, publish_tx_hash,
       total_users, asset_liabilities, onchain_vault_balances, invariant_ok, created_at
FROM merkle_snapshots
ORDER BY created_at DESC
LIMIT 1;
