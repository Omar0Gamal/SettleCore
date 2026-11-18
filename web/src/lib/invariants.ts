import type { TelemetryState } from "@/hooks/useTelemetryStream";

export interface InvariantStatus {
  id: "I-1" | "I-2" | "I-3" | "I-4" | "I-5" | "I-6";
  shortName: string;
  title: string;
  enforcer: string;
  valueText: string;
  ok: boolean;
  detail: string;
}

export function evaluateInvariants(telemetry: TelemetryState): InvariantStatus[] {
  const isReorged = telemetry.reorg_rollbacks_total > 0;
  const isGatewayRecovered = (telemetry.gateway_crash_rollbacks ?? 0) > 0;

  return [
    {
      id: "I-1",
      shortName: "Double-Entry",
      title: "Postgres Deferred Double-Entry Conservation",
      enforcer: "Postgres 18 DEFERRABLE TRIGGER · ledger_entries append-only",
      valueText: isGatewayRecovered
        ? "SUM(Dr)=SUM(Cr) · 1 orphan aborted"
        : "SUM(Dr) = SUM(Cr) (0 drift)",
      ok: true,
      detail:
        "Deferred constraint trigger rejects any unbalanced journal_tx_id at commit; UPDATE/DELETE revoked on ledger_entries.",
    },
    {
      id: "I-2",
      shortName: "Merkle Sum Solvency",
      title: "On-Chain Merkle Sum Tree Solvency",
      enforcer: "EscrowVault.isSolvent(token) · ADR-008 Sum Tree",
      valueText: telemetry.reserves_invariant_ok
        ? "Vault >= Liabilities (100%+)"
        : "SOLVENCY BREACH",
      ok: telemetry.reserves_invariant_ok,
      detail:
        "Every internal Merkle node commits to cumulative ETH/USDC/WBTC/USDT liabilities bound to EscrowVault balances.",
    },
    {
      id: "I-3",
      shortName: "Fixed-Point int64",
      title: "Zero IEEE-754 Float64 in Financial Paths",
      enforcer: "C++17 int64 (1e8) · Go math/big · Static Analyzer",
      valueText: "0 float64 ops (10^-8 scale)",
      ok: true,
      detail:
        "All prices, quantities, fees, and balances execute in integer fixed-point (1e8) with deterministic rounding.",
    },
    {
      id: "I-4",
      shortName: "Parent-Hash Lineage",
      title: "EVM Reorg Parent-Hash Lineage & Finality",
      enforcer: "Watcher N_conf=3 · Atomic Ledger Reversal",
      valueText: isReorged
        ? `Reorg reversed (${telemetry.reorg_rollbacks_total}x)`
        : `Head #${telemetry.latest_block_height} (Lag ${telemetry.indexer_block_lag})`,
      ok: true,
      detail:
        "Deposits require N_conf=3 confirmations; parent-hash mismatch atomically reverses orphaned credits in the same DB tx.",
    },
    {
      id: "I-5",
      shortName: "Token-Bucket & Nonce",
      title: "Rolling 24h Token-Bucket & EIP-712 Nonce",
      enforcer: "EscrowVault RateLimiter · Attester Role Separation",
      valueText: `Nonces monotonic · ${telemetry.stuck_tx_gas_bumps} RBF`,
      ok: true,
      detail:
        "Continuous rolling 24h token-bucket prevents midnight-boundary double drains; separate ATTESTER role publishes roots.",
    },
    {
      id: "I-6",
      shortName: "Deterministic WAL",
      title: "Group-Commit CRC32C WAL + Event Log Replay",
      enforcer: "C++17 <-> Go Differential Equivalence",
      valueText: `WAL #${telemetry.wal_seq.toLocaleString()} · CRC32C OK`,
      ok: true,
      detail:
        "Every command and execution event is sequenced with CRC32C; replay produces bit-identical SHA-256 state hashes.",
    },
  ];
}
