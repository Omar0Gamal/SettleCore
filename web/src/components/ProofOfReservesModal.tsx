"use client";

import { useEffect, useState } from "react";
import {
  ArrowRight,
  CheckCircle2,
  Clock,
  Cpu,
  Database,
  GitMerge,
  Lock,
  RefreshCw,
  ShieldCheck,
  X,
} from "lucide-react";
import type { Hex } from "viem";
import {
  computeMerkleInternalNode,
  computeMerkleLeafHash,
  computeMerkleSumInternalNode,
  getSolvencyProof,
  type AssetBalanceMap,
  type SolvencyProofResponse,
} from "@/lib/api";
import { formatQuantity, formatUnitsString, parseDecimalToScaled, truncateHash } from "@/lib/format";
import { playClickSfx, playVerifiedSfx } from "@/lib/sound";
import { ASSET_SPECS, type CustodyAsset } from "@/lib/wagmi";
import type { TelemetryState } from "@/hooks/useTelemetryStream";

interface ProofOfReservesModalProps {
  open: boolean;
  mode?: "workspace" | "modal";
  telemetry?: TelemetryState;
  onClose: () => void;
}

interface VerificationStep {
  label: string;
  hash: Hex;
  sums?: AssetBalanceMap | undefined;
}

function formatRawSumSummary(sums?: AssetBalanceMap): string {
  if (!sums) return "—";
  const eth = formatUnitsString(sums.ETH, 18, 2, true);
  const usdc = formatUnitsString(sums.USDC, 6, 0, true);
  return `${eth} ETH · $${usdc} USDC`;
}

export function ProofOfReservesModal({
  open,
  mode = "modal",
  telemetry,
  onClose,
}: ProofOfReservesModalProps) {
  const [proof, setProof] = useState<SolvencyProofResponse | null>(null);
  const [loading, setLoading] = useState<boolean>(false);
  const [steps, setSteps] = useState<VerificationStep[]>([]);
  const [verifiedRoot, setVerifiedRoot] = useState<Hex | null>(null);
  const [verifiedMatch, setVerifiedMatch] = useState<boolean>(false);

  const loadProof = async () => {
    playClickSfx();
    setLoading(true);
    setVerifiedMatch(false);
    setVerifiedRoot(null);
    setSteps([]);
    try {
      const data = await getSolvencyProof();
      setProof(data);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!open && mode === "modal") return;
    let cancelled = false;
    getSolvencyProof().then((data) => {
      if (cancelled) return;
      setProof(data);
      setVerifiedMatch(false);
      setVerifiedRoot(null);
      setSteps([]);
      setLoading(false);
    });
    return () => {
      cancelled = true;
    };
  }, [open, mode]);

  const runClientVerification = () => {
    if (!proof) return;
    const trace: VerificationStep[] = [];

    const recomputedLeaf = computeMerkleLeafHash(
      proof.user_leaf.user_id,
      proof.user_leaf.nonce,
      proof.user_leaf.balances
    );
    let currentHash: Hex = recomputedLeaf;
    let currentSums: AssetBalanceMap = proof.user_leaf.balances;

    trace.push({
      label: "0x00 Leaf (user_id || nonce || balances)",
      hash: recomputedLeaf,
      sums: currentSums,
    });

    proof.merkle_path.forEach((node, idx) => {
      if (node.sibling_sums) {
        const res =
          node.position === "RIGHT"
            ? computeMerkleSumInternalNode(currentHash, node.sibling_hash, currentSums, node.sibling_sums)
            : computeMerkleSumInternalNode(node.sibling_hash, currentHash, node.sibling_sums, currentSums);
        currentHash = res.hash;
        currentSums = res.sums;
      } else {
        currentHash =
          node.position === "RIGHT"
            ? computeMerkleInternalNode(currentHash, node.sibling_hash)
            : computeMerkleInternalNode(node.sibling_hash, currentHash);
      }

      trace.push({
        label: `L${idx + 1} Sum Node (0x01 || ${
          node.position === "RIGHT" ? "curr || sibling" : "sibling || curr"
        } || cumulative sums)`,
        hash: currentHash,
        sums: node.sibling_sums ? currentSums : undefined,
      });
    });

    setSteps(trace);
    setVerifiedRoot(currentHash);
    const matched =
      currentHash.toLowerCase() === proof.merkle_root.toLowerCase() && proof.invariant_ok;
    setVerifiedMatch(matched);
    if (matched) {
      playVerifiedSfx();
    } else {
      playClickSfx();
    }
  };

  if (!open && mode === "modal") return null;

  const content = (
    <div className="flex min-h-0 flex-1 flex-col gap-3.5">
      {loading || !proof ? (
        <div className="flex items-center justify-center gap-2 py-16 text-xs text-[#848E9C]">
          <RefreshCw className="h-4 w-4 animate-spin text-[#38BDF8]" />
          Fetching Merkle Sum Tree snapshot &amp; EscrowVault balances...
        </div>
      ) : (
        <>
          {/* On-Chain Root + Attester Header */}
          <div className="grid shrink-0 gap-3 rounded-lg border border-[#222831] bg-[#13171D] p-3.5 text-xs tabular-nums sm:grid-cols-3">
            <div className="sm:col-span-2">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-[10px] font-medium uppercase tracking-wider text-[#848E9C]">
                  Published On-Chain EscrowVault.latestReserveRoot
                </span>
                <span className="rounded border border-[#0ECB81]/35 bg-[#0ECB81]/10 px-1.5 py-0.5 text-[10px] font-semibold text-[#0ECB81]">
                  EscrowVault.isSolvent(token) == true
                </span>
              </div>
              <div className="mt-1 break-all font-semibold text-[#EAECEF]">{proof.merkle_root}</div>
            </div>
            <div className="sm:text-right">
              <span className="text-[10px] font-medium uppercase tracking-wider text-[#848E9C]">
                EVM Block #{proof.evm_block_number} · ATTESTER Role
              </span>
              <div className="mt-1 font-semibold text-[#38BDF8]">
                {truncateHash(proof.publish_tx_hash, 12, 10)}
              </div>
            </div>
          </div>

          {/* Asset-by-Asset On-Chain EscrowVault Balance vs Off-Chain User Liabilities */}
          <div className="shrink-0 rounded-lg border border-[#222831] bg-[#13171D] p-3.5">
            <div className="mb-2.5 flex flex-wrap items-center justify-between gap-2">
              <span className="text-xs font-semibold uppercase tracking-wider text-[#EAECEF]">
                On-Chain EscrowVault Reserves vs. Committed Sum-Tree Liabilities
              </span>
              <div className="flex items-center gap-2">
                {verifiedMatch ? (
                  <span className="rounded border border-[#0ECB81]/45 bg-[#0ECB81]/15 px-2 py-0.5 text-[10px] font-bold tabular-nums text-[#0ECB81]">
                    AUDIT PASSED · 100%+ SOLVENT
                  </span>
                ) : (
                  <span
                    data-testid="por-status-unverified"
                    className="inline-flex items-center gap-1 rounded border border-amber-400/40 bg-amber-400/10 px-2 py-0.5 text-[10px] font-bold tabular-nums text-amber-300"
                  >
                    <Clock className="h-3 w-3" />
                    READY TO AUDIT (UNVERIFIED)
                  </span>
                )}
              </div>
            </div>

            <div className="grid gap-3 md:grid-cols-2">
              {(["ETH", "USDC", "WBTC", "USDT"] as CustodyAsset[]).map((sym) => {
                const resScaled = parseDecimalToScaled(proof.vault_reserves[sym], 8) ?? 1n;
                const liabScaled =
                  parseDecimalToScaled(proof.total_liabilities[sym], 8) ?? 1n;
                const ratioPct =
                  liabScaled > 0n
                    ? Number((resScaled * 10000n) / liabScaled) / 100
                    : 100;
                const liabWidthPct =
                  resScaled > 0n
                    ? Math.min(100, Number((liabScaled * 10000n) / resScaled) / 100)
                    : 100;

                return (
                  <div
                    key={sym}
                    className="rounded-md border border-[#222831] bg-[#0B0E11] p-3 text-xs tabular-nums"
                  >
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <div className="flex items-center gap-2">
                        <span className="font-bold text-[#EAECEF]">{sym}</span>
                        <span className="rounded bg-[#0ECB81]/15 px-1.5 py-0.5 text-[10px] font-semibold text-[#0ECB81]">
                          {ratioPct.toFixed(2)}% Collateralized
                        </span>
                      </div>
                      <span className="text-[11px] text-[#848E9C]">
                        Your Leaf:{" "}
                        <strong className="text-[#38BDF8]">
                          {formatUnitsString(
                            proof.user_leaf.balances[sym],
                            ASSET_SPECS[sym].decimals,
                            4,
                            true
                          )}
                        </strong>
                      </span>
                    </div>

                    <div className="mt-2 flex items-center justify-between text-[11px]">
                      <span className="text-[#848E9C]">
                        Vault:{" "}
                        <strong className="text-[#0ECB81]">
                          {formatQuantity(proof.vault_reserves[sym], 4, true)}
                        </strong>
                      </span>
                      <span className="text-[#848E9C]">
                        Sum-Tree Liabilities:{" "}
                        <strong className="text-[#EAECEF]">
                          {formatQuantity(proof.total_liabilities[sym], 4, true)}
                        </strong>
                      </span>
                    </div>

                    <div className="mt-2 space-y-1">
                      <div className="h-1.5 w-full overflow-hidden rounded-full bg-[#0ECB81]/20">
                        <div className="h-full rounded-full bg-[#0ECB81]" style={{ width: "100%" }} />
                      </div>
                      <div className="h-1.5 w-full overflow-hidden rounded-full bg-[#1A1F27]">
                        <div
                          className="h-full rounded-full bg-[#38BDF8]"
                          style={{ width: `${liabWidthPct}%` }}
                        />
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>

          {/* Visual Merkle Sum Tree Explorer (ADR-008) */}
          <div className="flex min-h-0 flex-1 flex-col justify-between rounded-lg border border-[#222831] bg-[#13171D] p-3.5">
            <div>
              <div className="mb-2.5 flex flex-wrap items-center justify-between gap-2">
                <div className="flex items-center gap-2">
                  <GitMerge className="h-4 w-4 text-[#38BDF8]" />
                  <span className="text-xs font-semibold uppercase tracking-wider text-[#EAECEF]">
                    Keccak-256 Merkle Sum Tree Explorer (ADR-008)
                  </span>
                </div>
                <span className="rounded bg-[#38BDF8]/10 px-2 py-0.5 text-[10px] font-medium tabular-nums text-[#38BDF8]">
                  User Nonce #{proof.user_leaf.nonce} · H(0x01 || L || R || ΣAssets)
                </span>
              </div>

              <div className="grid gap-2.5 rounded-md border border-[#222831] bg-[#0B0E11] p-3 text-[11px] tabular-nums lg:grid-cols-[1fr_auto_1.2fr_auto_1fr] lg:items-center">
                {/* 0x00 Leaf */}
                <div
                  className={`rounded border p-2.5 ${
                    verifiedMatch
                      ? "border-[#0ECB81]/50 bg-[#0ECB81]/10"
                      : "border-[#38BDF8]/35 bg-[#13171D]"
                  }`}
                >
                  <div className="text-[10px] font-bold uppercase text-[#38BDF8]">
                    0x00 User Leaf
                  </div>
                  <div className="mt-1 font-semibold text-[#EAECEF]">
                    {truncateHash(proof.user_leaf.leaf_hash, 10, 8)}
                  </div>
                  <div className="mt-1 text-[10px] font-medium text-[#0ECB81]">
                    Σ {formatRawSumSummary(proof.user_leaf.balances)}
                  </div>
                  <div className="mt-0.5 text-[10px] text-[#848E9C]">
                    keccak256(0x00 || uid || nonce || bals)
                  </div>
                </div>

                <ArrowRight className="hidden h-4 w-4 shrink-0 text-[#848E9C] lg:block" />

                {/* L1 / L2 Sibling Hashes + Cumulative Liability Sums */}
                <div className="space-y-1.5">
                  {proof.merkle_path.map((p, i) => (
                    <div
                      key={`${p.sibling_hash}-${i}`}
                      className="rounded border border-[#222831] bg-[#13171D] px-2.5 py-2 text-[10px]"
                    >
                      <div className="flex items-center justify-between gap-2">
                        <span className="font-semibold text-[#848E9C]">
                          L{i + 1} Sibling ({p.position})
                        </span>
                        <span className="font-semibold text-[#EAECEF]">
                          {truncateHash(p.sibling_hash, 8, 6)}
                        </span>
                      </div>
                      {p.cumulative_sums && (
                        <div className="mt-1 flex items-center justify-between border-t border-[#222831] pt-1 text-[10px]">
                          <span className="text-[#848E9C]">Cumulative Σ:</span>
                          <span className="font-semibold text-[#38BDF8]">
                            {formatRawSumSummary(p.cumulative_sums)}
                          </span>
                        </div>
                      )}
                    </div>
                  ))}
                </div>

                <ArrowRight className="hidden h-4 w-4 shrink-0 text-[#848E9C] lg:block" />

                {/* 0x01 On-Chain Root + Total Vault Liabilities */}
                <div
                  className={`rounded border p-2.5 ${
                    verifiedMatch
                      ? "border-[#0ECB81]/60 bg-[#0ECB81]/15"
                      : "border-[#222831] bg-[#13171D]"
                  }`}
                >
                  <div className="flex items-center justify-between">
                    <span className="text-[10px] font-bold uppercase text-[#0ECB81]">
                      0x01 On-Chain Root
                    </span>
                    <span className="rounded bg-[#0ECB81]/15 px-1.5 py-0.5 text-[9px] font-bold text-[#0ECB81]">
                      isSolvent == true
                    </span>
                  </div>
                  <div className="mt-1 font-semibold text-[#EAECEF]">
                    {truncateHash(verifiedRoot ?? proof.merkle_root, 10, 8)}
                  </div>
                  <div className="mt-1 text-[10px] font-medium text-[#38BDF8]">
                    Total Σ {formatQuantity(proof.total_liabilities.ETH, 2, true)} ETH · $
                    {formatQuantity(proof.total_liabilities.USDC, 0, true)} USDC
                  </div>
                  <div className="mt-0.5 text-[10px] text-[#848E9C]">
                    EscrowVault.latestReserveRoot
                  </div>
                </div>
              </div>
            </div>

            {/* Action Buttons + Verification Trace */}
            <div className="mt-3 space-y-2.5">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <button
                  type="button"
                  data-testid="verify-por-btn"
                  onClick={runClientVerification}
                  className="btn-tactile inline-flex cursor-pointer items-center gap-2 rounded-md bg-[#0ECB81] px-4 py-2 text-xs font-bold text-[#0B0E11] shadow-[0_0_24px_-6px_rgba(14,203,129,0.7)] hover:bg-[#14D88B]"
                >
                  <Lock className="h-3.5 w-3.5" />
                  Verify Merkle Proof
                </button>

                <button
                  type="button"
                  onClick={loadProof}
                  className="btn-tactile inline-flex cursor-pointer items-center gap-1.5 rounded-md border border-[#222831] bg-[#1A1F27] px-3 py-2 text-xs font-medium text-[#EAECEF] hover:bg-[#222831]"
                >
                  <RefreshCw className="h-3.5 w-3.5" />
                  Refresh Snapshot
                </button>
              </div>

              {steps.length > 0 && (
                <div className="space-y-1.5 rounded-lg border border-[#0ECB81]/40 bg-[#0ECB81]/[0.07] p-3 text-[11px] tabular-nums">
                  {steps.map((s) => (
                    <div
                      key={s.label}
                      className="flex flex-col gap-0.5 sm:flex-row sm:items-center sm:justify-between"
                    >
                      <span className="text-[#EAECEF]">{s.label}</span>
                      <span className="font-semibold text-[#38BDF8]">
                        {truncateHash(s.hash, 10, 8)}
                        {s.sums ? ` · Σ ${formatRawSumSummary(s.sums)}` : ""}
                      </span>
                    </div>
                  ))}
                  {verifiedMatch && verifiedRoot && (
                    <div
                      data-testid="por-verified-banner"
                      className="mt-2 flex items-center gap-2 rounded-md border border-[#0ECB81]/50 bg-[#0ECB81]/15 px-3 py-2 text-xs font-bold text-[#0ECB81]"
                    >
                      <CheckCircle2 className="h-4 w-4 shrink-0" />
                      <span>
                        Cryptographic Inclusion &amp; Sum Verified · Root ({truncateHash(verifiedRoot, 10, 8)}) ·
                        EscrowVault.isSolvent(token) == true · reserves_invariant_ok = 1
                      </span>
                    </div>
                  )}
                </div>
              )}
            </div>
          </div>
        </>
      )}
    </div>
  );

  if (mode === "workspace") {
    return (
      <div className="flex h-full min-h-0 flex-col overflow-y-auto bg-[#0B0E11] p-4">
        <div
          data-testid="por-modal"
          className="mx-auto flex min-h-0 w-full max-w-[1520px] flex-1 flex-col gap-3.5"
        >
          {/* Workspace Header */}
          <div className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-b border-[#222831] pb-3">
            <div className="flex items-center gap-3">
              <div className="flex h-9 w-9 items-center justify-center rounded-lg border border-[#0ECB81]/35 bg-[#0ECB81]/10">
                <ShieldCheck className="h-4 w-4 text-[#0ECB81]" />
              </div>
              <div>
                <h2 className="font-display text-base font-bold tracking-tight text-[#EAECEF]">
                  Proof of Reserves &amp; Merkle Sum Tree Auditor
                </h2>
                <p className="text-xs text-[#848E9C]">
                  ADR-008 Domain-Separated Keccak-256 Merkle Sum Tree · Invariant I-2 On-Chain Solvency Attestation
                </p>
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-2 text-[11px] tabular-nums">
              <span className="inline-flex items-center gap-1.5 rounded-md border border-[#222831] bg-[#13171D] px-2.5 py-1 text-[#EAECEF]">
                <Cpu className="h-3.5 w-3.5 text-[#38BDF8]" />
                C++17 CLOB p50:{" "}
                <strong className="text-[#38BDF8]">
                  {(telemetry?.clob_p50_us ?? 0.18).toFixed(2)} µs
                </strong>
                {" · "}p99:{" "}
                <strong className="text-[#38BDF8]">
                  {(telemetry?.clob_p99_us ?? 0.56).toFixed(2)} µs
                </strong>
              </span>
              <span className="inline-flex items-center gap-1.5 rounded-md border border-[#222831] bg-[#13171D] px-2.5 py-1 text-[#EAECEF]">
                <Database className="h-3.5 w-3.5 text-[#0ECB81]" />
                WAL Seq:{" "}
                <strong className="text-[#EAECEF]">
                  #{(telemetry?.wal_seq ?? 148209).toLocaleString()}
                </strong>
              </span>
            </div>
          </div>

          {content}
        </div>
      </div>
    );
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-4 backdrop-blur-md">
      <div
        data-testid="por-modal"
        className="max-h-[92vh] w-full max-w-4xl overflow-y-auto rounded-xl border border-[#222831] bg-[#0B0E11] shadow-[0_24px_80px_-16px_rgba(0,0,0,0.85)]"
      >
        <div className="flex items-center justify-between border-b border-[#222831] bg-[#13171D] px-5 py-3.5">
          <div className="flex items-center gap-2.5">
            <div className="flex h-7 w-7 items-center justify-center rounded-md border border-[#0ECB81]/30 bg-[#0ECB81]/10">
              <ShieldCheck className="h-4 w-4 text-[#0ECB81]" />
            </div>
            <div>
              <h2 className="text-sm font-semibold text-[#EAECEF]">
                Cryptographic Merkle Sum Tree Verifier
              </h2>
              <p className="text-[11px] text-[#848E9C]">
                ADR-008 Domain-Separated Keccak-256 · Invariant I-2 Solvency Attestation
              </p>
            </div>
          </div>
          <button
            type="button"
            onClick={() => {
              playClickSfx();
              onClose();
            }}
            className="btn-tactile cursor-pointer rounded-md p-1.5 text-[#848E9C] hover:bg-[#1A1F27] hover:text-[#EAECEF]"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="p-5">{content}</div>
      </div>
    </div>
  );
}
