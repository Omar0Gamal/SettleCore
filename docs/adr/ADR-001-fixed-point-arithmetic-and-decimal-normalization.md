# ADR-001: Fixed-Point Arithmetic and Multi-Decimal Asset Normalization

- **Status:** Accepted
- **Date:** 2026-11-20
- **Scope:** `contracts/`, `engine/`, `services/`, `web/`

## 1. Context

SettleCore clears and settles spot trades across heterogeneous on-chain assets with differing native decimal precisions:
- `ETH` (native asset, 18 decimals, $10^{18}$ wei per ETH)
- `WBTC` (ERC-20, 8 decimals, $10^{8}$ satoshi per WBTC)
- `USDC` (ERC-20, 6 decimals, $10^{6}$ base units per USDC)
- `USDT` (non-standard ERC-20, 6 decimals, $10^{6}$ base units per USDT)

Supported trading pairs are `ETH/USDC` and `BTC/USDC`. Using IEEE-754 binary floating-point (`float`, `double`, `float64`) anywhere in order pricing, quantity matching, fee computation, or ledger settlement introduces non-associative rounding drift ($0.1 + 0.2 \neq 0.3$), breaking the double-entry conservation invariant ($\sum \text{debits} = \sum \text{credits}$) and causing Merkle Proof-of-Reserves mismatches.

Simultaneously, running arbitrary-precision big-integer arithmetic (`uint256` or `shopspring/decimal`) inside the inner loop of the C++17 Central Limit Order Book (CLOB) incurs heap allocations or multi-word software division overhead on every fill.

## 2. Decision

1. **Zero Floating-Point Policy:** `float`, `double`, and `float64` are prohibited across all financial code paths (`engine/`, `services/`, `contracts/`). `sqlc.yaml` overrides every PostgreSQL `NUMERIC` type to `github.com/shopspring/decimal.Decimal`.
2. **Normalized 8-Decimal Fixed-Point Internal CLOB (`1e8` Scale):** The C++17 matching engine represents all prices and base quantities as signed 64-bit integers (`int64_t`) scaled by $S = 10^8$ (`100_000_000` internal ticks = `1.00000000` whole unit).
3. **128-Bit Intermediate Notional Product:** Quote notional calculation ($\text{price} \times \text{quantity} / 10^8$) and basis-point fee calculation use hardware `__int128_t` before downscaling to prevent 64-bit overflow.
4. **Canonical PostgreSQL 18 Storage:** Ledger balances and journal entries are stored as `NUMERIC(38, 18)` (normalized human decimal units with 18 fractional digits) alongside exact native on-chain base units (`NUMERIC(78, 0)`) at the custody boundary.

## 3. Exact Specification

### 3.1 Asset Scaling Matrix

Let $D_{\text{chain}}$ denote an asset's native on-chain decimal places and $D_{\text{clob}} = 8$ denote the C++17 CLOB internal scale.

| Asset | Type | $D_{\text{chain}}$ | 1 Whole Unit On-Chain (`uint256`) | 1 Whole Unit in CLOB (`int64_t`) | Conversion Factor ($10^{D_{\text{chain}} - D_{\text{clob}}}$) | Minimum CLOB Lot Step |
| :--- | :--- | :---: | :---: | :---: | :---: | :---: |
| `ETH` | Native | 18 | $10^{18}$ wei | $10^8$ ticks | $\times / \div 10^{10}$ | $10^{-8}$ ETH ($10^{10}$ wei) |
| `WBTC` | ERC-20 | 8 | $10^8$ sat | $10^8$ ticks | $\times / \div 10^0$ (1:1) | $10^{-8}$ WBTC ($1$ sat) |
| `USDC` | ERC-20 | 6 | $10^6$ units | $10^8$ ticks | $\times / \div 10^{-2}$ (lot step = $10^2$ ticks) | $10^{-6}$ USDC ($100$ ticks) |
| `USDT` | ERC-20 | 6 | $10^6$ units | $10^8$ ticks | $\times / \div 10^{-2}$ (lot step = $10^2$ ticks) | $10^{-6}$ USDT ($100$ ticks) |

For 6-decimal quote tokens (`USDC`, `USDT`), order prices and settled quote totals are constrained by a tick/lot modulus of $100$ internal units ($10^{-6}$ whole units) so every settled trade maps bijectively to an integer number of on-chain token base units with zero unwithdrawable fractional dust.

### 3.2 Overflow Bounds and `__int128_t` Intermediate Math

The maximum value of a signed 64-bit integer is:

$$\text{INT64\_MAX} = 2^{63} - 1 = 9,223,372,036,854,775,807 \approx 9.223372 \times 10^{18}$$

At scale $S = 10^8$:
- **Maximum representable single order price or quantity:** $92,233,720,368.54775807$ whole units ($\approx 92.23\text{ billion}$ USDC or BTC).
- **Hard Ingress Cap:** The Go Gateway rejects any order with $P \le 0$, $Q \le 0$, $P > 10^{15}$ ($10,000,000.00000000$), or $Q > 10^{15}$ ($10,000,000.00000000$).

When matching a fill of base quantity $Q_{\text{fill}} \in [1, 10^{15}]$ at price $P_{\text{maker}} \in [1, 10^{15}]$, the unscaled product is:

$$P_{\text{maker}} \times Q_{\text{fill}} \le 10^{30} > \text{INT64\_MAX} \ (9.22 \times 10^{18})$$

Therefore, the C++17 engine computes quote notional and maker/taker fees strictly via 128-bit integer arithmetic (`__int128_t`, max $\approx 1.70 \times 10^{38}$):

```cpp
// Exact quote notional in 1e8 scale (floor rounding favors buyer reserve safety)
inline int64_t compute_quote_notional(int64_t price_1e8, int64_t qty_1e8) noexcept {
    __int128_t prod = static_cast<__int128_t>(price_1e8) * static_cast<__int128_t>(qty_1e8);
    return static_cast<int64_t>(prod / 100'000'000LL);
}

// Fee in 1e8 scale given fee_bps (1 bps = 0.01% = 1 / 10,000), ceiling-rounded to exchange fee account
inline int64_t compute_fee_ceil(int64_t notional_1e8, int64_t fee_bps) noexcept {
    __int128_t num = static_cast<__int128_t>(notional_1e8) * static_cast<__int128_t>(fee_bps);
    return static_cast<int64_t>((num + 9'999LL) / 10'000LL);
}
```

### 3.3 Dust & Price-Improvement Refund Rule

When a buyer submits a `LIMIT` buy order at price $P_{\text{limit}}$ for quantity $Q$, the Go ledger locks:

$$L_{\text{quote}} = \left\lfloor \frac{P_{\text{limit}} \times Q}{10^8} \right\rfloor + F_{\text{max}}$$

If the order matches against resting asks at $P_{\text{maker}} < P_{\text{limit}}$, the settlement worker debits the actual executed quote amount $\lfloor P_{\text{maker}} \times Q_{\text{fill}} / 10^8 \rfloor + F_{\text{actual}}$ from `locked` and atomically releases the remaining price-improvement delta back to the buyer's `available` balance when the order reaches a terminal state (`FILLED`, `CANCELED`, or `IOC_EXPIRED`).

## 4. Alternatives Considered

1. **IEEE-754 `double` (`float64`):** Rejected. A `double` has only 53 bits of significand ($\approx 15.95$ decimal digits), causing silent precision loss above $90,071,992.54740992$ ticks and non-deterministic rounding across CPU architectures.
2. **256-Bit Integers (`uint256_t`) Inside the C++17 CLOB:** Rejected. Increases `OrderNode` cache footprint from 64 bytes (1 cache line) to 128+ bytes and replaces single-cycle 64-bit ALU comparisons with multi-word comparisons on every price-level traversal.
3. **Decimal128 (`_Decimal128` / IEEE 754-2008):** Rejected due to lack of hardware instruction support on x86_64 consumer/server CPUs and poor C ABI interoperability with Go `cgo`.

## 5. Trade-offs & Failure Modes

- **Sub-Satoshi ETH Dust Upon Deposit:** Because `ETH` has 18 decimals on-chain and the CLOB operates at 8 decimals ($10^{10}$ wei step), an on-chain deposit whose wei amount is not a multiple of $10^{10}$ wei has its full 18-decimal precision recorded in PostgreSQL `NUMERIC(38, 18)` (`accounts.available`), while only multiples of $10^{-8}$ ETH are tradable in the CLOB. Full 18-decimal precision remains withdrawable on-chain.
- **Strict Boundary Validation:** Any C ABI call passing negative or out-of-range `int64_t` values returns `SC_ERR_INVALID_NUMERIC_BOUND` before mutating engine state or appending to the WAL.
