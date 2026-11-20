# ADR-008: Merkle Sum Tree Proof-of-Reserves and On-Chain `isSolvent`

- **Status:** Accepted
- **Date:** 2026-11-21
- **Scope:** `services/internal/reconciler/merkle.go`, `contracts/src/EscrowVault.sol`, `web/`

## 1. Context

A standard hash-only Merkle tree (`Keccak256(0x01 || Hash_L || Hash_R)`) proves **inclusion** (*"my leaf is under the root"*), but does **not** bind the sum of user liabilities to the published root:
1. Without per-node balance sums committed into every parent hash, an exchange could omit large liabilities or embed negative/overflowing balances in hidden branches while still serving a valid inclusion path to any single user.
2. An external verifier checking `EscrowVault.latestReserveRoot()` on-chain needs a trustless view comparing the root's committed total liabilities against the contract's actual ETH/ERC-20 balances.

## 2. Decision

1. **Multi-Asset Merkle Sum Tree (`services/internal/reconciler/merkle.go`):** Every leaf and every internal node carries a 4-asset integer liability vector $\vec{S} = (S_{\text{ETH}}, S_{\text{USDC}}, S_{\text{WBTC}}, S_{\text{USDT}})$ in native wei/base units (`uint256`), and every internal node hash commits to both child hashes **and** the summed child balances.
2. **Bind Total Liabilities & Solvency On-Chain (`contracts/src/ReserveAttestation.sol` inherited by `EscrowVault.sol`):** `publishReserveAttestation(merkleRoot, blockHeight, token, totalLiability)` records `latestReserveRoot`, `latestReserveBlockHeight`, and `publishedLiabilities[token]`, while `EscrowVault.isSolvent(address token)` provides a public view function verifying `vaultBalance(token) >= publishedLiabilities[token]` on-chain.
3. **Interactive Sum Verification in UI (`web/`):** The Proof-of-Reserves explorer displays per-hop sibling sums alongside sibling hashes so users verify both inclusion and non-negative total liability summation up to the published root.

## 3. Exact Specification

### 3.1 Leaf & Internal Node Commitments

For each user $u$ sorted lexicographically by `user_id`, with native non-negative balances $b_{u,a}$ ($a \in \{\text{ETH}, \text{USDC}, \text{WBTC}, \text{USDT}\}$):

- **Leaf Sum Vector:** $\vec{S}_u = (b_{u,\text{ETH}}, b_{u,\text{USDC}}, b_{u,\text{WBTC}}, b_{u,\text{USDT}})$
- **Leaf Hash (`0x00` domain prefix):**
  $$\text{Hash}_u = \text{Keccak256}\left(\texttt{0x00} \parallel \text{user\_id}_u \parallel \text{nonce}_u \parallel b_{u,\text{ETH}} \parallel b_{u,\text{USDC}} \parallel b_{u,\text{WBTC}} \parallel b_{u,\text{USDT}}\right)$$

For every internal node combining left child $(H_L, \vec{S}_L)$ and right child $(H_R, \vec{S}_R)$:

- **Parent Sum Vector (checked non-overflowing addition):**
  $$\vec{S}_{\text{parent}} = \vec{S}_L + \vec{S}_R$$
- **Parent Hash (`0x01` domain prefix):**
  $$\text{Hash}_{\text{parent}} = \text{Keccak256}\left(\texttt{0x01} \parallel H_L \parallel H_R \parallel S_{\text{ETH}} \parallel S_{\text{USDC}} \parallel S_{\text{WBTC}} \parallel S_{\text{USDT}}\right)$$
  where each summed asset balance $S_a = S_{L,a} + S_{R,a}$ is encoded as a 32-byte big-endian unsigned integer (`uint256`), matching EVM ABI encoding.

Padding for odd-length levels uses a zero-hash, zero-balance sentinel node $(0\text{x}00\dots00,\ \vec{0})$ so padding never inflates or deflates total liabilities.

### 3.2 Verifier Properties

When verifying a user inclusion path $[(H_{\text{sib},0}, \vec{S}_{\text{sib},0}), \dots, (H_{\text{sib},k-1}, \vec{S}_{\text{sib},k-1})]$:
1. All balances in the leaf and every sibling sum vector must be non-negative ($\ge 0$).
2. Recomputing the hash and summing $\vec{S}$ at each level must reproduce the published `merkle_root` **and** the published `total_liabilities` vector.
3. Tampering with any sibling balance or leaf sum alters $\vec{S}_{\text{parent}}$ and invalidates the root hash.
4. Calling `EscrowVault.isSolvent(token)` checks directly on-chain:
   $$\text{address(this).balance} \ge \text{publishedLiabilities[address(0)]} \quad (\text{for ETH})$$
   $$\text{IERC20(token).balanceOf(address(this))} \ge \text{publishedLiabilities[token]} \quad (\text{for ERC-20})$$

## 4. Alternatives Considered

1. **Standard Hash-Only Merkle Tree:** Rejected. Cannot prevent hidden balance deflation or missing-total omissions without external full-tree downloads.
2. **ZK-SNARK (Groth16 / Halo2) Proof-of-Solvency:** Deferred for future roadmap. Adds trusted setup or heavy prover latency in Go/browser for modest tree sizes, whereas a Keccak-256 Merkle Sum Tree verifies natively in browser TypeScript (`viem`) and Solidity in microseconds.

## 5. Trade-offs & Failure Modes

- **Proof Payload Size:** Including a 4-asset `SiblingSums` / `NodeSums` vector per `MerklePathStep` adds $\sim 4 \times 32\text{ B}$ of metadata per proof step (still $< 4\,\text{KB}$ for large user trees).
- **Root-vs-Liability Consistency:** `ReserveAttestation.publishReserveAttestation` couples the `merkleRoot`, `blockHeight`, `token`, and `totalLiability` in a single atomic transaction callable only by `owner()` or the separated `attester` address (never `hotSigner`; see [ADR-009](ADR-009-rolling-token-bucket-withdrawal-limiter.md)).
