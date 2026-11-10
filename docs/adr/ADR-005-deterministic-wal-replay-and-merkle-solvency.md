# ADR-005: Binary WAL Framing, Deterministic Replay, and Merkle Proof-of-Reserves

- **Status:** Accepted
- **Date:** 2026-11-20
- **Scope:** `engine/src/wal.cpp`, `engine/src/snapshot.cpp`, `services/cmd/replay-cli/`, `services/internal/reconciler/`

## 1. Context

Because the C++17 Central Limit Order Book maintains active order state in volatile RAM, process crashes or power loss require a recovery mechanism that reconstructs the exact pre-crash order book in milliseconds. Furthermore, external auditors and users must be able to verify two independent properties without trusting the exchange operator:
1. **Execution Determinism:** Replaying the command log from genesis (or a verified snapshot) produces the exact same trade fills and resting order book state hash down to the single byte.
2. **Custody Solvency (Proof-of-Reserves):** On-chain assets held inside `EscrowVault.sol` are greater than or equal to the sum of all user balances (`available + locked`), and every user can cryptographically verify that their non-negative balance was included in the published on-chain root.

## 2. Decision

1. **Fixed-Size 64-Byte Binary Write-Ahead Log (WAL) with CRC32C Framing:** Every state-mutating command entering the C++17 CLOB is assigned a strictly monotonic 64-bit `wal_seq` and written as a fixed 64-byte binary record protected by an IEEE/Castagnoli CRC32 checksum.
2. **Periodic Binary Snapshots + Tail WAL Replay:** The engine writes compacted point-in-time order book snapshots (`snapshot_<wal_seq>.snap`) so crash recovery loads the latest valid snapshot and replays only records with `seq > snapshot_seq`.
3. **Deterministic State Hashing (`replay-cli`):** `services/cmd/replay-cli` replays any WAL segment headlessly through the C++17 engine via C ABI and computes a canonical SHA-256 digest over all resting orders and cumulative fill state.
4. **60-Second Solvency Invariant Check & Hourly Merkle Proof-of-Reserves Root Publication:** The `reconciler` service verifies global asset solvency every 60 seconds (`reserves_invariant_ok` metric) and constructs a binary Merkle tree over all user liabilities every hour, publishing the root on-chain via `EscrowVault.publishReserveRoot(merkleRoot, blockHeight)`.

## 3. Exact Specification

### 3.1 Binary WAL Record Layout (64 Bytes, Little-Endian)

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|          Magic (0x5343574C = "SCWL")          |Version|Opcode |  [0..5]
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|           Market ID           |        Flags (TIF / STP)      |  [6..9]
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|              Reserved / Alignment Padding (0x0000)            |  [10..11]
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                    CRC32C Checksum (Bytes 16..63)             |  [12..15]
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
+                       wal_seq (uint64_t)                      +  [16..23]
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
+                       order_id (uint64_t)                     +  [24..31]
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
+                    account_id_low (uint64_t)                  +  [32..39]
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
+                   account_id_high (uint64_t)                  +  [40..47]
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
+                      price_1e8 (int64_t)                      +  [48..55]
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
+                       qty_1e8 (int64_t)                       +  [56..63]
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

- **WAL Opcodes:**
  - `0x01`: `OP_SUBMIT_ORDER` (`side`, `order_type`, `time_in_force`, `stp_mode` packed into `Flags`)
  - `0x02`: `OP_CANCEL_ORDER` (`order_id`, `account_id`)
  - `0x03`: `OP_CANCEL_ALL_ACCOUNT` (`account_id` — triggered by WebSocket Cancel-on-Disconnect)
  - `0x04`: `OP_UPDATE_MARK_PRICE` (`price_1e8` — updates circuit breaker reference mark price)
  - `0x05`: `OP_SNAPSHOT_BARRIER` (`wal_seq` checkpoint marker)

### 3.2 WAL Durability Modes (`ENGINE_WAL_FSYNC_MODE`)

SettleCore supports four WAL durability modes (detailed in [ADR-007](ADR-007-wal-durability-contract-and-group-commit.md)), paired with the `"SCEV"` execution event log (`*.wal.evlog`, [ADR-006](ADR-006-event-sourced-core-and-execution-event-log.md)) and benchmarked in `docs/assets/benchmarks/wal-overhead-{dark,light}.svg`:
1. `memory` — In-memory ring buffer only (baseline CLOB data-structure benchmark).
2. `buffered` — `write(2)` to OS page cache on every command + periodic `fdatasync(2)` (acknowledges before sync).
3. `group_commit` — Batch `write(2)` + `fdatasync(2)` covering `.wal` and `.evlog`, releasing acknowledgements **only after `fdatasync` succeeds** ([ADR-007](ADR-007-wal-durability-contract-and-group-commit.md)).
4. `fsync` — Synchronous `fdatasync(2)` per command before returning `sc_exec_report_t`.

### 3.3 Canonical State SHA-256 Digest (`replay-cli`)

To verify deterministic replay across machines or after crash recovery, `replay-cli` serializes the active order book in canonical order:
1. Iterate markets in ascending `market_id`.
2. Iterate bid price levels in descending `price_1e8`, then ask price levels in ascending `price_1e8`.
3. Within each price level, iterate the intrusive FIFO queue from `head_idx` to `tail_idx`, feeding `(order_id, account_id_high, account_id_low, price_1e8, remaining_1e8, side)` in little-endian binary into SHA-256.

Two engines that have processed the same WAL prefix `[1..N]` must produce identical 32-byte SHA-256 digests:

$$\text{SHA256}(\text{State}(\text{Replay}(W_{1..N}))) \equiv \text{SHA256}(\text{State}(\text{Live}(W_{1..N})))$$

### 3.4 Merkle Proof-of-Reserves Tree Construction

Every hour (and on demand via the UI/API), `services/internal/reconciler/merkle.go` snapshots all user balances at a consistent PostgreSQL `REPEATABLE READ` transaction and EVM block height $B$, building a **Merkle Sum Tree** ([ADR-008](ADR-008-merkle-sum-tree-proof-of-reserves.md)) paired with on-chain `EscrowVault.isSolvent(token)` verification:

1. **Leaf Construction (Domain-Separated to Prevent Second-Preimage Attacks):**
   For each user $u$ sorted lexicographically by `user_id`, let $b_{u,a} = \text{available}_{u,a} + \text{locked}_{u,a}$ scaled to native integer units:
   $$\text{Leaf}_u = \text{Keccak256}\left(\texttt{0x00} \ \parallel \ \text{user\_id}_u \ \parallel \ \text{nonce}_u \ \parallel \ b_{u,\text{ETH}} \ \parallel \ b_{u,\text{USDC}} \ \parallel \ b_{u,\text{WBTC}} \ \parallel \ b_{u,\text{USDT}}\right)$$
2. **Internal Node Construction (`0x01` Prefix with Asset Sum Commitments):**
   $$\text{Node}_{L,R} = \text{Keccak256}\left(\texttt{0x01} \ \parallel \ \text{Hash}_L \ \parallel \ \text{Hash}_R \ \parallel \ S_{\text{ETH}} \ \parallel \ S_{\text{USDC}} \ \parallel \ S_{\text{WBTC}} \ \parallel \ S_{\text{USDT}}\right)$$
   Odd-length levels duplicate a zero-balance sentinel leaf (`0x00...00`, $\vec{0}$).
3. **Solvency Invariant Verification:**
   For each asset $a \in \{\text{ETH}, \text{USDC}, \text{WBTC}, \text{USDT}\}$:
   $$\text{OnChainBalance}(\text{EscrowVault}, a, B) \ge \sum_{u \in \text{Users}} \left(\text{available}_{u,a} + \text{locked}_{u,a}\right) + \text{available}_{\text{FEE},a}$$
   If holds for all assets, `reserves_invariant_ok` is set to `1` and the root is published on-chain via `EscrowVault.publishReserveRoot(root, B)`.

## 4. Alternatives Considered

1. **Text / JSON Lines WAL:** Rejected. Formatting and parsing ASCII numbers adds $2\text{--}5\,\mu\text{s}$ per order and triples disk bandwidth compared to a fixed 64-byte aligned binary record.
2. **RocksDB / SQLite Embedded in the C++ Engine:** Rejected. LSM-tree background compactions cause multi-millisecond tail-latency spikes (`p99.99`) that stall a single-threaded matching loop.

## 5. Trade-offs & Failure Modes

- **Torn Write at WAL Tail During Kernel Panic:** If an abrupt power failure leaves a partial record (< 64 bytes) or corrupt bytes at the end of the active `.wal` segment, the CRC32C check fails on the final frame. The recovery loader truncates the incomplete trailing frame at the last valid `wal_seq` and reconciles any unsubmitted `RESERVED` database orders per ADR-003.
