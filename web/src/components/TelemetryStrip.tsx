"use client";

import { Activity, Cpu, Database, Radio, ShieldAlert, ShieldCheck } from "lucide-react";
import type { TelemetryState } from "@/hooks/useTelemetryStream";

interface TelemetryStripProps {
  telemetry: TelemetryState;
}

export function TelemetryStrip({ telemetry }: TelemetryStripProps) {
  const isFailover = telemetry.active_rpc.includes("rpc-proxy-2");
  const isBreakerHalted = telemetry.circuit_breaker_status !== "NORMAL";

  return (
    <footer
      data-testid="telemetry-strip"
      className="flex h-7 shrink-0 items-center justify-between border-t border-[#222831] bg-[#13171D] px-4 text-[11px] tabular-nums"
    >
      <div className="flex items-center gap-4">
        <span className="inline-flex items-center gap-1.5 text-[#EAECEF]">
          <Cpu className="h-3 w-3 text-[#38BDF8]" />
          <span className="text-[#848E9C]">CLOB</span>
          <span>
            p50 <strong className="font-semibold text-[#38BDF8]">{telemetry.clob_p50_us.toFixed(2)} µs</strong>
          </span>
          <span className="text-[#474D57]">·</span>
          <span>
            p99 <strong className="font-semibold text-[#38BDF8]">{telemetry.clob_p99_us.toFixed(2)} µs</strong>
          </span>
          <span className="text-[#474D57]">·</span>
          <span>
            p99.9 <strong className="font-semibold text-[#EAECEF]">{telemetry.clob_p999_us.toFixed(2)} µs</strong>
          </span>
        </span>

        <span className="hidden rounded border border-sky-400/25 bg-sky-400/10 px-1.5 py-0.5 text-[10px] font-medium text-[#38BDF8] sm:inline">
          Zero float64
        </span>

        <span className="hidden items-center gap-1 text-[#848E9C] md:inline-flex">
          <Database className="h-3 w-3 text-[#848E9C]" />
          WAL <strong className="font-semibold text-[#EAECEF]">#{telemetry.wal_seq.toLocaleString()}</strong>
        </span>
      </div>

      <div className="flex items-center gap-4">
        <span className="inline-flex items-center gap-1.5">
          <Radio className={`h-3 w-3 ${isFailover ? "text-amber-400" : "text-[#0ECB81]"}`} />
          <span className="text-[#848E9C]">RPC</span>
          <strong className={`font-semibold ${isFailover ? "text-amber-300" : "text-[#0ECB81]"}`}>
            {telemetry.active_rpc}
          </strong>
        </span>

        <span className="hidden text-[#848E9C] lg:inline">
          Lag <strong className="font-semibold text-[#EAECEF]">{telemetry.indexer_block_lag}</strong>
        </span>

        <span className="text-[#848E9C]">
          Reorgs{" "}
          <strong
            className={`font-semibold ${
              telemetry.reorg_rollbacks_total > 0 ? "text-[#F6465D]" : "text-[#EAECEF]"
            }`}
          >
            {telemetry.reorg_rollbacks_total}
          </strong>
        </span>

        <span className="hidden text-[#848E9C] sm:inline">
          Gas Bumps <strong className="font-semibold text-[#38BDF8]">{telemetry.stuck_tx_gas_bumps}</strong>
        </span>

        <span className="inline-flex items-center gap-1">
          {telemetry.reserves_invariant_ok ? (
            <ShieldCheck className="h-3 w-3 text-[#0ECB81]" />
          ) : (
            <ShieldAlert className="h-3 w-3 text-[#F6465D]" />
          )}
          <span className="text-[#848E9C]">Reserves</span>
          <strong
            className={`font-semibold ${
              telemetry.reserves_invariant_ok ? "text-[#0ECB81]" : "text-[#F6465D]"
            }`}
          >
            {telemetry.reserves_invariant_ok ? "OK (1)" : "BREACH (0)"}
          </strong>
        </span>

        <span
          className={`inline-flex items-center gap-1 rounded px-2 py-0.5 text-[10px] font-semibold ${
            isBreakerHalted
              ? "border border-[#F6465D]/40 bg-[#F6465D]/15 text-[#F6465D]"
              : "border border-[#0ECB81]/30 bg-[#0ECB81]/10 text-[#0ECB81]"
          }`}
        >
          <Activity className="h-3 w-3" />
          {telemetry.circuit_breaker_status}
        </span>
      </div>
    </footer>
  );
}
