# SettleCore REST & WebSocket API Specification

The SettleCore `gateway` (`services/cmd/gateway`) exposes a versioned HTTP/REST interface (`gin-gonic/gin`) and a low-latency multiplexed WebSocket feed (`github.com/coder/websocket`) on `:8080`.

All financial quantities and prices in JSON payloads are encoded as **base-10 decimal strings** (never JSON IEEE-754 numbers) and parsed in Go 1.27 via `shopspring/decimal` before conversion to 8-decimal fixed-point `int64_t` (`1e8` scale) at the C++17 C ABI boundary.

---

## 1. Authentication & Idempotency Headers

| Header | Required On | Format | Description |
| :--- | :--- | :--- | :--- |
| `X-SettleCore-User-Id` | All private endpoints | UUIDv4 | Authenticated account owner identifier (or EIP-712 session bearer token). |
| `Idempotency-Key` | `POST /v1/orders`, `POST /v1/withdrawals` | UUIDv4 | Enforces exactly-once execution across network retries. Duplicate keys return the cached execution receipt. |

---

## 2. REST Endpoints

### 2.1 Submit Order (`POST /v1/orders`)

Performs synchronous pre-trade PostgreSQL 18 balance reservation (`available -> locked`) and dispatches the command across the C ABI into the C++17 CLOB.

**Request Body (optional EIP-712 fields `signature`, `signer_address`, `order_nonce`, `deadline` verified via `OrderEIP712Verifier`):**
```json
{
  "market_id": "ETH/USDC",
  "side": "BUY",
  "order_type": "LIMIT",
  "time_in_force": "GTC",
  "stp_mode": "CANCEL_NEWEST",
  "price": "3125.50000000",
  "quantity": "2.50000000"
}
```

**Response (`201 Created`):**
```json
{
  "order_id": "9004128",
  "idempotency_key": "d290f1ee-6c54-4b01-90e6-d701748f0851",
  "wal_seq": 148209,
  "market_id": "ETH/USDC",
  "status": "PARTIALLY_FILLED",
  "filled_quantity": "1.00000000",
  "remaining_quantity": "1.50000000",
  "match_latency_ns": 840,
  "fills": [
    {
      "trade_id": "55019",
      "maker_order_id": "9004110",
      "price": "3125.00000000",
      "quantity": "1.00000000",
      "quote_notional": "3125.00000000"
    }
  ],
  "lifecycle": {
    "reserved_at_ns": 1732066812419000000,
    "wal_appended_at_ns": 1732066812419100000,
    "matched_at_ns": 1732066812419100840,
    "match_latency_ns": 840,
    "wal_seq": 148209,
    "journal_tx_id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
    "eip712_verified": true
  }
}
```

### 2.2 Cancel Order (`DELETE /v1/orders/{order_id}`)

Removes a resting order from the C++17 CLOB in $O(1)$ time and schedules immediate release of remaining `locked` funds to `available`.

**Query Parameters:** `market_id=ETH/USDC`

**Response (`200 OK`):**
```json
{
  "order_id": "9004128",
  "wal_seq": 148215,
  "status": "CANCELED",
  "released_locked_amount": "4688.25000000"
}
```

### 2.3 List Account Balances (`GET /v1/accounts/balances`)

**Response (`200 OK`):**
```json
{
  "user_id": "8e3b91c0-4a12-4e8d-9b1c-001122334455",
  "balances": [
    {
      "asset": "ETH",
      "available": "14.250000000000000000",
      "locked": "2.000000000000000000"
    },
    {
      "asset": "USDC",
      "available": "48210.500000000000000000",
      "locked": "4688.250000000000000000"
    },
    {
      "asset": "WBTC",
      "available": "0.850000000000000000",
      "locked": "0.000000000000000000"
    },
    {
      "asset": "USDT",
      "available": "10000.000000000000000000",
      "locked": "0.000000000000000000"
    }
  ]
}
```

### 2.4 Request On-Chain Withdrawal (`POST /v1/withdrawals`)

Atomically locks user funds in PostgreSQL 18 and queues a row in `withdrawal_outbox` for EIP-712 signing (`HOT` or `COLD_COSIGNED` tier) and EIP-1559 submission.

**Request Body:**
```json
{
  "asset": "USDC",
  "amount": "5000.000000",
  "recipient_address": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
}
```

**Response (`202 Accepted`):**
```json
{
  "withdrawal_id": "3f9a1102-8b77-4c1d-a023-998877665544",
  "asset": "USDC",
  "amount": "5000.000000",
  "raw_amount_wei": "5000000000",
  "signing_tier": "HOT",
  "voucher_nonce": 10042,
  "status": "QUEUED"
}
```

### 2.5 Fetch User Merkle Proof-of-Reserves (`GET /v1/solvency/proof`)

Returns the latest published Merkle root, on-chain `EscrowVault` reserves vs. off-chain liabilities, and the user's inclusion path for client-side Keccak-256 verification.

**Response (`200 OK`):**
```json
{
  "snapshot_id": "a1b2c3d4-0000-4000-8000-010203040506",
  "merkle_root": "0x8f4e2b99c1d3e8f0a44219b7d6c5a3e2f10987654321fedcba9876543210abcd",
  "evm_block_number": 1048,
  "publish_tx_hash": "0x3c91a8f0211928374655647382910abcdef1234567890abcdef1234567890abc",
  "invariant_ok": true,
  "user_leaf": {
    "user_id": "8e3b91c0-4a12-4e8d-9b1c-001122334455",
    "nonce": 1048,
    "balances": {
      "ETH": "16250000000000000000",
      "USDC": "52898750000",
      "WBTC": "85000000",
      "USDT": "10000000000"
    },
    "leaf_hash": "0x19a4f302c88e1d9b4f6c2e3a109283746574839201abcdef0123456789abcdef"
  },
  "merkle_path": [
    {
      "position": "RIGHT",
      "sibling_hash": "0x77b12c34d56e78f90123456789abcdef0123456789abcdef0123456789abcdef"
    },
    {
      "position": "LEFT",
      "sibling_hash": "0x44e98a12b34c56d78e90123456789abcdef0123456789abcdef0123456789abc"
    }
  ]
}
```

### 2.6 Chaos Control Endpoints (`/v1/chaos/*`)

Enabled when `CHAOS_API_ENABLED=true` for interactive failure-injection drills from the Next.js Chaos Control Panel and `scripts/chaos/*.sh`:

| Endpoint | Payload | Effect |
| :--- | :--- | :--- |
| `POST /v1/chaos/reorg` | `{"depth": 3}` |Snapshots/reverts Anvil via `evm_revert` + mines $D+1$ divergent blocks to trigger watcher parent-hash rollback. |
| `POST /v1/chaos/rpc-failover` | `{"action": "kill" \| "restore"}` | Severs or restores `rpc-proxy-1` upstream connection so `rpc_pool.go` fails over to `rpc-proxy-2`. |
| `POST /v1/chaos/gas-spike` | `{"base_fee_multiplier": 10}` | Calls `anvil_setNextBlockBaseFeePerGas` with $10\times$ current base fee to trigger `relayer` EIP-1559 fee bumping. |
| `POST /v1/chaos/flash-crash` | `{"pair": "ETH/USDC", "drop_bps": 1500}` | Submits a synthetic aggressive sweep exceeding the 10% (`1000` bps) mark price deviation band to trip the C++17 circuit breaker. |
| `POST /v1/chaos/gateway-crash` | `{}` (or optional `user_id`, `market_id`, `side`, `price`, `quantity`) | Reserves an order in PostgreSQL (`RESERVED`), simulates a mid-flight crash prior to WAL append, and runs `ReconcileOrphanedReservations` (`ABORTED_UNSUBMITTED`, `locked -> available`). |

---

## 3. WebSocket Protocol (`ws://localhost:8080/v1/ws`)

Implemented using **`github.com/coder/websocket`** (never the archived `gorilla/websocket`).

### 3.1 Subscription & Cancel-on-Disconnect Handshake

```json
{
  "op": "subscribe",
  "cancel_on_disconnect": true,
  "channels": [
    "l2:ETH/USDC",
    "trades:ETH/USDC",
    "user:8e3b91c0-4a12-4e8d-9b1c-001122334455",
    "telemetry"
  ]
}
```

When `"cancel_on_disconnect": true` is set on the WebSocket session, any abnormal TCP disconnect, WebSocket close frame, or missed $3\,\text{s}$ ping/pong deadline causes the Gateway WS hub to immediately enqueue `OP_CANCEL_ALL_ACCOUNT` into the C++17 CLOB for that user.

### 3.2 Channel Frame Schemas

1. **L2 Order Book Snapshot & Diff (`l2:ETH/USDC`):**
   ```json
   {
     "channel": "l2:ETH/USDC",
     "type": "l2_diff",
     "wal_seq": 148220,
     "bids": [["3124.50000000", "8.40000000"], ["3124.00000000", "0.00000000"]],
     "asks": [["3125.50000000", "3.15000000"]],
     "circuit_breaker_tripped": false
   }
   ```
   (A quantity of `"0.00000000"` removes that price level from the client's local book.)

2. **Public Trade Tape (`trades:ETH/USDC`):**
   ```json
   {
     "channel": "trades:ETH/USDC",
     "trade_id": "55019",
     "wal_seq": 148209,
     "price": "3125.00000000",
     "quantity": "1.00000000",
     "taker_side": "BUY",
     "executed_at": "2026-11-20T01:40:12.419283Z"
   }
   ```

3. **Engine & Custody Telemetry Strip (`telemetry`):**
   ```json
   {
     "channel": "telemetry",
     "clob_p50_us": 0.42,
     "clob_p99_us": 1.18,
     "clob_p999_us": 3.65,
     "active_rpc": "rpc-proxy-1",
     "indexer_block_lag": 0,
     "reorg_rollbacks_total": 1,
     "stuck_tx_gas_bumps": 2,
     "reserves_invariant_ok": true,
     "circuit_breaker_status": "NORMAL"
   }
   ```

---

## 4. Error Code Taxonomy

| HTTP Status | Error Code | Trigger Condition |
| :---: | :--- | :--- |
| `400` | `ERR_INVALID_DECIMAL_PRECISION` | Price or quantity violates asset lot/tick step or fixed-point bounds (`[1, 10^15]`). |
| `409` | `ERR_IDEMPOTENCY_CONFLICT` | Same `Idempotency-Key` reused with a different request payload hash. |
| `422` | `ERR_INSUFFICIENT_AVAILABLE_BALANCE` | Pre-trade DB check finds `accounts.available < required_reserve`. |
| `422` | `ERR_CIRCUIT_BREAKER_TRIPPED` | Order price deviates $>10\%$ (`1000` bps) from reference mark price or market is halted. |
| `422` | `ERR_SELF_TRADE_PREVENTED` | Taker order crossed resting maker order from the same `account_id` under STP policy. |
| `429` | `ERR_ENGINE_BACKPRESSURE` | CLOB ring buffer utilization $> 85\%$; only cancellations permitted. |
| `503` | `ERR_ORDER_POOL_EXHAUSTED` | C++17 pre-allocated `OrderNode` slab (`1,048,576` slots) is full. |
