-- name: InsertIndexedBlock :one
INSERT INTO indexed_blocks (block_number, block_hash, parent_hash, status)
VALUES ($1, $2, $3, 'CANONICAL')
ON CONFLICT (block_hash) DO UPDATE
SET status = 'CANONICAL'
RETURNING block_number, block_hash, parent_hash, status, indexed_at;

-- name: GetCanonicalBlockByNumber :one
SELECT block_number, block_hash, parent_hash, status, indexed_at
FROM indexed_blocks
WHERE block_number = $1 AND status = 'CANONICAL'
LIMIT 1;

-- name: GetLatestCanonicalBlock :one
SELECT block_number, block_hash, parent_hash, status, indexed_at
FROM indexed_blocks
WHERE status = 'CANONICAL'
ORDER BY block_number DESC
LIMIT 1;

-- name: MarkBlocksOrphanedAbove :many
UPDATE indexed_blocks
SET status = 'ORPHANED'
WHERE block_number > $1 AND status = 'CANONICAL'
RETURNING block_number, block_hash, parent_hash, status, indexed_at;

-- name: InsertOnchainDeposit :one
INSERT INTO onchain_deposits (
    deposit_id, tx_hash, log_index, block_number, block_hash, depositor_address,
    user_id, asset, raw_amount_wei, credited_amount, status
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'PENDING')
ON CONFLICT (tx_hash, log_index, block_hash) DO NOTHING
RETURNING deposit_id, tx_hash, log_index, block_number, block_hash, depositor_address,
          user_id, asset, raw_amount_wei, credited_amount, status, journal_tx_id, detected_at, confirmed_at;

-- name: ListPendingDepositsAtOrBelow :many
SELECT deposit_id, tx_hash, log_index, block_number, block_hash, depositor_address,
       user_id, asset, raw_amount_wei, credited_amount, status, journal_tx_id, detected_at, confirmed_at
FROM onchain_deposits
WHERE status = 'PENDING' AND block_number <= $1
ORDER BY block_number ASC, log_index ASC
FOR UPDATE;

-- name: ConfirmOnchainDeposit :one
UPDATE onchain_deposits
SET status = 'CONFIRMED',
    journal_tx_id = $2,
    confirmed_at = NOW()
WHERE deposit_id = $1 AND status = 'PENDING'
RETURNING deposit_id, tx_hash, log_index, block_number, block_hash, depositor_address,
          user_id, asset, raw_amount_wei, credited_amount, status, journal_tx_id, detected_at, confirmed_at;

-- name: MarkDepositsOrphanedAbove :many
UPDATE onchain_deposits
SET status = 'ORPHANED'
WHERE block_number > $1 AND status IN ('PENDING', 'CONFIRMED')
RETURNING deposit_id, tx_hash, log_index, block_number, block_hash, depositor_address,
          user_id, asset, raw_amount_wei, credited_amount, status, journal_tx_id, detected_at, confirmed_at;

-- name: ListRecentDepositsByUser :many
SELECT deposit_id, tx_hash, log_index, block_number, block_hash, depositor_address,
       user_id, asset, raw_amount_wei, credited_amount, status, journal_tx_id, detected_at, confirmed_at
FROM onchain_deposits
WHERE user_id = $1
ORDER BY detected_at DESC
LIMIT $2;

-- name: InsertWithdrawalOutbox :one
INSERT INTO withdrawal_outbox (
    withdrawal_id, idempotency_key, user_id, recipient_address, asset,
    human_amount, raw_amount_wei, signing_tier, voucher_nonce, status
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'QUEUED')
RETURNING withdrawal_id, idempotency_key, user_id, recipient_address, asset,
          human_amount, raw_amount_wei, signing_tier, voucher_nonce, relayer_eoa_nonce,
          last_tx_hash, max_fee_per_gas_wei, max_priority_fee_wei, gas_bump_count, status, created_at, updated_at;

-- name: GetWithdrawalByIdempotencyKey :one
SELECT withdrawal_id, idempotency_key, user_id, recipient_address, asset,
       human_amount, raw_amount_wei, signing_tier, voucher_nonce, relayer_eoa_nonce,
       last_tx_hash, max_fee_per_gas_wei, max_priority_fee_wei, gas_bump_count, status, created_at, updated_at
FROM withdrawal_outbox
WHERE idempotency_key = $1;

-- name: ClaimQueuedWithdrawals :many
SELECT withdrawal_id, idempotency_key, user_id, recipient_address, asset,
       human_amount, raw_amount_wei, signing_tier, voucher_nonce, relayer_eoa_nonce,
       last_tx_hash, max_fee_per_gas_wei, max_priority_fee_wei, gas_bump_count, status, created_at, updated_at
FROM withdrawal_outbox
WHERE status = 'QUEUED'
ORDER BY created_at ASC
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: ListStuckSubmittedWithdrawals :many
SELECT withdrawal_id, idempotency_key, user_id, recipient_address, asset,
       human_amount, raw_amount_wei, signing_tier, voucher_nonce, relayer_eoa_nonce,
       last_tx_hash, max_fee_per_gas_wei, max_priority_fee_wei, gas_bump_count, status, created_at, updated_at
FROM withdrawal_outbox
WHERE status = 'SUBMITTED' AND updated_at <= $1
ORDER BY updated_at ASC
FOR UPDATE SKIP LOCKED;

-- name: MarkWithdrawalSubmitted :one
UPDATE withdrawal_outbox
SET status = 'SUBMITTED',
    relayer_eoa_nonce = $2,
    last_tx_hash = $3,
    max_fee_per_gas_wei = $4,
    max_priority_fee_wei = $5,
    updated_at = NOW()
WHERE withdrawal_id = $1
RETURNING withdrawal_id, idempotency_key, user_id, recipient_address, asset,
          human_amount, raw_amount_wei, signing_tier, voucher_nonce, relayer_eoa_nonce,
          last_tx_hash, max_fee_per_gas_wei, max_priority_fee_wei, gas_bump_count, status, created_at, updated_at;

-- name: BumpWithdrawalGas :one
UPDATE withdrawal_outbox
SET last_tx_hash = $2,
    max_fee_per_gas_wei = $3,
    max_priority_fee_wei = $4,
    gas_bump_count = gas_bump_count + 1,
    updated_at = NOW()
WHERE withdrawal_id = $1
RETURNING withdrawal_id, idempotency_key, user_id, recipient_address, asset,
          human_amount, raw_amount_wei, signing_tier, voucher_nonce, relayer_eoa_nonce,
          last_tx_hash, max_fee_per_gas_wei, max_priority_fee_wei, gas_bump_count, status, created_at, updated_at;

-- name: MarkWithdrawalConfirmed :one
UPDATE withdrawal_outbox
SET status = 'CONFIRMED',
    updated_at = NOW()
WHERE withdrawal_id = $1
RETURNING withdrawal_id, idempotency_key, user_id, recipient_address, asset,
          human_amount, raw_amount_wei, signing_tier, voucher_nonce, relayer_eoa_nonce,
          last_tx_hash, max_fee_per_gas_wei, max_priority_fee_wei, gas_bump_count, status, created_at, updated_at;

-- name: MarkWithdrawalFailedRefunded :one
UPDATE withdrawal_outbox
SET status = 'FAILED_REFUNDED',
    updated_at = NOW()
WHERE withdrawal_id = $1
RETURNING withdrawal_id, idempotency_key, user_id, recipient_address, asset,
          human_amount, raw_amount_wei, signing_tier, voucher_nonce, relayer_eoa_nonce,
          last_tx_hash, max_fee_per_gas_wei, max_priority_fee_wei, gas_bump_count, status, created_at, updated_at;

-- name: GetMaxVoucherNonce :one
SELECT COALESCE(MAX(voucher_nonce), 0)::BIGINT AS max_nonce
FROM withdrawal_outbox;
