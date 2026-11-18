"use client";

import { useEffect, useState } from "react";
import {
  getSimSnapshot,
  subscribeChaosEvents,
  subscribeOrderEvents,
  type CustodyTransfer,
} from "@/lib/api";
import {
  isDemoMode,
  isFrameSteppedDemo,
  mulberry32,
  subscribeDemoSeek,
} from "@/lib/demo";

export type CircuitBreakerStatus = "NORMAL" | "CANCEL_ONLY" | "HALTED (>10% DEV)";

export interface TelemetryState {
  clob_p50_us: number;
  clob_p99_us: number;
  clob_p999_us: number;
  wal_seq: number;
  active_rpc: string;
  indexer_block_lag: number;
  reorg_rollbacks_total: number;
  stuck_tx_gas_bumps: number;
  gateway_crash_rollbacks: number;
  last_aborted_order_id: string | null;
  reserves_invariant_ok: boolean;
  circuit_breaker_status: CircuitBreakerStatus;
  latest_block_height: number;
  recent_deposit_status: "CONFIRMED" | "ORPHANED / ROLLED BACK" | "PENDING";
  transfers: CustodyTransfer[];
  ws_connected: boolean;
}

const WS_URL = process.env.NEXT_PUBLIC_WS_URL ?? "ws://localhost:8080/v1/ws";

export function useTelemetryStream(): TelemetryState {
  const [state, setState] = useState<TelemetryState>(() => {
    const sim = getSimSnapshot();
    const dep = sim.transfers.find((t) => t.direction === "DEPOSIT");
    return {
      clob_p50_us: 0.18,
      clob_p99_us: 0.56,
      clob_p999_us: 0.78,
      wal_seq: sim.walSeq,
      active_rpc: sim.activeRpc,
      indexer_block_lag: 0,
      reorg_rollbacks_total: sim.reorgRollbacks,
      stuck_tx_gas_bumps: sim.gasBumps,
      gateway_crash_rollbacks: sim.gatewayCrashRollbacks,
      last_aborted_order_id: sim.lastAbortedOrderId,
      reserves_invariant_ok: sim.reservesInvariantOk,
      circuit_breaker_status: sim.circuitBreaker,
      latest_block_height: sim.blockNumber,
      recent_deposit_status:
        dep?.status === "ORPHANED"
          ? "ORPHANED / ROLLED BACK"
          : dep?.status === "PENDING"
            ? "PENDING"
            : "CONFIRMED",
      transfers: sim.transfers.map((t) => ({ ...t })),
      ws_connected: false,
    };
  });

  useEffect(() => {
    if (isDemoMode()) return;

    let ws: WebSocket | null = null;
    let disposed = false;

    try {
      ws = new WebSocket(WS_URL);
      ws.onopen = () => {
        if (disposed || !ws) return;
        setState((prev) => ({ ...prev, ws_connected: true }));
        ws.send(
          JSON.stringify({
            op: "subscribe",
            cancel_on_disconnect: false,
            channels: ["telemetry"],
          })
        );
      };
      ws.onclose = () => {
        if (!disposed) setState((prev) => ({ ...prev, ws_connected: false }));
      };
      ws.onerror = () => {
        if (!disposed) setState((prev) => ({ ...prev, ws_connected: false }));
      };
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(String(ev.data)) as Partial<TelemetryState> & { channel?: string };
          if (msg.channel === "telemetry") {
            setState((prev) => ({
              ...prev,
              clob_p50_us: msg.clob_p50_us ?? prev.clob_p50_us,
              clob_p99_us: msg.clob_p99_us ?? prev.clob_p99_us,
              clob_p999_us: msg.clob_p999_us ?? prev.clob_p999_us,
              wal_seq: msg.wal_seq ?? prev.wal_seq,
              active_rpc: msg.active_rpc ?? prev.active_rpc,
              indexer_block_lag: msg.indexer_block_lag ?? prev.indexer_block_lag,
              reorg_rollbacks_total: msg.reorg_rollbacks_total ?? prev.reorg_rollbacks_total,
              stuck_tx_gas_bumps: msg.stuck_tx_gas_bumps ?? prev.stuck_tx_gas_bumps,
              reserves_invariant_ok: msg.reserves_invariant_ok ?? prev.reserves_invariant_ok,
              circuit_breaker_status: msg.circuit_breaker_status ?? prev.circuit_breaker_status,
            }));
          }
        } catch {
          // Ignore malformed telemetry payload
        }
      };
    } catch {
      // Offline fallback remains active
    }

    return () => {
      disposed = true;
      ws?.close();
    };
  }, []);

  useEffect(() => {
    const syncFromSim = () => {
      const sim = getSimSnapshot();
      const dep = sim.transfers.find((t) => t.direction === "DEPOSIT");
      setState((prev) => ({
        ...prev,
        wal_seq: Math.max(prev.wal_seq, sim.walSeq),
        active_rpc: sim.activeRpc,
        reorg_rollbacks_total: sim.reorgRollbacks,
        stuck_tx_gas_bumps: sim.gasBumps,
        gateway_crash_rollbacks: sim.gatewayCrashRollbacks,
        last_aborted_order_id: sim.lastAbortedOrderId,
        reserves_invariant_ok: sim.reservesInvariantOk,
        circuit_breaker_status: sim.circuitBreaker,
        latest_block_height: sim.blockNumber,
        recent_deposit_status:
          dep?.status === "ORPHANED"
            ? "ORPHANED / ROLLED BACK"
            : dep?.status === "PENDING"
              ? "PENDING"
              : "CONFIRMED",
        transfers: sim.transfers.map((t) => ({ ...t })),
      }));
    };

    const unsubChaos = subscribeChaosEvents(() => syncFromSim());
    const unsubOrders = subscribeOrderEvents((res) => {
      setState((prev) => ({
        ...prev,
        wal_seq: Math.max(prev.wal_seq, res.wal_seq),
        clob_p50_us: Number((res.match_latency_ns / 1000).toFixed(2)),
      }));
    });

    const unsubSeek = subscribeDemoSeek((frame) => {
      const sim = getSimSnapshot();
      const rng = mulberry32(0x7E1E7E70 + frame);
      const jitter50 = Number((0.17 + rng() * 0.04).toFixed(2));
      const jitter99 = Number((0.52 + rng() * 0.09).toFixed(2));
      const jitter999 = Number((0.74 + rng() * 0.12).toFixed(2));
      const walStep = Math.floor(frame / 15);
      setState((prev) => ({
        ...prev,
        clob_p50_us: jitter50,
        clob_p99_us: jitter99,
        clob_p999_us: jitter999,
        wal_seq: Math.max(sim.walSeq, 148209 + walStep),
      }));
    });

    let step = 1;
    const interval = isFrameSteppedDemo()
      ? null
      : setInterval(() => {
          setState((prev) => {
            if (prev.ws_connected) return prev;
            const rng = mulberry32(0xCA11AB1E + step++);
            const jitter50 = Number((0.17 + rng() * 0.04).toFixed(2));
            const jitter99 = Number((0.52 + rng() * 0.09).toFixed(2));
            const jitter999 = Number((0.74 + rng() * 0.12).toFixed(2));
            return {
              ...prev,
              clob_p50_us: jitter50,
              clob_p99_us: jitter99,
              clob_p999_us: jitter999,
              wal_seq: prev.wal_seq + (prev.circuit_breaker_status === "NORMAL" ? 1 : 0),
            };
          });
        }, 1500);

    return () => {
      unsubChaos();
      unsubOrders();
      unsubSeek();
      if (interval) clearInterval(interval);
    };
  }, []);

  return state;
}
