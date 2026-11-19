"use client";

import { useState } from "react";
import {
  AlertOctagon,
  ArrowRight,
  CheckCircle2,
  Clock,
  Flame,
  Fuel,
  GitBranch,
  Radio,
  RotateCcw,
  ServerCrash,
  ShieldAlert,
  ShieldCheck,
  WifiOff,
  Zap,
} from "lucide-react";
import {
  resetChaos,
  triggerFlashCrash,
  triggerGasSpike,
  triggerGatewayCrash,
  triggerReorg,
  triggerRpcFailover,
  type ChaosActionResponse,
  type CustodyTransfer,
  type MarketPair,
} from "@/lib/api";
import { getDemoNowMs } from "@/lib/demo";
import { formatQuantity, formatTimestamp, truncateHash } from "@/lib/format";
import { evaluateInvariants } from "@/lib/invariants";
import { playChaosAlertSfx, playClickSfx } from "@/lib/sound";
import type { TelemetryState } from "@/hooks/useTelemetryStream";

interface ChaosControlPanelProps {
  pair: MarketPair;
  telemetry: TelemetryState;
  expanded?: boolean;
  onToggleExpand?: () => void;
  onAfterDrill: () => void;
}

interface ChaosLogEntry {
  id: string;
  time: string;
  drill: string;
  message: string;
}

const INITIAL_CHAOS_LOGS: ChaosLogEntry[] = [
  {
    id: "boot-7",
    time: "2026-11-20T01:30:00.000Z",
    drill: "ARMED",
    message:
      "5 adversarial fault-injection drills armed (Reorg, RPC Failover, 10x Gas Spike, Flash Crash, Mid-Flight Gateway Crash).",
  },
  {
    id: "boot-6",
    time: "2026-11-20T01:29:54.000Z",
    drill: "WAL_SYNC",
    message: "WAL Snapshot #148,200 verified (Group-Commit CRC32C checksum valid, 0 drift).",
  },
  {
    id: "boot-5",
    time: "2026-11-20T01:29:41.000Z",
    drill: "VAULT",
    message:
      "Rolling 24h Token-Bucket Limiter active on EscrowVault; separate ATTESTER role verified.",
  },
  {
    id: "boot-4",
    time: "2026-11-20T01:29:28.000Z",
    drill: "LEDGER",
    message:
      "Postgres 18 Deferred Double-Entry Trigger armed; UPDATE/DELETE revoked on ledger_entries.",
  },
  {
    id: "boot-3",
    time: "2026-11-20T01:29:12.000Z",
    drill: "EVENT_LOG",
    message:
      "Settlement Execution Event Log consumer synced at seq #148,209 (0.00 ms backlog).",
  },
  {
    id: "boot-2",
    time: "2026-11-20T01:28:55.000Z",
    drill: "SOLVENCY",
    message:
      "ADR-008 Merkle Sum Tree snapshot #103 committed; EscrowVault.isSolvent(token) == true.",
  },
  {
    id: "boot-1",
    time: "2026-11-20T01:28:30.000Z",
    drill: "SIM_SUITE",
    message:
      "Deterministic 1,000-seed fault simulation suite passed (I-1..I-6 invariants green across every step).",
  },
];

export function ChaosControlPanel({
  pair,
  telemetry,
  onAfterDrill,
}: ChaosControlPanelProps) {
  const [busyDrill, setBusyDrill] = useState<string | null>(null);
  const [logs, setLogs] = useState<ChaosLogEntry[]>(INITIAL_CHAOS_LOGS);

  const invariants = evaluateInvariants(telemetry);
  const isRpcFailover = telemetry.active_rpc.includes("rpc-proxy-2");
  const isReorgTriggered = telemetry.reorg_rollbacks_total > 0;
  const isGasBumped = telemetry.stuck_tx_gas_bumps > 2;
  const isBreakerTripped = telemetry.circuit_breaker_status !== "NORMAL";
  const isGatewayCrashed = telemetry.gateway_crash_rollbacks > 0;

  const latestDeposit = telemetry.transfers.find((t) => t.direction === "DEPOSIT");
  const reorgStartBlock = latestDeposit ? Math.max(1, latestDeposit.block_number - 2) : 101;
  const reorgEndBlock = latestDeposit ? latestDeposit.block_number : 103;

  const runDrill = async (
    key: string,
    fn: () => Promise<ChaosActionResponse>,
    isReset = false
  ) => {
    setBusyDrill(key);
    try {
      const res = await fn();
      if (isReset) {
        playClickSfx();
      } else {
        playChaosAlertSfx();
      }
      setLogs((prev) => [
        {
          id: `${getDemoNowMs()}-${key}-${prev.length}`,
          time: new Date(getDemoNowMs()).toISOString(),
          drill: res.drill.toUpperCase(),
          message: res.message,
        },
        ...prev.slice(0, 11),
      ]);
      onAfterDrill();
    } finally {
      setBusyDrill(null);
    }
  };

  return (
    <div
      id="chaos-lab-panel"
      className="flex h-full min-h-0 flex-col overflow-y-auto bg-[#0B0E11] p-4"
    >
      <div className="mx-auto flex min-h-0 w-full max-w-[1520px] flex-1 flex-col gap-3.5">
        {/* Top Lab Header */}
        <div className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-b border-[#222831] pb-3">
          <div className="flex items-center gap-3">
            <div className="flex h-9 w-9 items-center justify-center rounded-lg border border-[#F6465D]/40 bg-[#F6465D]/10">
              <Flame className="h-4 w-4 text-[#F6465D]" />
            </div>
            <div>
              <div className="flex items-center gap-2">
                <h2 className="font-display text-base font-bold tracking-tight text-[#EAECEF]">
                  Chaos &amp; Systems Lab Cockpit
                </h2>
                <span className="rounded border border-[#F6465D]/35 bg-[#F6465D]/15 px-2 py-0.5 text-[10px] font-semibold uppercase text-[#F6465D]">
                  Live Adversarial Fault Injection
                </span>
              </div>
              <p className="text-xs text-[#848E9C]">
                Deterministic EVM reorg rollback, multi-RPC quorum failover, RBF gas bumping, ±10% CLOB collar, and mid-flight gateway crash recovery.
              </p>
            </div>
          </div>

          <button
            type="button"
            data-testid="chaos-reset-btn"
            disabled={busyDrill !== null}
            onClick={() => void runDrill("reset", () => resetChaos(), true)}
            className="btn-tactile inline-flex cursor-pointer items-center gap-1.5 rounded-md border border-[#0ECB81]/40 bg-[#0ECB81]/10 px-3.5 py-2 text-xs font-semibold text-[#0ECB81] hover:bg-[#0ECB81]/20 disabled:opacity-50"
          >
            <RotateCcw className="h-3.5 w-3.5" />
            Reset Normal State
          </button>
        </div>

        {/* Live 6-Invariant Verification Bar */}
        <div
          data-testid="chaos-invariants-bar"
          className="grid shrink-0 grid-cols-2 gap-2 md:grid-cols-3 xl:grid-cols-6"
        >
          {invariants.map((inv) => (
            <div
              key={inv.id}
              className={`rounded-lg border px-3 py-2 tabular-nums ${
                inv.ok
                  ? "border-[#0ECB81]/30 bg-[#13171D]"
                  : "border-[#F6465D]/55 bg-[#F6465D]/10"
              }`}
            >
              <div className="flex items-center justify-between gap-1 text-[10px]">
                <span className="font-bold text-[#EAECEF]">
                  <span className="text-[#38BDF8]">{inv.id}</span> {inv.shortName}
                </span>
                <span
                  className={`inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[9px] font-bold ${
                    inv.ok
                      ? "bg-[#0ECB81]/15 text-[#0ECB81]"
                      : "bg-[#F6465D]/20 text-[#F6465D]"
                  }`}
                >
                  {inv.ok ? "OK" : "FAIL"}
                </span>
              </div>
              <div className="mt-1 truncate text-[11px] font-semibold text-[#0ECB81]">
                {inv.valueText}
              </div>
            </div>
          ))}
        </div>

        {/* Visual EVM Block-Chain Reorg Fork Timeline + Visual Multi-RPC Failover Router */}
        <div className="grid shrink-0 gap-3.5 lg:grid-cols-12">
          {/* Visual EVM Block-Chain Reorg Fork Timeline */}
          <div className="rounded-lg border border-[#222831] bg-[#13171D] p-3.5 lg:col-span-7">
            <div className="mb-2.5 flex items-center justify-between">
              <div className="flex items-center gap-2">
                <GitBranch className="h-4 w-4 text-[#F6465D]" />
                <h3 className="text-xs font-semibold uppercase tracking-wider text-[#EAECEF]">
                  EVM Block-Chain Reorg Fork Timeline
                </h3>
              </div>
              <span
                className={`rounded px-2 py-0.5 text-[10px] font-semibold uppercase tabular-nums ${
                  isReorgTriggered
                    ? "bg-[#F6465D]/20 text-[#F6465D]"
                    : "bg-[#0ECB81]/15 text-[#0ECB81]"
                }`}
              >
                {isReorgTriggered ? "FORK DETECTED · DEPOSIT ROLLED BACK" : "CANONICAL CHAIN SYNCED"}
              </span>
            </div>

            {!isReorgTriggered ? (
              <div className="flex flex-wrap items-center gap-2 rounded-md border border-[#222831] bg-[#0B0E11] p-3.5 tabular-nums">
                {["#100", "#101", "#102"].map((blk) => (
                  <div key={blk} className="flex items-center gap-2">
                    <div className="rounded border border-[#222831] bg-[#1A1F27] px-3 py-1.5 text-xs font-semibold text-[#EAECEF]">
                      Block {blk}
                    </div>
                    <ArrowRight className="h-3.5 w-3.5 text-[#848E9C]" />
                  </div>
                ))}
                <div className="rounded border border-[#0ECB81]/45 bg-[#0ECB81]/15 px-3 py-1.5 text-xs font-semibold text-[#0ECB81]">
                  Block #103 · 5.0000 ETH Deposit (CONFIRMED 3/3)
                </div>
              </div>
            ) : (
              <div className="flex flex-col gap-2.5 rounded-md border border-[#F6465D]/35 bg-[#0B0E11] p-3.5 tabular-nums">
                <div className="flex flex-wrap items-center gap-3">
                  <div className="shrink-0 rounded border border-[#38BDF8]/40 bg-[#1A1F27] px-3 py-2 text-xs font-semibold text-[#EAECEF]">
                    Anchor #100
                  </div>
                  <div className="flex flex-1 flex-col gap-2">
                    {/* Top Red Orphaned Fork */}
                    <div className="flex flex-wrap items-center gap-2 rounded border border-[#F6465D]/40 bg-[#F6465D]/10 px-3 py-1.5">
                      <span className="text-[10px] font-bold uppercase text-[#F6465D]">
                        Orphaned Fork:
                      </span>
                      <span className="text-xs text-[#EAECEF]">#101 → #102 → #103 (5.0 ETH)</span>
                      <span className="ml-auto rounded bg-[#F6465D]/25 px-2 py-0.5 text-[10px] font-bold text-[#F6465D]">
                        ORPHANED • DEPOSIT REVERSED
                      </span>
                    </div>

                    {/* Bottom Emerald Canonical Fork */}
                    <div className="flex flex-wrap items-center gap-2 rounded border border-[#0ECB81]/40 bg-[#0ECB81]/10 px-3 py-1.5">
                      <span className="text-[10px] font-bold uppercase text-[#0ECB81]">
                        Canonical Chain:
                      </span>
                      <span className="text-xs text-[#EAECEF]">
                        #101&apos; → #102&apos; → #103&apos; → #104&apos;
                      </span>
                      <span className="ml-auto rounded bg-[#0ECB81]/20 px-2 py-0.5 text-[10px] font-bold text-[#0ECB81]">
                        CANONICAL HEAD
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            )}
          </div>

          {/* Visual Multi-RPC Failover Router */}
          <div className="rounded-lg border border-[#222831] bg-[#13171D] p-3.5 lg:col-span-5">
            <div className="mb-2.5 flex items-center justify-between">
              <div className="flex items-center gap-2">
                <Radio className={`h-4 w-4 ${isRpcFailover ? "text-amber-400" : "text-[#0ECB81]"}`} />
                <h3 className="text-xs font-semibold uppercase tracking-wider text-[#EAECEF]">
                  Multi-RPC Quorum Failover Router
                </h3>
              </div>
              <span className="text-[10px] font-medium tabular-nums text-[#848E9C]">
                0 Missed Blocks · {telemetry.active_rpc}
              </span>
            </div>

            <div className="space-y-2 rounded-md border border-[#222831] bg-[#0B0E11] p-3 text-xs tabular-nums">
              <div className="flex items-center justify-between">
                <span className="rounded border border-[#38BDF8]/35 bg-[#38BDF8]/10 px-2.5 py-1 font-semibold text-[#38BDF8]">
                  Watcher / Indexer
                </span>
                <ArrowRight className="h-3.5 w-3.5 text-[#848E9C]" />
                <div
                  className={`flex items-center gap-2 rounded border px-2.5 py-1 font-semibold ${
                    isRpcFailover
                      ? "border-[#F6465D]/45 bg-[#F6465D]/10 text-[#F6465D] line-through"
                      : "border-[#0ECB81]/45 bg-[#0ECB81]/15 text-[#0ECB81]"
                  }`}
                >
                  rpc-proxy-1 :8545 {isRpcFailover ? "(SEVERED)" : "(ACTIVE)"}
                </div>
              </div>

              <div className="flex items-center justify-between pl-6">
                <span className="text-[10px] text-[#848E9C]">
                  {isRpcFailover
                    ? "Health check failed -> hot-swapped in 184 ms"
                    : "Standby hot-spare synced to block head"}
                </span>
                <div
                  className={`rounded border px-2.5 py-1 font-semibold ${
                    isRpcFailover
                      ? "border-[#0ECB81]/50 bg-[#0ECB81]/15 text-[#0ECB81]"
                      : "border-[#222831] bg-[#1A1F27] text-[#848E9C]"
                  }`}
                >
                  rpc-proxy-2 :8546 {isRpcFailover ? "(FAILOVER ACTIVE)" : "(STANDBY)"}
                </div>
              </div>
            </div>
          </div>
        </div>

        {/* 5 Adversarial Chaos Drill Cards */}
        <div className="grid shrink-0 gap-3 md:grid-cols-2 xl:grid-cols-5">
          {/* Drill 1: 3-Block Reorg */}
          <button
            type="button"
            data-testid="chaos-trigger-reorg"
            disabled={busyDrill !== null}
            onClick={() => runDrill("reorg", () => triggerReorg(3))}
            className={`btn-tactile group flex cursor-pointer flex-col justify-between gap-2.5 rounded-lg border p-3.5 text-left disabled:opacity-50 ${
              isReorgTriggered
                ? "border-[#F6465D]/55 bg-[#F6465D]/10"
                : "border-[#222831] bg-[#13171D] hover:border-[#F6465D]/45 hover:bg-[#1A1F27]"
            }`}
          >
            <div className="flex w-full items-center justify-between gap-1">
              <span className="inline-flex items-center gap-1.5 text-xs font-bold text-[#EAECEF]">
                <GitBranch className="h-4 w-4 text-[#F6465D]" />
                3-Block Reorg
              </span>
              <span
                className={`rounded px-1.5 py-0.5 text-[10px] font-bold uppercase tabular-nums ${
                  isReorgTriggered
                    ? "bg-[#F6465D]/20 text-[#F6465D]"
                    : "bg-[#1A1F27] text-[#848E9C]"
                }`}
              >
                {isReorgTriggered ? `ROLLED (${telemetry.reorg_rollbacks_total})` : "ARMED"}
              </span>
            </div>

            <p className="text-[11px] leading-snug text-[#848E9C]">
              Orphan blocks #101..103 via parent-hash mismatch &amp; reverse orphaned 5.0 ETH deposit credit.
            </p>

            <div className="flex w-full items-center justify-between border-t border-[#222831] pt-2 text-[11px] tabular-nums">
              <span className="text-[#848E9C]">
                Rollbacks: <strong className="text-[#EAECEF]">{telemetry.reorg_rollbacks_total}</strong>
              </span>
              <span className="font-semibold text-[#F6465D] group-hover:underline">
                Trigger Reorg →
              </span>
            </div>
          </button>

          {/* Drill 2: Primary RPC Kill */}
          <button
            type="button"
            data-testid="chaos-trigger-rpc"
            disabled={busyDrill !== null}
            onClick={() =>
              runDrill("rpc", () => triggerRpcFailover(isRpcFailover ? "restore" : "kill"))
            }
            className={`btn-tactile group flex cursor-pointer flex-col justify-between gap-2.5 rounded-lg border p-3.5 text-left disabled:opacity-50 ${
              isRpcFailover
                ? "border-amber-400/55 bg-amber-400/10"
                : "border-[#222831] bg-[#13171D] hover:border-amber-400/45 hover:bg-[#1A1F27]"
            }`}
          >
            <div className="flex w-full items-center justify-between gap-1">
              <span className="inline-flex items-center gap-1.5 text-xs font-bold text-[#EAECEF]">
                <WifiOff className="h-4 w-4 text-amber-400" />
                {isRpcFailover ? "Restore RPC" : "Kill Primary RPC"}
              </span>
              <span
                className={`rounded px-1.5 py-0.5 text-[10px] font-bold uppercase ${
                  isRpcFailover
                    ? "bg-amber-400/20 text-amber-300"
                    : "bg-[#0ECB81]/15 text-[#0ECB81]"
                }`}
              >
                {isRpcFailover ? "FAILOVER" : "OK"}
              </span>
            </div>

            <p className="text-[11px] leading-snug text-[#848E9C]">
              Sever rpc-proxy-1 health score &amp; hot-swap watcher to rpc-proxy-2 (&lt;500ms, 0 missed blocks).
            </p>

            <div className="flex w-full items-center justify-between border-t border-[#222831] pt-2 text-[11px] tabular-nums">
              <span className="text-[#848E9C]">
                Node: <strong className="text-[#EAECEF]">{isRpcFailover ? "proxy-2" : "proxy-1"}</strong>
              </span>
              <span className="font-semibold text-amber-300 group-hover:underline">
                {isRpcFailover ? "Restore →" : "Execute Drill →"}
              </span>
            </div>
          </button>

          {/* Drill 3: 10x EIP-1559 Gas Spike */}
          <button
            type="button"
            data-testid="chaos-trigger-gas"
            disabled={busyDrill !== null}
            onClick={() => runDrill("gas", () => triggerGasSpike(10))}
            className={`btn-tactile group flex cursor-pointer flex-col justify-between gap-2.5 rounded-lg border p-3.5 text-left disabled:opacity-50 ${
              isGasBumped
                ? "border-[#38BDF8]/55 bg-[#38BDF8]/10"
                : "border-[#222831] bg-[#13171D] hover:border-[#38BDF8]/45 hover:bg-[#1A1F27]"
            }`}
          >
            <div className="flex w-full items-center justify-between gap-1">
              <span className="inline-flex items-center gap-1.5 text-xs font-bold text-[#EAECEF]">
                <Fuel className="h-4 w-4 text-[#38BDF8]" />
                10x Gas Spike
              </span>
              <span className="rounded bg-[#38BDF8]/15 px-1.5 py-0.5 text-[10px] font-bold uppercase tabular-nums text-[#38BDF8]">
                BUMPS: {telemetry.stuck_tx_gas_bumps}
              </span>
            </div>

            <p className="text-[11px] leading-snug text-[#848E9C]">
              Spike baseFeePerGas 10x &amp; RBF-bump stuck outbox withdrawal (+15% fee at same EOA nonce).
            </p>

            <div className="flex w-full items-center justify-between border-t border-[#222831] pt-2 text-[11px] tabular-nums">
              <span className="text-[#848E9C]">
                Gas Bumps: <strong className="text-[#38BDF8]">{telemetry.stuck_tx_gas_bumps}</strong>
              </span>
              <span className="font-semibold text-[#38BDF8] group-hover:underline">
                Execute Drill →
              </span>
            </div>
          </button>

          {/* Drill 4: -15% Flash Crash Circuit Breaker */}
          <button
            type="button"
            data-testid="chaos-trigger-flash-crash"
            disabled={busyDrill !== null}
            onClick={() => runDrill("flash", () => triggerFlashCrash(pair, 1500))}
            className={`btn-tactile group flex cursor-pointer flex-col justify-between gap-2.5 rounded-lg border p-3.5 text-left disabled:opacity-50 ${
              isBreakerTripped
                ? "border-[#F6465D]/55 bg-[#F6465D]/10"
                : "border-[#222831] bg-[#13171D] hover:border-[#F6465D]/45 hover:bg-[#1A1F27]"
            }`}
          >
            <div className="flex w-full items-center justify-between gap-1">
              <span className="inline-flex items-center gap-1.5 text-xs font-bold text-[#EAECEF]">
                <Zap className="h-4 w-4 text-[#F6465D]" />
                -15% Flash Crash
              </span>
              <span
                className={`rounded px-1.5 py-0.5 text-[10px] font-bold uppercase ${
                  isBreakerTripped
                    ? "bg-[#F6465D]/20 text-[#F6465D]"
                    : "bg-[#0ECB81]/15 text-[#0ECB81]"
                }`}
              >
                {isBreakerTripped ? "HALTED" : "ARMED"}
              </span>
            </div>

            <p className="text-[11px] leading-snug text-[#848E9C]">
              Sweep {pair} -1500 bps through book to trip ±10% C++17 collar into CANCEL_ONLY mode.
            </p>

            <div className="flex w-full items-center justify-between border-t border-[#222831] pt-2 text-[11px] tabular-nums">
              <span className="text-[#848E9C]">
                Collar:{" "}
                <strong className={isBreakerTripped ? "text-[#F6465D]" : "text-[#0ECB81]"}>
                  {isBreakerTripped ? "HALTED" : "NORMAL"}
                </strong>
              </span>
              <span className="font-semibold text-[#F6465D] group-hover:underline">
                Execute Drill →
              </span>
            </div>
          </button>

          {/* Drill 5: Kill Gateway Mid-Flight */}
          <button
            type="button"
            data-testid="chaos-trigger-gateway-crash"
            disabled={busyDrill !== null}
            onClick={() => runDrill("gateway-crash", () => triggerGatewayCrash())}
            className={`btn-tactile group flex cursor-pointer flex-col justify-between gap-2.5 rounded-lg border p-3.5 text-left disabled:opacity-50 ${
              isGatewayCrashed
                ? "border-amber-400/55 bg-amber-400/10"
                : "border-[#222831] bg-[#13171D] hover:border-amber-400/45 hover:bg-[#1A1F27]"
            }`}
          >
            <div className="flex w-full items-center justify-between gap-1">
              <span className="inline-flex items-center gap-1.5 text-xs font-bold text-[#EAECEF]">
                <ServerCrash className="h-4 w-4 text-amber-400" />
                Kill Gateway Mid-Flight
              </span>
              <span
                className={`rounded px-1.5 py-0.5 text-[10px] font-bold uppercase tabular-nums ${
                  isGatewayCrashed
                    ? "bg-amber-400/20 text-amber-300"
                    : "bg-[#1A1F27] text-[#848E9C]"
                }`}
              >
                {isGatewayCrashed ? `REC (${telemetry.gateway_crash_rollbacks})` : "ARMED"}
              </span>
            </div>

            <p className="text-[11px] leading-snug text-[#848E9C]">
              SIGKILL between Pre-Trade DB Reserve &amp; WAL append; recovery rolls back RESERVED → ABORTED_UNSUBMITTED.
            </p>

            <div className="flex w-full items-center justify-between border-t border-[#222831] pt-2 text-[11px] tabular-nums">
              <span className="text-[#848E9C]">
                Recovered:{" "}
                <strong className="text-[#0ECB81]">{telemetry.gateway_crash_rollbacks}</strong>
              </span>
              <span className="font-semibold text-amber-300 group-hover:underline">
                Inject Crash →
              </span>
            </div>
          </button>
        </div>

        {/* Full-Height Bottom Grid: Custody Watchlist + Chaos Telemetry Log (Zero L5 Gap) */}
        <div className="grid min-h-0 flex-1 gap-3.5 lg:grid-cols-12">
          <div className="flex min-h-0 flex-col overflow-hidden rounded-lg border border-[#222831] bg-[#13171D] lg:col-span-8">
            <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-[#222831] px-4 py-2.5">
              <div className="flex items-center gap-2">
                <ShieldCheck className="h-4 w-4 text-[#38BDF8]" />
                <h3 className="text-xs font-semibold uppercase tracking-wider text-[#EAECEF]">
                  On-Chain Custody &amp; Deposit Finality Watchlist
                </h3>
              </div>

              {latestDeposit && (
                <div className="flex items-center gap-2 rounded border border-[#222831] bg-[#0B0E11] px-2.5 py-1 text-[11px] tabular-nums">
                  <span className="text-[#848E9C]">
                    Deposit #{latestDeposit.block_number} ({formatQuantity(latestDeposit.amount, 4)}{" "}
                    {latestDeposit.asset})
                  </span>
                  {latestDeposit.status === "ORPHANED" ? (
                    <span
                      data-testid="deposit-status-badge"
                      className="inline-flex items-center gap-1 rounded border border-[#F6465D]/45 bg-[#F6465D]/15 px-2 py-0.5 text-[10px] font-bold text-[#F6465D]"
                    >
                      <AlertOctagon className="h-3 w-3" />
                      ORPHANED / ROLLED BACK (Reorg #{reorgStartBlock}..{reorgEndBlock})
                    </span>
                  ) : latestDeposit.status === "PENDING" ? (
                    <span
                      data-testid="deposit-status-badge"
                      className="inline-flex items-center gap-1 rounded border border-[#38BDF8]/30 bg-[#38BDF8]/10 px-2 py-0.5 text-[10px] font-semibold text-[#38BDF8]"
                    >
                      <Clock className="h-3 w-3" />
                      PENDING ({latestDeposit.confirmations}/{latestDeposit.required_confirmations} Blocks)
                    </span>
                  ) : (
                    <span
                      data-testid="deposit-status-badge"
                      className="inline-flex items-center gap-1 rounded border border-[#0ECB81]/35 bg-[#0ECB81]/15 px-2 py-0.5 text-[10px] font-semibold text-[#0ECB81]"
                    >
                      <CheckCircle2 className="h-3 w-3" />
                      CONFIRMED ({latestDeposit.confirmations}/{latestDeposit.required_confirmations} Blocks)
                    </span>
                  )}
                </div>
              )}
            </div>

            <div className="min-h-0 flex-1 overflow-auto bg-[#0B0E11]">
              <table className="w-full text-left text-xs tabular-nums">
                <thead className="sticky top-0 z-10 bg-[#0B0E11]">
                  <tr className="border-b border-[#222831] text-[10px] font-medium uppercase tracking-wider text-[#848E9C]">
                    <th className="px-4 py-2">Direction</th>
                    <th className="px-3 py-2">Asset</th>
                    <th className="px-3 py-2 text-right">Amount</th>
                    <th className="px-3 py-2">Tx Hash</th>
                    <th className="px-3 py-2 text-right">Block</th>
                    <th className="px-4 py-2">Finality Status</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[#222831]/60">
                  {telemetry.transfers.map((tx: CustodyTransfer) => (
                    <tr key={tx.id} className="hover:bg-[#13171D]/70">
                      <td className="px-4 py-2">
                        <span
                          className={`rounded px-1.5 py-0.5 text-[10px] font-semibold ${
                            tx.direction === "DEPOSIT"
                              ? "bg-[#0ECB81]/15 text-[#0ECB81]"
                              : "bg-[#38BDF8]/15 text-[#38BDF8]"
                          }`}
                        >
                          {tx.direction}
                        </span>
                      </td>
                      <td className="px-3 py-2 font-semibold text-[#EAECEF]">{tx.asset}</td>
                      <td className="px-3 py-2 text-right text-[#EAECEF]">
                        {formatQuantity(tx.amount, 4, true)}
                      </td>
                      <td className="px-3 py-2 text-[#848E9C]">{truncateHash(tx.tx_hash)}</td>
                      <td className="px-3 py-2 text-right text-[#EAECEF]">#{tx.block_number}</td>
                      <td className="px-4 py-2">
                        {tx.status === "ORPHANED" ? (
                          <span className="inline-flex items-center gap-1 rounded border border-[#F6465D]/40 bg-[#F6465D]/15 px-2 py-0.5 text-[10px] font-semibold text-[#F6465D]">
                            ORPHANED / ROLLED BACK (Reorg #{Math.max(1, tx.block_number - 2)}..
                            {tx.block_number})
                          </span>
                        ) : tx.status === "CONFIRMED" ? (
                          <span className="inline-flex items-center gap-1 rounded border border-[#0ECB81]/30 bg-[#0ECB81]/15 px-2 py-0.5 text-[10px] font-medium text-[#0ECB81]">
                            CONFIRMED ({tx.confirmations}/{tx.required_confirmations} Blocks)
                            {tx.gas_bump_count ? ` · ${tx.gas_bump_count} bump` : ""}
                          </span>
                        ) : (
                          <span className="inline-flex items-center gap-1 rounded border border-[#38BDF8]/30 bg-[#38BDF8]/15 px-2 py-0.5 text-[10px] font-medium text-[#38BDF8]">
                            {tx.status} ({tx.signing_tier ?? "PENDING"})
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {/* Pinned Recovery Summary Footer */}
            <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-t border-[#222831] bg-[#13171D] px-4 py-2 text-[11px] tabular-nums text-[#848E9C]">
              <span>
                Pre-Trade Recovery:{" "}
                <strong className="text-[#EAECEF]">
                  {telemetry.last_aborted_order_id
                    ? `Order #${telemetry.last_aborted_order_id} ABORTED_UNSUBMITTED`
                    : "0 orphaned RESERVED orders"}
                </strong>
              </span>
              <span>
                WAL Sequence:{" "}
                <strong className="text-[#38BDF8]">#{telemetry.wal_seq.toLocaleString()}</strong>
              </span>
            </div>
          </div>

          {/* Live Chaos Audit Log */}
          <div className="flex min-h-0 flex-col overflow-hidden rounded-lg border border-[#222831] bg-[#13171D] lg:col-span-4">
            <div className="flex shrink-0 items-center justify-between border-b border-[#222831] px-4 py-2.5">
              <div className="flex items-center gap-2">
                <ShieldAlert className="h-4 w-4 text-[#F6465D]" />
                <h3 className="text-xs font-semibold uppercase tracking-wider text-[#EAECEF]">
                  Chaos Telemetry Log
                </h3>
              </div>
              <span className="text-[10px] text-[#848E9C]">{logs.length} Events</span>
            </div>
            <div className="min-h-0 flex-1 space-y-2 overflow-y-auto bg-[#0B0E11] p-3 text-[11px] tabular-nums">
              {logs.map((entry) => (
                <div
                  key={entry.id}
                  className="rounded border border-[#222831] bg-[#13171D] px-2.5 py-2 leading-snug"
                >
                  <div className="flex items-center justify-between text-[10px]">
                    <span
                      className={`font-bold ${
                        entry.drill === "REORG" || entry.drill === "FLASH-CRASH"
                          ? "text-[#F6465D]"
                          : entry.drill === "RESET" || entry.drill === "SIM_SUITE"
                            ? "text-[#0ECB81]"
                            : entry.drill === "GATEWAY-CRASH"
                              ? "text-amber-300"
                              : "text-[#38BDF8]"
                      }`}
                    >
                      {entry.drill}
                    </span>
                    <span className="text-[#848E9C]">
                      {formatTimestamp(entry.time).slice(0, 8)} UTC
                    </span>
                  </div>
                  <p className="mt-1 text-[#EAECEF]">{entry.message}</p>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
