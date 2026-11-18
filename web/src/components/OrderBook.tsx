"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, ArrowUp, ShieldAlert, ShieldCheck } from "lucide-react";
import { formatPrice, formatQuantity } from "@/lib/format";
import { playClickSfx } from "@/lib/sound";
import type { BookLevel, MarketSnapshot } from "@/hooks/useMarketStream";

interface OrderBookProps {
  market: MarketSnapshot;
  onSelectPrice: (price: string) => void;
}

type BookDisplayMode = "split" | "bids" | "asks";

interface HoverSweep {
  side: "ASK" | "BID";
  index: number;
  avgPrice: number;
  sumBase: number;
  sumQuote: number;
}

function aggregateLevels(levels: BookLevel[], tick: number, side: "ASK" | "BID"): BookLevel[] {
  if (levels.length === 0) return [];
  const buckets = new Map<number, number>();
  for (const lvl of levels) {
    const px = Number(lvl.price);
    const qty = Number(lvl.quantity);
    if (!Number.isFinite(px) || !Number.isFinite(qty)) continue;
    const bucketPx =
      side === "ASK"
        ? Math.ceil(px / tick) * tick
        : Math.floor(px / tick) * tick;
    const rounded = Number(bucketPx.toFixed(2));
    buckets.set(rounded, (buckets.get(rounded) ?? 0) + qty);
  }

  const sortedPrices = Array.from(buckets.keys()).sort((a, b) =>
    side === "ASK" ? a - b : b - a
  );
  let cum = 0;
  const out: BookLevel[] = sortedPrices.map((px) => {
    const q = buckets.get(px) ?? 0;
    cum += q;
    return {
      price: px.toFixed(8),
      quantity: q.toFixed(8),
      cumulative: cum.toFixed(8),
    };
  });

  // Asks are rendered top-down (highest at top, best ask nearest spread at bottom)
  return side === "ASK" ? out.reverse() : out;
}

export function OrderBook({ market, onSelectPrice }: OrderBookProps) {
  const [displayMode, setDisplayMode] = useState<BookDisplayMode>("split");
  const [tickChoice, setTickChoice] = useState<string>("0.50");
  const [hoverSweep, setHoverSweep] = useState<HoverSweep | null>(null);

  const prevMarkRef = useRef<number>(Number(market.markPrice));
  const [direction, setDirection] = useState<"UP" | "DOWN">("UP");

  useEffect(() => {
    const curr = Number(market.markPrice);
    if (Number.isFinite(curr) && curr !== prevMarkRef.current) {
      setDirection(curr >= prevMarkRef.current ? "UP" : "DOWN");
      prevMarkRef.current = curr;
    }
  }, [market.markPrice]);

  const tickOptions = useMemo(
    () => (market.pair === "ETH/USDC" ? ["0.50", "1.00", "2.50"] : ["5.00", "10.00", "25.00"]),
    [market.pair]
  );

  const effectiveTick = useMemo(() => {
    const num = Number(tickChoice);
    if (market.pair === "BTC/USDC" && num < 5) return 5.0;
    if (market.pair === "ETH/USDC" && num >= 5) return 0.5;
    return num > 0 ? num : 0.5;
  }, [tickChoice, market.pair]);

  const groupedAsks = useMemo(
    () => aggregateLevels(market.asks, effectiveTick, "ASK"),
    [market.asks, effectiveTick]
  );
  const groupedBids = useMemo(
    () => aggregateLevels(market.bids, effectiveTick, "BID"),
    [market.bids, effectiveTick]
  );

  const visibleAsks = useMemo(
    () => (displayMode === "split" ? groupedAsks.slice(-11) : displayMode === "asks" ? groupedAsks.slice(-16) : []),
    [groupedAsks, displayMode]
  );
  const visibleBids = useMemo(
    () => (displayMode === "split" ? groupedBids.slice(0, 11) : displayMode === "bids" ? groupedBids.slice(0, 16) : []),
    [groupedBids, displayMode]
  );

  const maxAskCum = useMemo(() => {
    const first = visibleAsks[0];
    return first ? Math.max(Number(first.cumulative), 0.0001) : 1;
  }, [visibleAsks]);

  const maxBidCum = useMemo(() => {
    const last = visibleBids[visibleBids.length - 1];
    return last ? Math.max(Number(last.cumulative), 0.0001) : 1;
  }, [visibleBids]);

  const imbalance = useMemo(() => {
    const bidVol = groupedBids[groupedBids.length - 1]
      ? Number(groupedBids[groupedBids.length - 1]!.cumulative)
      : 0;
    const askVol = groupedAsks[0] ? Number(groupedAsks[0].cumulative) : 0;
    const total = bidVol + askVol;
    if (total <= 0) return { bidPct: 49, askPct: 51 };
    const bidPct = Math.round((bidVol / total) * 100);
    return { bidPct, askPct: 100 - bidPct };
  }, [groupedBids, groupedAsks]);

  const baseSymbol = market.pair === "ETH/USDC" ? "ETH" : "BTC";

  const handleAskHover = (idx: number) => {
    // visibleAsks is highest -> best ask at bottom (length - 1)
    const slice = visibleAsks.slice(idx);
    let sumBase = 0;
    let sumQuote = 0;
    for (const lvl of slice) {
      const q = Number(lvl.quantity);
      const p = Number(lvl.price);
      sumBase += q;
      sumQuote += q * p;
    }
    setHoverSweep({
      side: "ASK",
      index: idx,
      avgPrice: sumBase > 0 ? sumQuote / sumBase : 0,
      sumBase,
      sumQuote,
    });
  };

  const handleBidHover = (idx: number) => {
    // visibleBids is best bid at top (0) -> lowest at bottom
    const slice = visibleBids.slice(0, idx + 1);
    let sumBase = 0;
    let sumQuote = 0;
    for (const lvl of slice) {
      const q = Number(lvl.quantity);
      const p = Number(lvl.price);
      sumBase += q;
      sumQuote += q * p;
    }
    setHoverSweep({
      side: "BID",
      index: idx,
      avgPrice: sumBase > 0 ? sumQuote / sumBase : 0,
      sumBase,
      sumQuote,
    });
  };

  return (
    <section className="relative flex h-full min-h-0 flex-col overflow-hidden bg-[#0B0E11]">
      {/* Header with 3-Mode Display Toggle & Tick Grouping Selector */}
      <div className="flex shrink-0 items-center justify-between border-b border-[#222831] bg-[#13171D] px-2.5 py-1.5">
        <div className="flex items-center gap-1">
          <button
            type="button"
            title="Split Order Book (Asks + Bids)"
            onClick={() => {
              playClickSfx();
              setDisplayMode("split");
            }}
            className={`btn-tactile flex h-5 w-5 cursor-pointer flex-col items-center justify-center gap-[2px] rounded border ${
              displayMode === "split"
                ? "border-[#38BDF8]/50 bg-[#1A1F27]"
                : "border-transparent hover:bg-[#1A1F27]"
            }`}
          >
            <span className="h-[3px] w-3 rounded-[1px] bg-[#F6465D]" />
            <span className="h-[3px] w-3 rounded-[1px] bg-[#0ECB81]" />
          </button>
          <button
            type="button"
            title="Bids Only"
            onClick={() => {
              playClickSfx();
              setDisplayMode("bids");
            }}
            className={`btn-tactile flex h-5 w-5 cursor-pointer flex-col items-center justify-center gap-[2px] rounded border ${
              displayMode === "bids"
                ? "border-[#0ECB81]/50 bg-[#1A1F27]"
                : "border-transparent hover:bg-[#1A1F27]"
            }`}
          >
            <span className="h-[2px] w-3 rounded-[1px] bg-[#0ECB81]" />
            <span className="h-[2px] w-3 rounded-[1px] bg-[#0ECB81]" />
            <span className="h-[2px] w-3 rounded-[1px] bg-[#0ECB81]" />
          </button>
          <button
            type="button"
            title="Asks Only"
            onClick={() => {
              playClickSfx();
              setDisplayMode("asks");
            }}
            className={`btn-tactile flex h-5 w-5 cursor-pointer flex-col items-center justify-center gap-[2px] rounded border ${
              displayMode === "asks"
                ? "border-[#F6465D]/50 bg-[#1A1F27]"
                : "border-transparent hover:bg-[#1A1F27]"
            }`}
          >
            <span className="h-[2px] w-3 rounded-[1px] bg-[#F6465D]" />
            <span className="h-[2px] w-3 rounded-[1px] bg-[#F6465D]" />
            <span className="h-[2px] w-3 rounded-[1px] bg-[#F6465D]" />
          </button>
          <h2 className="ml-1 text-xs font-semibold tracking-tight text-[#EAECEF]">Order Book</h2>
        </div>

        <select
          aria-label="Order book tick grouping"
          value={effectiveTick.toFixed(2)}
          onChange={(e) => {
            playClickSfx();
            setTickChoice(e.target.value);
          }}
          className="cursor-pointer rounded border border-[#222831] bg-[#1A1F27] px-1.5 py-0.5 text-[10px] font-medium tabular-nums text-[#EAECEF] outline-none hover:border-[#38BDF8]/40"
        >
          {tickOptions.map((opt) => (
            <option key={opt} value={opt}>
              {opt}
            </option>
          ))}
        </select>
      </div>

      {/* Column Headers */}
      <div className="grid shrink-0 grid-cols-3 border-b border-[#222831] px-2.5 py-1 text-[10px] font-medium uppercase tracking-wider text-[#848E9C]">
        <span>Price (USDC)</span>
        <span className="text-right">Size ({baseSymbol})</span>
        <span className="text-right">Total</span>
      </div>

      {/* Asks */}
      {displayMode !== "bids" && (
        <div
          onMouseLeave={() => setHoverSweep(null)}
          className="flex min-h-0 flex-1 flex-col justify-end overflow-hidden px-1 py-0.5"
        >
          {visibleAsks.map((level, idx) => {
            const depthPct = Math.min(100, (Number(level.cumulative) / maxAskCum) * 100);
            const inSweep = hoverSweep?.side === "ASK" && idx >= hoverSweep.index;
            return (
              <button
                key={`ask-${level.price}`}
                type="button"
                onMouseEnter={() => handleAskHover(idx)}
                onClick={() => {
                  playClickSfx();
                  onSelectPrice(formatPrice(level.price, 2, false));
                }}
                title="Click to populate Order Entry limit price"
                className={`group relative grid w-full cursor-pointer grid-cols-3 items-center rounded px-1.5 py-[2px] text-left text-[11px] tabular-nums transition-colors ${
                  inSweep ? "bg-[#F6465D]/15" : "hover:bg-[#1A1F27]"
                }`}
              >
                <span
                  className="pointer-events-none absolute inset-y-[1px] right-0 rounded-r bg-[#F6465D]/[0.14] transition-all duration-200 group-hover:bg-[#F6465D]/[0.24]"
                  style={{ width: `${depthPct}%` }}
                />
                <span className="relative z-10 font-medium text-[#F6465D]">
                  {formatPrice(level.price, 2)}
                </span>
                <span className="relative z-10 text-right text-[#EAECEF]">
                  {formatQuantity(level.quantity, 4)}
                </span>
                <span className="relative z-10 text-right text-[#848E9C]">
                  {formatQuantity(level.cumulative, 2)}
                </span>
              </button>
            );
          })}
        </div>
      )}

      {/* Center Directional Mid-Price Banner */}
      <div className="flex shrink-0 items-center justify-between border-y border-[#222831] bg-[#13171D] px-2.5 py-1.5">
        <div className="flex items-center gap-1.5 tabular-nums">
          <span
            className={`inline-flex items-center gap-0.5 text-sm font-bold tracking-tight ${
              direction === "UP" ? "text-[#0ECB81]" : "text-[#F6465D]"
            }`}
          >
            {formatPrice(market.markPrice, 2)}
            {direction === "UP" ? (
              <ArrowUp className="h-3.5 w-3.5" />
            ) : (
              <ArrowDown className="h-3.5 w-3.5" />
            )}
          </span>
          <span className="text-[10px] text-[#848E9C]">
            Mark ${formatPrice(market.indexPrice, 2)}
          </span>
        </div>

        <div className="flex items-center gap-1.5 tabular-nums">
          <span className="hidden text-[10px] text-[#848E9C] xl:inline">
            Spread ${market.spread}
          </span>
          {market.circuitBreakerTripped ? (
            <span className="inline-flex items-center gap-0.5 rounded bg-[#F6465D]/15 px-1.5 py-0.5 text-[9px] font-semibold text-[#F6465D]">
              <ShieldAlert className="h-2.5 w-2.5" />
              HALT
            </span>
          ) : (
            <span className="inline-flex items-center gap-0.5 rounded bg-[#0ECB81]/10 px-1.5 py-0.5 text-[9px] font-semibold text-[#0ECB81]">
              <ShieldCheck className="h-2.5 w-2.5" />
              ±10%
            </span>
          )}
        </div>
      </div>

      {/* Bids */}
      {displayMode !== "asks" && (
        <div
          onMouseLeave={() => setHoverSweep(null)}
          className="flex min-h-0 flex-1 flex-col justify-start overflow-hidden px-1 py-0.5"
        >
          {visibleBids.map((level, idx) => {
            const depthPct = Math.min(100, (Number(level.cumulative) / maxBidCum) * 100);
            const inSweep = hoverSweep?.side === "BID" && idx <= hoverSweep.index;
            return (
              <button
                key={`bid-${level.price}`}
                type="button"
                onMouseEnter={() => handleBidHover(idx)}
                onClick={() => {
                  playClickSfx();
                  onSelectPrice(formatPrice(level.price, 2, false));
                }}
                title="Click to populate Order Entry limit price"
                className={`group relative grid w-full cursor-pointer grid-cols-3 items-center rounded px-1.5 py-[2px] text-left text-[11px] tabular-nums transition-colors ${
                  inSweep ? "bg-[#0ECB81]/15" : "hover:bg-[#1A1F27]"
                }`}
              >
                <span
                  className="pointer-events-none absolute inset-y-[1px] right-0 rounded-r bg-[#0ECB81]/[0.14] transition-all duration-200 group-hover:bg-[#0ECB81]/[0.24]"
                  style={{ width: `${depthPct}%` }}
                />
                <span className="relative z-10 font-medium text-[#0ECB81]">
                  {formatPrice(level.price, 2)}
                </span>
                <span className="relative z-10 text-right text-[#EAECEF]">
                  {formatQuantity(level.quantity, 4)}
                </span>
                <span className="relative z-10 text-right text-[#848E9C]">
                  {formatQuantity(level.cumulative, 2)}
                </span>
              </button>
            );
          })}
        </div>
      )}

      {/* Hover Cumulative Sweep Summary Pill */}
      {hoverSweep && (
        <div className="pointer-events-none mx-2 mb-1 flex shrink-0 items-center justify-between rounded border border-[#38BDF8]/35 bg-[#13171D]/95 px-2 py-1 text-[10px] tabular-nums shadow-lg">
          <span className="text-[#848E9C]">
            Avg <strong className="text-[#EAECEF]">${hoverSweep.avgPrice.toFixed(2)}</strong>
          </span>
          <span className="text-[#848E9C]">
            Sum{" "}
            <strong className={hoverSweep.side === "BID" ? "text-[#0ECB81]" : "text-[#F6465D]"}>
              {hoverSweep.sumBase.toFixed(2)} {baseSymbol}
            </strong>
          </span>
          <span className="text-[#848E9C]">
            <strong className="text-[#38BDF8]">${hoverSweep.sumQuote.toLocaleString(undefined, { maximumFractionDigits: 0 })}</strong>
          </span>
        </div>
      )}

      {/* Bottom B 49% --●-- 51% S Depth Ratio Bar */}
      <div className="shrink-0 border-t border-[#222831] bg-[#13171D] px-2.5 py-1.5">
        <div className="flex items-center gap-2 text-[10px] font-semibold tabular-nums">
          <span className="text-[#0ECB81]">B {imbalance.bidPct}%</span>
          <div className="relative flex h-1.5 flex-1 items-center overflow-hidden rounded-full bg-[#1A1F27]">
            <div
              className="h-full bg-[#0ECB81] transition-all duration-300"
              style={{ width: `${imbalance.bidPct}%` }}
            />
            <div
              className="h-full bg-[#F6465D] transition-all duration-300"
              style={{ width: `${imbalance.askPct}%` }}
            />
            <span
              className="absolute top-1/2 h-2.5 w-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full border border-[#0B0E11] bg-[#EAECEF]"
              style={{ left: `${imbalance.bidPct}%` }}
            />
          </div>
          <span className="text-[#F6465D]">{imbalance.askPct}% S</span>
        </div>
      </div>
    </section>
  );
}
