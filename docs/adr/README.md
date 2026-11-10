# Architecture Decision Records (`docs/adr/`)

This directory contains the Architecture Decision Records (ADRs) governing SettleCore's arithmetic representation, C++17 / Go 1.27 memory boundary, PostgreSQL 18 double-entry accounting, reorg-safe EVM indexing, WAL durability, Merkle Sum Tree solvency, rolling token-bucket rate limiting, and deterministic simulation testing.

## Index

| ADR | Title | Status | Subsystem Scope |
| :--- | :--- | :--- | :--- |
| [ADR-001](ADR-001-fixed-point-arithmetic-and-decimal-normalization.md) | Fixed-Point Arithmetic and Multi-Decimal Asset Normalization | Accepted | `contracts/`, `engine/`, `services/`, `web/` |
| [ADR-002](ADR-002-cpp17-clob-and-go-c-abi-boundary.md) | Single-Threaded Zero-Lock Hot Path C++17 CLOB and Flat C ABI Boundary | Accepted | `engine/`, `services/internal/engine/` |
| [ADR-003](ADR-003-double-entry-ledger-and-two-phase-settlement.md) | PostgreSQL 18 Double-Entry Ledger and Two-Phase Order Settlement | Accepted | `services/internal/ledger/`, `services/migrations/` |
| [ADR-004](ADR-004-reorg-safe-evm-indexing-and-tiered-custody.md) | Reorg-Safe EVM Indexing, Multi-RPC Failover, and Tiered EIP-712 Custody | Accepted | `contracts/`, `services/internal/evm/` |
| [ADR-005](ADR-005-deterministic-wal-replay-and-merkle-solvency.md) | Binary WAL Framing, Deterministic Replay, and Merkle Proof-of-Reserves | Accepted | `engine/`, `services/cmd/replay-cli/`, `services/internal/reconciler/` |
| [ADR-006](ADR-006-event-sourced-core-and-execution-event-log.md) | Event-Sourced Core and Execution Event Log (`"SCEV"`) | Accepted | `engine/`, `services/internal/engine/`, `services/cmd/settlement/` |
| [ADR-007](ADR-007-wal-durability-contract-and-group-commit.md) | WAL Durability Contract and Group-Commit Amortization | Accepted | `engine/src/wal.cpp`, `engine/benchmarks/` |
| [ADR-008](ADR-008-merkle-sum-tree-proof-of-reserves.md) | Merkle Sum Tree Proof-of-Reserves and On-Chain `isSolvent` | Accepted | `services/internal/reconciler/merkle.go`, `contracts/src/EscrowVault.sol`, `web/` |
| [ADR-009](ADR-009-rolling-token-bucket-withdrawal-limiter.md) | Rolling Token-Bucket Withdrawal Limiter and Role Separation | Accepted | `contracts/src/EscrowVault.sol`, `contracts/test/` |
| [ADR-010](ADR-010-deterministic-simulation-testing.md) | Deterministic Simulation Testing (`services/internal/sim`) | Accepted | `services/internal/sim/` |
