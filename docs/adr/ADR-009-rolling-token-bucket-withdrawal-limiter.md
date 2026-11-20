# ADR-009: Rolling Token-Bucket Withdrawal Limiter and Role Separation

- **Status:** Accepted
- **Date:** 2026-11-21
- **Scope:** `contracts/src/EscrowVault.sol`, `contracts/src/RateLimited.sol`, `contracts/src/ReserveAttestation.sol`, `contracts/test/EscrowVault.invariant.t.sol`, `services/internal/evm/`

## 1. Context

Tiered hot/cold custody in `contracts/src/EscrowVault.sol` must bound worst-case asset outflow if `HOT_SIGNER_PRIVATE_KEY` on the online `relayer` host is compromised, while isolating withdrawal signing from reserve solvency attestation:

1. **Sliding-Window Rate Enforcement:** A naive calendar-day bucket (`uint256 day = block.timestamp / 1 days`) permits an attacker with a compromised hot key to withdraw $100\%$ of `hotDailyCap[token]` at `23:59:50` UTC and another $100\%$ at `00:00:10` UTC—draining **$2\times$ the daily hot cap in 20 seconds** across the UTC midnight boundary.
2. **Separation of Hot Signing and Solvency Attestation:** Because the threat model assumes `hotSigner` may be compromised on the online `relayer` host, `hotSigner` must never be permitted to call `publishReserveRoot` or `publishReserveAttestation`, which would otherwise allow a compromised hot signer to both drain funds up to the hot cap and publish fraudulent solvency roots.
3. **OpenZeppelin Contracts `v5.6.1` Scope:** The pinned dependency in `contracts/lib/openzeppelin-contracts` is OpenZeppelin Contracts `v5.6.1` (providing `SafeERC20`, `EIP712`, `ECDSA`, `ReentrancyGuard`, and `Ownable`), so the continuous rolling rate limiter is implemented directly as a compact, auditable contract module.

## 2. Decision

1. **Continuous Linear-Decay Token Bucket (`contracts/src/RateLimited.sol`):** Implement a continuous rolling 24-hour (`1 days = 86,400` seconds) token-bucket rate limiter inherited by `EscrowVault.sol` (keeping `EscrowVault.sol` at `128` LOC).
2. **Dedicated Attester Role (`contracts/src/ReserveAttestation.sol`):** Restrict `publishReserveRoot` and `publishReserveAttestation` to `owner()` or the dedicated `attester` address (configured via `setAttester(address)`), explicitly rejecting `hotSigner` with `Unauthorized()`.
3. **Modular Vault Architecture:** Isolate `RateLimited.sol` and `ReserveAttestation.sol` as focused abstract base contracts so `EscrowVault.sol` stays compact (`128` LOC) with `100%` line/function coverage under Foundry.

## 3. Exact Specification

### 3.1 Continuous Rolling 24-Hour Token-Bucket Formula (`RateLimited.sol`)

For each allowlisted token $t$ with configured 24-hour capacity $C_t = \text{hotDailyCap}[t]$ and window $W = 1\text{ days} = 86,400\,\text{s}$:

Let `prevUsed = rollingHotUsed[t]` and `elapsed = block.timestamp - lastHotTimestamp[t]`:

1. **Linear Decay of Prior Outflow (`_currentHotUsed` / `currentRollingHotUsed`):**
   $$\text{decay} = \begin{cases} \text{prevUsed} & \text{if } \text{elapsed} \ge W \\ \left\lfloor \dfrac{\text{prevUsed} \cdot \text{elapsed}}{W} \right\rfloor & \text{if } \text{elapsed} < W \end{cases}$$
   $$\text{currentUsed} = \text{prevUsed} > \text{decay} \ ? \ (\text{prevUsed} - \text{decay}) : 0$$
2. **Tier Decision (`_consumeHotQuota`):**
   A withdrawal of `amount` requires `coldSigner` EIP-712 co-signature (`requiresCold = true`) if and only if:
   $$\text{amount} > \text{hotPerTxCap}[t] \quad \lor \quad \text{currentUsed} \ge C_t \quad \lor \quad \text{amount} > C_t - \text{currentUsed}$$
3. **State Update (Hot-Only Path):**
   When `requiresCold == false`, the vault updates:
   $$\text{rollingHotUsed}[t] = \text{currentUsed} + \text{amount}, \qquad \text{lastHotTimestamp}[t] = \text{block.timestamp}$$
   Cold co-signed withdrawals bypass and do not consume the hot token-bucket allowance.

### 3.2 Midnight-Boundary Burst Immunity

At `23:59:50` UTC, withdrawing $100\%$ of $C_t$ sets `rollingHotUsed[t] = C_t`. Twenty seconds later at `00:00:10` UTC ($\text{elapsed} = 20\,\text{s}$):

$$\text{decay} = \left\lfloor C_t \cdot \frac{20}{86,400} \right\rfloor \approx 0.00023\,C_t \implies \text{currentUsed} \approx 0.99977\,C_t$$

Only $\sim 0.023\%$ of $C_t$ has refilled—**any hot-only withdrawal exceeding that 20-second trickle reverts with `InvalidColdSignature()`** unless co-signed by `coldSigner`.

### 3.3 Foundry Invariant & Boundary Verification

`contracts/test/EscrowVault.t.sol` and `contracts/test/EscrowVault.invariant.t.sol` explicitly verify:
- **Midnight-Boundary Burst Check (`test_Rolling24hCapPreventsMidnightBoundaryDoubleDrain`):** Draining the cap immediately before an `86,400`-second boundary rollover and attempting another hot withdrawal immediately after reverts without `coldSigner`.
- **Role Isolation (`test_CompromisedHotSignerCannotPublishReserveRoot`):** `publishReserveRoot` / `publishReserveAttestation` called by `hotSigner` always reverts with `Unauthorized()`.
- **Sliding-Window Invariant (`invariant_hotOutflowNeverExceedsCapWithoutColdSig`):** Across stateful fuzzing (`256` runs, `8,192` calls), total hot-only outflow never exceeds the rolling cap without `coldSigner`.

## 4. Alternatives Considered

1. **Discrete Calendar-Day Buckets (`block.timestamp / 1 days`):** Rejected. Allows $2\times$ daily outflow across a midnight boundary.
2. **Fixed Ring Buffer of Hourly Checkpoints (24 Slots per Token):** Rejected. Costs up to 24 `SSTORE`/`SLOAD` slots per withdrawal, bloating `EscrowVault.sol` gas and LOC beyond the `< 150` LOC complexity budget.

## 5. Trade-offs & Failure Modes

- **Integer Truncation Favoring Safety:** In `decay = (prevUsed * elapsed) / 1 days`, Solidity integer division rounds *down* `decay` by at most 1 wei per step (which slightly *under-decays* `prevUsed`, conservatively erring toward requiring cold co-signing).
- **Timestamp Manipulation Margin:** On Ethereum PoS (`12s` slots), validator timestamp drift is bounded, making linear refill over `86,400s` insensitive to single-slot jitter.
