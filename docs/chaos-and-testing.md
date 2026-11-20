# SettleCore Chaos Engineering & Verification Specification

SettleCore is verified through a four-layer deterministic test pyramid (including FoundationDB/TigerBeetle-style seeded fault-injection simulation in `services/internal/sim/`) combined with five live fault-injection chaos drills runnable via CLI (`scripts/chaos/*.sh`), Gateway API (`POST /v1/chaos/*`), or the Next.js **Chaos & Systems Lab** (`web/src/components/ChaosControlPanel.tsx`).

---

## 1. Four-Layer Verification Pyramid

| Layer | Workspace & Runner | Verification Scope & Flags |
| :--- | :--- | :--- |
| **1. On-Chain Unit, Fuzz & Invariant** | `contracts/` (`forge test`) | - `EscrowVault.t.sol`: ETH, `USDC`, `WBTC`, and non-standard `USDT` deposits/withdrawals, EIP-712 signature validation, hot/cold threshold boundary, replay nonce rejection.<br>- `EscrowVault.fuzz.t.sol`: `1,000` local / `10,000` CI fuzz runs across arbitrary amounts, deadlines, and signer keys.<br>- `EscrowVault.invariant.t.sol`: Stateful invariant handler (`256` runs, depth `32`) asserting vault token balance $\ge$ cumulative net deposits minus withdrawals and rate-limiter sliding-window bounds. |
| **2. C++17 CLOB & Sanitizer Suite** | `engine/` (`ctest --preset asan-ubsan`) | - Compiled with `-fsanitize=address,undefined -fno-omit-frame-pointer -Werror`.<br>- `order_book_test.cpp`: Price-time FIFO priority, $O(1)$ cancel, partial fills, slab exhaustion recovery.<br>- `matching_engine_test.cpp`: `GTC`, `IOC`, `FOK` semantics and `__int128_t` quote notional overflow boundaries.<br>- `wal_replay_test.cpp`: CRC32 frame corruption detection, torn-tail truncation, snapshot + WAL replay state hash identity.<br>- `stp_circuit_breaker_test.cpp`: Self-Trade Prevention (`CANCEL_NEWEST`, `CANCEL_OLDEST`, `CANCEL_BOTH`) and $\pm 10\%$ mark price circuit breaker. |
| **3. Go 1.27 Race, Differential & Deterministic Sim** | `services/` (`go test -race ./...`) | - Executed with Go 1.27 `-race` detector.<br>- `RunStoreConformanceSuite`: Shared ledger conformance across `MemoryStore` and `PostgresStore` (enforcing `trg_ledger_entries_balanced` & `trg_ledger_entries_immutable`).<br>- `TestDifferentialEngineEquivalenceAndWALReplay`: 25,000 seeded commands verifying identical fills, L2 depth, `.evlog` frames, and SHA-256 `StateHash()`.<br>- `TestDeterministicSimulation`: `100` seeds $\times$ `200` steps (`20,000` transitions) injecting reorgs, RPC blackouts, gas spikes, flash crashes, and mid-flight gateway crashes while asserting `I-1`..`I-6` at every step. |
| **4. Live End-to-End Chaos Drills** | `scripts/chaos/*.sh` + Next.js UI | - Five live fault-injection drills against running stack or `e2e_chaos_test.go` (`gateway`, `watcher`, `relayer`, `settlement`, `reconciler`). |

---

## 2. Live Chaos Drills & Observable Telemetry Assertions

### Drill 1: Deterministic 3-Block Chain Reorganization (`scripts/chaos/trigger-reorg.sh`)

- **Command:** `make chaos-reorg` or click **`Trigger 3-Block Reorg`** in the UI Chaos Control Panel.
- **Fault Mechanism:**
  1. Records an on-chain deposit at block $H$ and mines blocks $H+1, H+2$ so the deposit reaches `CONFIRMED` ($N_{\text{conf}} = 3$) and credits the user's PostgreSQL 18 `available` balance.
  2. Reverts Anvil state to parent block $H-1$ via `evm_revert` and mines 4 empty canonical blocks ($H' \dots H'+3$) with divergent block hashes.
- **Expected System Response:**
  - `watcher` detects `Block[H'+3].parentHash != indexed_blocks[H+2].blockHash`.
  - Walks backward to common ancestor $H-1$, marks old blocks $H \dots H+2$ and the deposit as `ORPHANED`, and posts an atomic compensating double-entry debit in `ledger_entries`.
- **Observable Pass Criteria:**
  - Prometheus counter `reorg_rollbacks_total` increments by `1`.
  - Prometheus gauge `reserves_invariant_ok` remains `1`.
  - User deposit badge in Next.js UI updates from `CONFIRMED` to `ORPHANED / ROLLED BACK`.

### Drill 2: Primary EVM RPC Node Kill & Failover (`scripts/chaos/kill-rpc.sh`)

- **Command:** `bash scripts/chaos/kill-rpc.sh kill` (and `bash scripts/chaos/kill-rpc.sh restore`).
- **Fault Mechanism:** Terminates the `rpc-proxy-1` (`:8545`) upstream relay while Anvil continues producing blocks at 1-second intervals.
- **Expected System Response:**
  - `services/internal/evm/rpc_pool.go` detects connection failure within $< 500\,\text{ms}$ and switches the active provider to `rpc-proxy-2` (`:8546`).
- **Observable Pass Criteria:**
  - `indexer_block_lag` remains $\le 1$ block with zero missed deposit events.
  - Next.js Telemetry Strip updates active RPC indicator from `rpc-proxy-1 (PRIMARY)` to `rpc-proxy-2 (FAILOVER)`.

### Drill 3: 10x EIP-1559 Base-Fee Spike (`scripts/chaos/gas-spike.sh`)

- **Command:** `bash scripts/chaos/gas-spike.sh`
- **Fault Mechanism:** Submits a queued withdrawal while simultaneously calling `anvil_setNextBlockBaseFeePerGas` with a $10\times$ base-fee jump so the initial broadcast transaction sits unmined in the mempool.
- **Expected System Response:**
  - `services/internal/evm/gas_bumper.go` detects the unmined withdrawal after the stuck-transaction threshold, computes updated EIP-1559 fee caps ($\ge +15\%$ priority and max fee covering $2 \times \text{newBaseFee}$), and re-signs a replacement transaction at the **exact same relayer EOA nonce**.
- **Observable Pass Criteria:**
  - Prometheus counter `stuck_tx_gas_bumps` increments.
  - `withdrawal_outbox.gas_bump_count` increments and `status` transitions `SUBMITTED -> CONFIRMED` without creating a nonce gap.

### Drill 4: -15% Flash Crash Circuit Breaker (`scripts/chaos/flash-crash.sh`)

- **Command:** `bash scripts/chaos/flash-crash.sh`
- **Fault Mechanism:** Injects an aggressive sell sweep on `ETH/USDC` priced $15\%$ (`1500` bps) below the current reference mark price, exceeding the $10\%$ (`1000` bps) `ENGINE_CIRCUIT_BREAKER_BPS` collar.
- **Expected System Response:**
  - C++17 `circuit_breaker.cpp` halts aggressive matching before executing fills beyond the $10\%$ collar, returns `SC_ERR_CIRCUIT_BREAKER_TRIPPED`, and switches the market to `CANCEL_ONLY` mode.
  - Gateway immediately releases the pre-trade database reservation (`locked -> available`) for the rejected sweep order.
- **Observable Pass Criteria:**
  - Next.js Telemetry Strip displays `CIRCUIT BREAKER: HALTED (>10% DEV)`.
  - Resting maker orders below the collar remain untouched; cancel requests continue executing in $O(1)$.

### Drill 5: Kill Gateway Mid-Flight (`POST /v1/chaos/gateway-crash`)

- **Command:** `POST /v1/chaos/gateway-crash` or click **`Kill Gateway Mid-Flight`** in the Chaos & Systems Lab.
- **Fault Mechanism:** Executes pre-trade Postgres balance reservation (`available -> locked`, `orders.status = 'RESERVED'`) and simulates a hard process crash *before* the command is appended to the C++17 WAL (`engine.wal`).
- **Expected System Response:**
  - Startup reconciliation (`ReconcileOrphanedReservations` in `services/internal/gateway/recovery.go`) scans `RESERVED` orders missing a committed `wal_seq`, transitions them to `ABORTED_UNSUBMITTED`, and posts balanced `CANCEL_RELEASE` entries returning `locked -> available`.
- **Observable Pass Criteria:**
  - `orphans_rolled_back >= 1` (`ABORTED_UNSUBMITTED`), `reserves_invariant_ok` remains `1`, and all 6 Live Invariants stay green.
