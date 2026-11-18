"use client";

import { Zap } from "lucide-react";
import type { MarketSnapshot } from "@/hooks/useMarketStream";
import { formatLatencyUs, formatPrice, formatQuantity, formatTimestamp } from "@/lib/format";

interface TradeTapeProps {
  market: MarketSnapshot;
}

export function TradeTape({ market }: TradeTapeProps) {
  return (
    <section className="flex h-full min-h-0 flex-col overflow-hidden bg-[#0B0E11]">
      <div className="flex shrink-0 items-center justify-between border-b border-[#222831] bg-[#13171D] px-3 py-1.5">
        <div className="flex items-center gap-1.5">
          <h2 className="text-xs font-semibold tracking-tight text-[#EAECEF]">Market Trades</h2>
          <span className="inline-flex items-center gap-1 rounded border border-sky-400/25 bg-sky-400/10 px-1.5 py-0.5 text-[10px] font-medium text-[#38BDF8]">
            <Zap className="h-2.5 w-2.5" />
            Live
          </span>
        </div>
        <span className="text-[10px] font-medium text-[#848E9C]">Match · UTC</span>
      </div>

      <div className="grid shrink-0 grid-cols-[1.05fr_0.9fr_0.85fr_0.9fr] border-b border-[#222831] px-3 py-1 text-[10px] font-medium uppercase tracking-wider text-[#848E9C]">
        <span>Price</span>
        <span className="text-right">Size</span>
        <span className="text-right">Match</span>
        <span className="text-right">Time</span>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-1 py-0.5">
        {market.trades.slice(0, 24).map((trade) => {
          const isBuy = trade.taker_side === "BUY";
          return (
            <div
              key={`${trade.trade_id}-${trade.wal_seq}`}
              title={`Trade #${trade.trade_id} · WAL Seq #${trade.wal_seq} · Side: ${trade.taker_side}`}
              className={`grid grid-cols-[1.05fr_0.9fr_0.85fr_0.9fr] items-center rounded px-2 py-[3px] text-[11px] tabular-nums transition-colors ${
                trade.isNew
                  ? isBuy
                    ? "animate-flash-bid"
                    : "animate-flash-ask"
                  : "hover:bg-[#1A1F27]/70"
              }`}
            >
              <span className={`font-medium ${isBuy ? "text-[#0ECB81]" : "text-[#F6465D]"}`}>
                {formatPrice(trade.price, 2)}
              </span>
              <span className="text-right text-[#EAECEF]">
                {formatQuantity(trade.quantity, 4)}
              </span>
              <span className="text-right">
                <span className="rounded bg-sky-400/10 px-1 py-[1px] text-[10px] font-medium text-[#38BDF8]">
                  {formatLatencyUs(trade.latency_us, "us")}
                </span>
              </span>
              <span className="text-right text-[#848E9C]">
                {formatTimestamp(trade.executed_at).slice(0, 8)}
              </span>
            </div>
          );
        })}
      </div>
    </section>
  );
}
