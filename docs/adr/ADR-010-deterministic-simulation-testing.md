# ADR-010: Deterministic Simulation Testing (`services/internal/sim`)

- **Status:** Accepted
- **Date:** 2026-11-21
- **Scope:** `services/internal/sim/`, `services/internal/ledger/`, `services/internal/engine/`, `services/internal/evm/`, `services/internal/reconciler/`

## 1. Context

Traditional unit tests check isolated happy paths, while full Docker integration tests (`make e2e`) are slow, non-deterministic in thread scheduling, and cover only a handful of scripted failure scenarios. In distributed exchange architectures—where gateway crashes, WAL torn tails, EVM chain reorganizations, RPC flaps, duplicate webhook/HTTP retries, and relayer crashes interact concurrently—subtle defects often hide in rare interleavings of faults.

Following the deterministic simulation testing methodology pioneered by **FoundationDB** and **TigerBeetle**, SettleCore requires a fast, in-process, **100% seed-reproducible fault-injection harness** that explores hundreds of interleaved failure schedules in seconds and prints a single failing seed for instant replay.

## 2. Decision

Implement a seeded **Deterministic Fault-Injection Simulator** in `services/internal/sim` driven by a deterministic PRNG (`math/rand/v2` or explicit `uint64` PCG/ChaCha seed) and virtual clock:

1. **Unified Workload + Fault Schedule:** Every simulation step draws from a seeded distribution of:
   - User deposits (with configurable confirmation depths),
   - Order submissions (`LIMIT`, `MARKET`, `GTC`, `IOC`, `FOK`), cancellations, and cancel-all events,
   - Withdrawal requests (`HOT` and `COLD_COSIGNED` tiers),
   - **Injected Faults:**
     - Gateway crash between pre-trade Postgres reserve (`RESERVED`) and WAL append (`ABORTED_UNSUBMITTED` recovery),
     - Matching engine crash at random WAL offsets followed by snapshot + WAL replay,
     - EVM chain reorganizations of depth $1 \dots 6$ blocks (rolling back `PENDING` and `CONFIRMED` deposits),
     - Primary RPC endpoint flaps / timeouts triggering failover,
     - Relayer crash between transaction broadcast and DB status update, and
     - Duplicate/replayed order and deposit deliveries.
2. **Per-Step Invariant Assertions (`I-1` to `I-6`):** After **every single simulation step**, the harness asserts all core invariants:
   - **I-1 (Double-Entry Conservation):** $\sum \text{credits} - \sum \text{debits} = 0$ per asset and `available >= 0`, `locked >= 0` across all user accounts.
   - **I-2 (Vault Solvency):** Simulated `EscrowVault` on-chain balances $\ge$ total user liabilities + fee treasury, and Merkle Sum Tree verification passes.
   - **I-3 (Fixed-Point Consistency):** Zero precision drift between `1e8` engine ticks and `shopspring/decimal` ledger balances.
   - **I-4 (Reorg Lineage Consistency):** No `ORPHANED` deposit remains credited; canonical block parent-hash chain is unbroken.
   - **I-5 (Nonce Monotonicity):** Relayer EOA nonces and withdrawal voucher nonces have zero gaps and zero replays.
   - **I-6 (Deterministic Replay Parity):** Replaying the recorded WAL produces an identical canonical `StateHash()`.
3. **One-Line Repro & CI Gate:** `go test -v ./internal/sim/...` runs `100` seeds $\times$ `200` steps (`20,000` fault-injected state transitions) by default in CI. Any failure halts immediately and prints a deterministic one-line repro command (`go test ./internal/sim -run TestDeterministicSimulation -seed=0x...`). `TestSimulatorCatchesInjectedBug` additionally verifies that deliberate invariant violations (`unbalanced_balance`, `skip_reorg_reversal`, `corrupt_wal_replay`) are deterministically detected (`I-2`, `I-6`).

## 3. Exact Specification

```text
Seed (uint64) ──► Deterministic PRNG + Virtual Clock
                       │
                       ▼
           Step Loop (1 .. N_steps)
           ├── Workload Action (Deposit / Order / Cancel / Withdraw)
           ├── Fault Action (Reorg / RPC Flap / Engine Crash / Gateway Crash / Relayer Crash / Dup Delivery)
           └── Assert All Invariants (I-1 .. I-6)
                       │
                       ▼
          On Violation: Print Failing Seed & Step Index -> 100% Deterministic Repro
```

## 4. Alternatives Considered

1. **Chaos Mesh / Docker Kill Loops Only:** Rejected as the primary fault-discovery loop. Container restarts take seconds per fault, cannot explore $20,000+$ state transitions per CI run, and cannot deterministically reproduce timing-sensitive interleavings.
2. **Property-Based Unit Tests Per Package Only:** Insufficient. Package-local tests cannot catch cross-boundary defects (such as a deposit reorg interacting with an in-flight pre-trade order reservation and relayer gas bump).

## 5. Trade-offs & Failure Modes

- **Simulated vs. Real Postgres/EVM Fidelity:** The in-process simulator uses `MemoryStore` and a simulated EVM chain for speed ($20,000$ steps in seconds). To prevent `MemoryStore` from diverging from PostgreSQL 18 behavior, SettleCore enforces a shared **Store Conformance Suite** (`RunStoreConformanceSuite`) that runs identically against both `MemoryStore` and real PostgreSQL 18 (via Testcontainers), ensuring the simulator faithfully mirrors database constraint semantics.
