# Contributing to SettleCore

SettleCore enforces strict mechanical invariants across its C++17 matching engine, Go 1.27 double-entry ledger, Solidity 0.8.38 custody contracts, and Next.js 16.4 interface. Every change must satisfy the repository's verification gates before merge.

## Architectural Rules

1. **Zero Floating-Point Arithmetic in Financial Paths**
   - `float`, `double`, and `float64` are forbidden across order matching, ledger accounting, fee calculation, and custody reconciliation.
   - Internal CLOB prices and quantities use normalized 8-decimal fixed-point `int64_t` (`1e8` scale) as specified in [ADR-001](docs/adr/ADR-001-fixed-point-arithmetic-and-decimal-normalization.md).
   - Go services use `github.com/shopspring/decimal.Decimal` mapped directly to PostgreSQL `NUMERIC(38, 18)` via `sqlc.yaml` overrides.
   - Solidity contracts operate strictly in native token base units (`uint256`).

2. **Single-Threaded Hot Path & Zero Heap Allocations**
   - `engine/` order insertion, cancellation, and price-time matching must execute on the pinned CLOB thread using pre-allocated slab pools.
   - Dynamic memory allocations (`new`, `malloc`, `std::vector::push_back` without reserved capacity) on the hot matching path will fail code review and latency regression checks.

3. **Double-Entry Conservation & Two-Phase Settlement**
   - Every balance mutation in PostgreSQL 18 must record balanced debit and credit legs in `ledger_entries` within a single `READ COMMITTED` transaction holding deterministic row locks (`ORDER BY account_id ASC`).

4. **Non-Standard ERC-20 Safety**
   - All token transfers in `contracts/src/EscrowVault.sol` must use OpenZeppelin `SafeERC20` (`safeTransfer`, `safeTransferFrom`) to handle tokens such as `USDT` that omit boolean return values.

## Toolchain Requirements

| Workspace | Required Toolchain | Configuration File |
| :--- | :--- | :--- |
| `contracts/` | Foundry (`forge`, `anvil`), Solidity `0.8.38`, OpenZeppelin `v5.6.1` | `contracts/foundry.toml` |
| `engine/` | C++17 compiler (`clang++` or `g++`), CMake `3.28+`, Ninja | `engine/CMakePresets.json` |
| `services/` | Go `1.27+`, `sqlc`, `golangci-lint` | `services/go.mod`, `services/sqlc.yaml` |
| `web/` | Node.js `22+`, Next.js `16.4`, TypeScript `5.8+` | `web/package.json`, `web/tsconfig.json` |
| `benchmarks/charts/` | Python `3.12+`, `uv`, `ruff` | `benchmarks/charts/pyproject.toml` |
| `demo-video/` | Node.js `22+`, Playwright, Remotion `v4`, FFmpeg `6.0+` | `demo-video/package.json` |

## Pre-Submission Verification

Run the unified formatting, static analysis, and test targets from the repository root:

```bash
make fmt
make lint
make test
```

If a change modifies `engine/`, run the address/undefined-behavior sanitizer suite and benchmark check:

```bash
cmake --preset asan-ubsan -S engine -B engine/build/asan-ubsan
cmake --build engine/build/asan-ubsan
ctest --test-dir engine/build/asan-ubsan --output-on-failure
```
