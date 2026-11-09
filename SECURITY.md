# Security Policy

## Threat Model Summary

SettleCore is engineered as a hybrid exchange architecture separating a deterministic C++17 matching core, a PostgreSQL 18 double-entry ledger, and an on-chain Solidity `EscrowVault` custody contract. The security design assumes:

1. **Hot-Signer Compromise:** The `relayer` host holding `HOT_SIGNER_PRIVATE_KEY` may be compromised. On-chain custody caps exposure via per-transaction limits (`hotPerTxCap[token]`) and a continuous rolling 24-hour token-bucket rate limiter (`hotDailyCap[token]`). Any withdrawal exceeding either cap requires a second EIP-712 co-signature from `COLD_SIGNER_ROLE`. The `ATTESTER_ROLE` (which publishes Merkle Proof-of-Reserves roots) is separated from `HOT_SIGNER_ROLE` so a compromised hot signer cannot forge solvency attestations.
2. **Chain Reorganizations & RPC Instability:** An EVM fork can orphan confirmed blocks, or a primary RPC endpoint can lag or drop connections. The `watcher` validates `parentHash` lineage on every block, promotes deposits only after $N_{\text{conf}}$ canonical confirmations, and rolls back orphaned deposits atomically via `REORG_REVERSAL` double-entry debits.
3. **Concurrent Double-Spend & Unbalanced Journals:** Concurrent HTTP/WS requests cannot double-spend `available` balances due to `SELECT ... FOR UPDATE` row locks, `Idempotency-Key` uniqueness, `CHECK (available >= 0 AND locked >= 0)`, and a PostgreSQL deferred constraint trigger enforcing $\sum \text{debits} = \sum \text{credits}$ per `journal_tx_id` on an append-only `ledger_entries` table.
4. **Arithmetic Precision Drift:** Floating-point types (`float`, `double`, `float64`) are forbidden in all financial code paths. Matching uses `int64_t` (`1e8` scale) with `__int128_t` intermediate products; Go uses `shopspring/decimal`; PostgreSQL uses `NUMERIC(38, 18)` and `NUMERIC(78, 0)`.

---

## Core Invariant Guarantees (`I-1` to `I-6`)

| ID | Invariant | Enforcement Boundary |
| :---: | :--- | :--- |
| **I-1** | Double-Entry Conservation | PostgreSQL 18 deferred `SUM(debit) = SUM(credit)` constraint trigger + append-only `ledger_entries` triggers (`NO UPDATE/DELETE`) |
| **I-2** | Proof-of-Reserves Solvency | 60s `reconciler` invariant loop + Merkle **Sum** Tree root and `totalLiabilities` published on-chain (`EscrowVault.isSolvent(token)`) |
| **I-3** | Fixed-Point Precision | Compile-time integer math (`int64_t` + `__int128_t`) in C++17; `shopspring/decimal` in Go; `NUMERIC` in Postgres |
| **I-4** | Reorg Lineage Finality | Parent-hash chain continuity in `watcher` + atomic `REORG_REVERSAL` rollback |
| **I-5** | Nonce Monotonicity & Replay Immunity | Serialized relayer EOA nonces + on-chain `usedNonces[voucherNonce]` + EIP-712 signed order nonces |
| **I-6** | Deterministic WAL Replay | Single-threaded pinned C++17 CLOB + 64-byte CRC32C WAL (`SCWL`) + execution event log (`SCEV`) + `replay-cli` SHA-256 verification |

See [docs/security-and-invariants.md](docs/security-and-invariants.md) and [docs/adr/README.md](docs/adr/README.md) for formal specifications.

---

## Known Scope Boundaries & Operational Limitations

SettleCore is a reference exchange engineering system and has **not** undergone an external third-party smart-contract or cryptographic audit:

- **Local/Testnet Custody:** Default deployments target local Anvil (`chainId = 31337`) and Sepolia testnet. Production mainnet deployment requires HSM/MPC custody for `COLD_SIGNER_ROLE` and `ATTESTER_ROLE`, multi-sig ownership (`Safe`), and external security review.
- **Demo Mode:** The static demo mode (`NEXT_PUBLIC_DEMO_MODE=1`) executes a seeded client-side simulation engine when no live Gateway is connected.
- **No Production Key Management:** `.env.example` ships deterministic Anvil test keys strictly for local integration testing and chaos drills.

---

## Reporting a Vulnerability

If you discover a security issue in `contracts/`, `engine/`, `services/`, or `web/`:

1. **Do not** open a public GitHub issue with exploit details against live or testnet deployments.
2. Send a minimal reproduction (affected file paths, failing Foundry/Go/C++ test or deterministic simulation seed, and impact summary) to the repository maintainers via GitHub Security Advisories (**Security -> Advisories -> Report a vulnerability**).
3. Include any deterministic seed (for `services/internal/sim` or Foundry fuzzing) so the issue can be reproduced and turned into a permanent regression test.
