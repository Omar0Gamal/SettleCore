# Changelog

All notable engineering milestones in SettleCore are documented in this file.

## [1.0.0] — 2026-11-20

Production Core, Custody & Terminal release:

### Architecture & Core Systems
- **Rolling 24h Token-Bucket Limiter & Role Separation (`EscrowVault.sol`, `RateLimited.sol`, `ReserveAttestation.sol`, ADR-009):** Continuous linear-decay 24-hour token-bucket rate limiter preventing calendar-day boundary bursts, isolated `attester` role separated from `hotSigner`, and `Ownable` custody governance with Foundry invariant tests.
- **Merkle Sum Tree & On-Chain Solvency (`reconciler/merkle.go`, `ReserveAttestation.sol`, ADR-008):** Multi-asset **Merkle Sum Tree** (`Keccak256(0x01 || L || R || sumL+sumR)` per asset) binding cumulative user liabilities to published roots and on-chain `isSolvent(token)` verification.
- **Event-Sourced Core & Execution Event Log (`engine/`, `services/cmd/settlement/`, ADR-006):** Paired 64-byte CRC32C input command WAL (`"SCWL"`) and output execution event log (`"SCEV"`) so `settlement` consumes execution events deterministically without re-running matching.
- **Group-Commit WAL (`engine/src/wal.cpp`, ADR-007):** `GROUP_COMMIT` durability mode amortizing `fdatasync` across batches covering `.wal` and `.evlog` while releasing acknowledgements only after durable sync.
- **Open-Loop Benchmarking & Multi-Market Sharding (`engine/benchmarks/`):** Open-loop arrival-rate recording (`hdr_record_corrected_value`), batched C-ABI dispatch (`sc_engine_submit_batch`), and dual-theme SVG benchmark charts across all four WAL durability modes and book depths.
- **Database-Enforced Double-Entry (`services/migrations/`, `services/internal/ledger/`):** PostgreSQL 18 deferred constraint trigger (`trg_ledger_entries_balanced`) verifying `SUM(debit) = SUM(credit)` per `journal_tx_id`, append-only `trg_ledger_entries_immutable` enforcement, and a shared `Store` conformance suite (`MemoryStore` + PostgreSQL 18).
- **Deterministic Simulation Testing (`services/internal/sim`, ADR-010):** Seeded fault-injection simulation driving random orders, engine crashes, reorgs, RPC flaps, and relayer crashes while asserting `I-1`..`I-6` at every step.
- **C++17 Engine via C ABI & Differential Equivalence (`services/internal/engine/`):** C++17 `libsettlecore_engine.a` execution path (`CGO_ENABLED=1`) with C++↔Go differential equivalence tests, WAL cross-replay verification, zero-allocation matcher verification, and libFuzzer smoke harness.
- **Spot Trading Terminal & Static Demo (`web/`):** Multi-workspace Next.js 16.4 terminal featuring `Trade`, `Custody & Balances`, `Chaos & Systems Lab`, and `Proof of Reserves` views, Order Lifecycle Inspector drawer, Live Invariants strip, Architecture Guide, `Cmd/Ctrl+K` command palette, 5 chaos drills, and static export mode (`NEXT_PUBLIC_STATIC_EXPORT=1`).
- **4K Media, Dual-Theme Architecture SVGs & Documentation (`demo-video/`, `docs/`):** Playwright `3840x2160` capture pipeline, `<10 MB` faststart 1080p @ 60fps launch MP4 + clickable poster card, dual-theme dark/light vector system topology SVGs (`docs/assets/architecture/`), studio PNG product renders, `ADR-001`..`ADR-010`, `SECURITY.md`, `.gitattributes`, and unconditional CI checks in `.github/workflows/ci.yml`.

---

## [0.7.0] — 2026-11-19

Initial Multi-Layer Implementation:

- **Core Scaffolding:** Monorepo layout, Makefile, `docker-compose.yml`, Prometheus/Grafana configs, and `ADR-001`..`ADR-005`.
- **Smart Contracts (`contracts/`):** Solidity `0.8.38` `EscrowVault.sol`, `IEscrowVault.sol`, ERC-20 mocks (`MockUSDC`, `MockWBTC`, non-standard `MockUSDT`), Foundry unit/fuzz/invariant tests.
- **Matching Engine (`engine/`):** C++17 CLOB (`OrderNode` 64-byte slab, AVL price levels, `FlatOrderMap`, `STP`, `±10%` circuit breaker), CRC32C WAL, binary snapshots, C ABI, `HdrHistogram_c` + Google Benchmark suites, and `uv` dual-theme SVG chart generator.
- **Ledger & Gateway (`services/`):** Go 1.27 double-entry ledger (`shopspring/decimal`, PostgreSQL 18 `NUMERIC(38,18)`), `gateway` (`gin` + `coder/websocket` Cancel-on-Disconnect), `settlement`, and `replay-cli`.
- **EVM Custody & Reconciler (`services/internal/evm`, `reconciler`):** EVM `watcher` (parent-hash reorg rollback), `rpc_pool` (`<500 ms` failover), `relayer` (serialized EOA nonce + `+15%` EIP-1559 gas bumper), and `reconciler` (60s solvency + Merkle PoR).
- **Market-Maker & Web Terminal (`services/cmd/market-maker`, `web/`):** External `market-maker` bot and Next.js 16.4 trading terminal (`lightweight-charts`, `wagmi` v3, `viem` v2).
- **End-to-End Verification (`services/internal/gateway/`):** Integration and chaos drill verification (`TestEndToEndChaosDrillsAndPrometheusMetrics`).
- **Launch Media Pipeline (`demo-video/`):** Playwright + Remotion `v4` launch video pipeline and studio product media.
