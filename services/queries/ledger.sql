-- name: EnsureAccount :one
INSERT INTO accounts (account_id, user_id, asset, account_type)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, asset, account_type) DO UPDATE
SET updated_at = accounts.updated_at
RETURNING account_id, user_id, asset, account_type, available, locked, version, created_at, updated_at;

-- name: GetAccountForUpdate :one
SELECT account_id, user_id, asset, account_type, available, locked, version, created_at, updated_at
FROM accounts
WHERE user_id = $1 AND asset = $2 AND account_type = $3
FOR UPDATE;

-- name: GetAccountsByIDsForUpdate :many
SELECT account_id, user_id, asset, account_type, available, locked, version, created_at, updated_at
FROM accounts
WHERE account_id = ANY($1::uuid[])
ORDER BY account_id ASC
FOR UPDATE;

-- name: ListUserBalances :many
SELECT account_id, user_id, asset, account_type, available, locked, version, created_at, updated_at
FROM accounts
WHERE user_id = $1 AND account_type = 'USER'
ORDER BY asset ASC;

-- name: UpdateAccountBalances :one
UPDATE accounts
SET available = available + sqlc.arg(available_delta),
    locked = locked + sqlc.arg(locked_delta),
    version = version + 1,
    updated_at = NOW()
WHERE account_id = $1
RETURNING account_id, user_id, asset, account_type, available, locked, version, created_at, updated_at;

-- name: InsertLedgerEntry :one
INSERT INTO ledger_entries (
    journal_tx_id, account_id, asset, entry_type, direction, amount, balance_after, reference_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING entry_id, journal_tx_id, account_id, asset, entry_type, direction, amount, balance_after, reference_id, created_at;

-- name: ListLedgerEntriesByJournalTx :many
SELECT entry_id, journal_tx_id, account_id, asset, entry_type, direction, amount, balance_after, reference_id, created_at
FROM ledger_entries
WHERE journal_tx_id = $1
ORDER BY entry_id ASC;

-- name: SumUserLiabilitiesByAsset :many
SELECT
    asset,
    COALESCE(SUM(available), 0)::NUMERIC(38, 18) AS total_available,
    COALESCE(SUM(locked), 0)::NUMERIC(38, 18) AS total_locked
FROM accounts
WHERE account_type = 'USER'
GROUP BY asset
ORDER BY asset ASC;

-- name: SumFeeTreasuryByAsset :many
SELECT
    asset,
    COALESCE(SUM(available + locked), 0)::NUMERIC(38, 18) AS total_fees
FROM accounts
WHERE account_type = 'SYSTEM_FEE_TREASURY'
GROUP BY asset
ORDER BY asset ASC;

-- name: ListAllUserAccountsForMerkle :many
SELECT user_id, asset, available, locked
FROM accounts
WHERE account_type = 'USER'
ORDER BY user_id ASC, asset ASC;
