# SettleCore Security Model & Formal System Invariants

This document defines the six core invariants enforced across SettleCore's smart contracts, C++17 matching engine, and Go 1.27 ledger services, along with the threat model and concrete architectural mitigations.

---

## 1. Core System Invariants

| ID | Invariant Name | Formal Mathematical Statement | Enforcement Boundary |
| :---: | :--- | :--- | :--- |
| **I-1** | **Double-Entry Conservation** | $\forall \text{tx } j, \forall \text{asset } a: \sum_{e \in j} \text{credit}_{e,a} - \sum_{e \in j} \text{debit}_{e,a} = 0$ | PostgreSQL 18 `ledger_entries` transaction commit boundary + `accounts` `CHECK (available >= 0 AND locked >= 0)` |
| **I-2** | **Proof-of-Reserves Solvency** | $\forall a, \forall \text{block } B: \text{Balance}_{\text{EscrowVault}}(a, B) \ge \sum_{u \in \text{Users}} (\text{available}_{u,a} + \text{locked}_{u,a}) + \text{available}_{\text{FEE},a}$ | `reconciler` 60s invariant loop (`reserves_invariant_ok`) + hourly on-chain Merkle root (`EscrowVault.publishReserveRoot`) |
| **I-3** | **Fixed-Point Precision & Overflow Safety** | Zero `float64` in any financial struct; CLOB uses `int64_t` ($10^8$ scale) with `__int128_t` intermediate products; DB uses `NUMERIC(38,18)` | C++17 compile-time types + `sqlc.yaml` `shopspring/decimal.Decimal` overrides |
| **I-4** | **Reorg Lineage Finality** | $\forall H \in \text{CANONICAL}: \text{Block}[H].\text{parentHash} \equiv \text{Block}[H-1].\text{blockHash}$; deposits credited iff $H_{\text{head}} - H_{\text{dep}} \ge N_{\text{conf}}$ | `watcher` parent-hash verification + atomic PostgreSQL 18 reorg rollback transaction |
| **I-5** | **Nonce Monotonicity & Replay Immunity** | Relayer EOA nonces allocate sequentially without gaps; on-chain `usedNonces[voucherNonce]` transitions $0 \rightarrow 1$ at most once | `relayer` serialized outbox lock (`FOR UPDATE`) + `EscrowVault.sol` `usedNonces` mapping |
| **I-6** | **Deterministic WAL Replay** | $\text{SHA256}(\text{State}(\text{Replay}(W_{1..N}))) \equiv \text{SHA256}(\text{State}(\text{Live}(W_{1..N})))$ | Single-threaded C++17 CLOB + 64-byte CRC32C WAL + `services/cmd/replay-cli` |

---

## 2. Threat Model & Mitigations

### 2.1 Hot-Signer Private Key Compromise
- **Threat:** An attacker compromises the `relayer` host and extracts `HOT_SIGNER_PRIVATE_KEY`, attempting to drain all `ETH`, `USDC`, `WBTC`, and `USDT` from `EscrowVault.sol`.
- **Mitigation:**
  1. `EscrowVault.sol` enforces two independent on-chain caps on `HOT_SIGNER_ROLE` withdrawals:
     - **Per-Transaction Cap (`hotPerTxCap[token]`):** Any withdrawal where `amount > hotPerTxCap[token]` reverts unless accompanied by a valid EIP-712 co-signature from `COLD_SIGNER_ROLE`.
     - **Rolling 24-Hour Token-Bucket Rate Cap (Hand-Written Limiter paired with OpenZeppelin Contracts `v5.6.1`):** Tracks continuous linear-decay hot-tier outflow per asset over any sliding 24-hour window (eliminating the $2\times$ UTC midnight boundary burst possible with calendar-day buckets; see [ADR-009](adr/ADR-009-rolling-token-bucket-withdrawal-limiter.md)). Once the token-bucket capacity is exhausted, even small withdrawals revert on-chain without a `COLD_SIGNER_ROLE` signature.
  2. If the 60-second `reconciler` detects any unauthorized on-chain outflow (`reserves_invariant_ok == 0`), it emits an immediate alert and triggers an emergency pause.

### 2.2 Chain Reorganization Double-Credit Attack
- **Threat:** An attacker deposits on a short-lived fork block $H$, trades or withdraws once credited, and the deposit block $H$ is subsequently orphaned by a canonical chain reorg.
- **Mitigation:**
  1. Deposits recorded at block $H$ remain in `status = 'PENDING'` (not added to `accounts.available`) until $N_{\text{conf}}$ subsequent canonical blocks link to $H$ via verified `parent_hash`.
  2. When a reorg occurs within the confirmation window (or during the 3-block reorg chaos drill), `services/internal/evm/reorg.go` atomically marks orphaned blocks and posts a compensating double-entry debit (`REORG_REVERSAL`) in the same database transaction.

### 2.3 Concurrent Order & Withdrawal Double-Spend Race
- **Threat:** A user simultaneously submits 50 parallel `POST /v1/orders` and `POST /v1/withdrawals` requests attempting to spend the same `available` balance multiple times across concurrent HTTP threads.
- **Mitigation:**
  1. Both pre-trade order reservation and withdrawal outbox queuing acquire an explicit row-level lock on `accounts` (`SELECT available, locked FROM accounts WHERE user_id = $1 AND asset = $2 FOR UPDATE`) and enforce the PostgreSQL 18 table constraint `CHECK (available >= 0 AND locked >= 0)`.
  2. Every mutating endpoint requires a client `Idempotency-Key` backed by a `UNIQUE` index in PostgreSQL 18.

### 2.4 Non-Standard ERC-20 Return Data (`USDT`) & Reentrancy
- **Threat:**
  1. Calling standard `IERC20(usdt).transfer(recipient, amount)` on Tether (`USDT`) reverts in Solidity `0.8.x` because `USDT.transfer` returns no boolean value (`returndatasize() == 0`).
  2. Malicious token hooks or native ETH fallback receivers attempt reentrant calls into `EscrowVault.withdraw`.
- **Mitigation:**
  1. `EscrowVault.sol` wraps all ERC-20 transfers with OpenZeppelin `SafeERC20` (`using SafeERC20 for IERC20;`), which inspects `returndatasize()` and accepts either empty return data (on non-reverting calls) or ABI-encoded `true`.
  2. Only explicitly allowlisted tokens (`isAllowedToken[token] == true`) can be deposited or withdrawn.
  3. All external state-mutating functions in `EscrowVault.sol` follow the Checks-Effects-Interactions (CEI) pattern (`usedNonces[nonce] = true` before transfer) and carry OpenZeppelin's `nonReentrant` modifier (`ReentrancyGuard`).

### 2.5 Wash Trading & Self-Trade Manipulation
- **Threat:** A single account places crossing buy and sell orders against itself to fabricate trade volume on the public `TradeTape` and distort candlestick charts.
- **Mitigation:**
  - The C++17 matching loop checks `taker.account_id == maker.account_id` before executing any fill and enforces Self-Trade Prevention (`STP_CANCEL_NEWEST`, `STP_CANCEL_OLDEST`, or `STP_CANCEL_BOTH`) directly in memory with zero fake trade events emitted.
