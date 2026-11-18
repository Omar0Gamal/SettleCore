"use client";

import { useMemo, useState } from "react";
import type { MarketSnapshot } from "@/hooks/useMarketStream";
import { formatPrice } from "@/lib/format";

interface DepthChartProps {
  market: MarketSnapshot;
  embedded?: boolean;
}

interface DepthPoint {
  price: number;
  cumulative: number;
  side: "BUY" | "SELL";
  svgX: number;
  svgY: number;
}

export function DepthChart({ market, embedded = false }: DepthChartProps) {
  const [hovered, setHovered] = useState<DepthPoint | null>(null);

  const {
    bidPath,
    bidArea,
    askPath,
    askArea,
    points,
    minPrice,
    maxPrice,
    maxDepth,
    midX,
    totalBidDepth,
    totalAskDepth,
  } = useMemo(() => {
    const bidsRaw = [...market.bids]
      .map((b) => ({
        price: Number(b.price),
        cumulative: Number(b.cumulative),
        side: "BUY" as const,
      }))
      .sort((a, b) => a.price - b.price);

    const asksRaw = [...market.asks]
      .map((a) => ({
        price: Number(a.price),
        cumulative: Number(a.cumulative),
        side: "SELL" as const,
      }))
      .sort((a, b) => a.price - b.price);

    const allRaw = [...bidsRaw, ...asksRaw];
    const minP = allRaw[0]?.price ?? 0;
    const maxP = allRaw[allRaw.length - 1]?.price ?? 1;
    const maxD = Math.max(1, ...allRaw.map((p) => p.cumulative));

    const w = 720;
    const h = 240;
    const padX = 16;
    const padTop = 18;
    const padBot = 24;
    const usableW = w - padX * 2;
    const usableH = h - padTop - padBot;

    const toX = (p: number) =>
      maxP === minP ? w / 2 : padX + ((p - minP) / (maxP - minP)) * usableW;
    const toY = (d: number) => padTop + usableH - (d / maxD) * usableH;

    const bidsPts: DepthPoint[] = bidsRaw.map((pt) => ({
      ...pt,
      svgX: toX(pt.price),
      svgY: toY(pt.cumulative),
    }));
    const asksPts: DepthPoint[] = asksRaw.map((pt) => ({
      ...pt,
      svgX: toX(pt.price),
      svgY: toY(pt.cumulative),
    }));

    const buildStepLine = (pts: DepthPoint[]) => {
      if (pts.length === 0) return { line: "", area: "" };
      const first = pts[0];
      if (!first) return { line: "", area: "" };
      let d = `M ${first.svgX.toFixed(1)} ${first.svgY.toFixed(1)}`;
      for (let i = 1; i < pts.length; i++) {
        const prev = pts[i - 1];
        const curr = pts[i];
        if (!prev || !curr) continue;
        d += ` L ${curr.svgX.toFixed(1)} ${prev.svgY.toFixed(1)}`;
        d += ` L ${curr.svgX.toFixed(1)} ${curr.svgY.toFixed(1)}`;
      }
      const last = pts[pts.length - 1] ?? first;
      const baseY = (padTop + usableH).toFixed(1);
      const area = `${d} L ${last.svgX.toFixed(1)} ${baseY} L ${first.svgX.toFixed(1)} ${baseY} Z`;
      return { line: d, area };
    };

    const bidSvg = buildStepLine(bidsPts);
    const askSvg = buildStepLine(asksPts);
    const markNum = Number(market.markPrice);
    const computedMidX = Number.isFinite(markNum) ? toX(markNum) : w / 2;

    return {
      bidPath: bidSvg.line,
      bidArea: bidSvg.area,
      askPath: askSvg.line,
      askArea: askSvg.area,
      points: [...bidsPts, ...asksPts],
      minPrice: minP,
      maxPrice: maxP,
      maxDepth: maxD,
      midX: computedMidX,
      totalBidDepth: bidsRaw[0]?.cumulative ?? 0,
      totalAskDepth: asksRaw[asksRaw.length - 1]?.cumulative ?? 0,
    };
  }, [market.bids, market.asks, market.markPrice]);

  const baseSymbol = market.pair.split("/")[0];

  const handleMouseMove = (e: React.MouseEvent<SVGSVGElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    const relX = ((e.clientX - rect.left) / Math.max(rect.width, 1)) * 720;
    let nearest: DepthPoint | null = null;
    let bestDist = Infinity;
    for (const pt of points) {
      const dist = Math.abs(pt.svgX - relX);
      if (dist < bestDist) {
        bestDist = dist;
        nearest = pt;
      }
    }
    setHovered(nearest);
  };

  const innerContent = (
    <>
      <div className="flex shrink-0 items-center justify-between border-b border-[#222831] bg-[#13171D] px-3 py-1 text-[11px] tabular-nums">
        <span className="text-[#848E9C]">
          Cumulative Bids <strong className="font-semibold text-[#0ECB81]">{totalBidDepth.toFixed(1)}</strong> · Asks{" "}
          <strong className="font-semibold text-[#F6465D]">{totalAskDepth.toFixed(1)}</strong> {baseSymbol}
        </span>
        {hovered ? (
          <span className="rounded border border-[#222831] bg-[#1A1F27] px-2 py-0.5 text-[#EAECEF]">
            ${hovered.price.toLocaleString(undefined, { minimumFractionDigits: 2 })} ·{" "}
            <span className={hovered.side === "BUY" ? "text-[#0ECB81]" : "text-[#F6465D]"}>
              {hovered.cumulative.toFixed(2)} {baseSymbol}
            </span>
          </span>
        ) : (
          <span className="text-[#848E9C]">
            Mid <strong className="font-semibold text-[#EAECEF]">${formatPrice(market.markPrice, 2)}</strong> · Peak{" "}
            {maxDepth.toFixed(1)} {baseSymbol}
          </span>
        )}
      </div>

      <div className="relative min-h-0 flex-1 bg-[#0B0E11] p-2">
        <svg
          viewBox="0 0 720 240"
          preserveAspectRatio="none"
          onMouseMove={handleMouseMove}
          onMouseLeave={() => setHovered(null)}
          className="h-full w-full cursor-crosshair"
        >
          <defs>
            <linearGradient id="bidGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#0ECB81" stopOpacity="0.34" />
              <stop offset="100%" stopColor="#0ECB81" stopOpacity="0.02" />
            </linearGradient>
            <linearGradient id="askGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#F6465D" stopOpacity="0.34" />
              <stop offset="100%" stopColor="#F6465D" stopOpacity="0.02" />
            </linearGradient>
            <linearGradient id="midLaser" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#38BDF8" stopOpacity="0.85" />
              <stop offset="100%" stopColor="#38BDF8" stopOpacity="0.1" />
            </linearGradient>
          </defs>

          {[60, 110, 160].map((y) => (
            <line
              key={y}
              x1="16"
              y1={y}
              x2="704"
              y2={y}
              stroke="#1A1F27"
              strokeWidth="1"
            />
          ))}

          <line
            x1={midX}
            y1="12"
            x2={midX}
            y2="216"
            stroke="url(#midLaser)"
            strokeWidth="1.25"
            strokeDasharray="3 3"
          />

          {bidArea && <path d={bidArea} fill="url(#bidGrad)" />}
          {bidPath && <path d={bidPath} fill="none" stroke="#0ECB81" strokeWidth="1.75" />}

          {askArea && <path d={askArea} fill="url(#askGrad)" />}
          {askPath && <path d={askPath} fill="none" stroke="#F6465D" strokeWidth="1.75" />}

          {hovered && (
            <>
              <line
                x1={hovered.svgX}
                y1="14"
                x2={hovered.svgX}
                y2="216"
                stroke="rgba(255,255,255,0.22)"
                strokeWidth="1"
              />
              <circle
                cx={hovered.svgX}
                cy={hovered.svgY}
                r="4"
                fill={hovered.side === "BUY" ? "#0ECB81" : "#F6465D"}
                stroke="#0B0E11"
                strokeWidth="1.5"
              />
            </>
          )}

          <text
            x="16"
            y="233"
            fill="#848E9C"
            fontSize="10.5"
            fontFamily="Inter, -apple-system, BlinkMacSystemFont, sans-serif"
          >
            ${minPrice.toFixed(1)}
          </text>
          <text
            x={midX}
            y="233"
            textAnchor="middle"
            fill="#38BDF8"
            fontSize="10.5"
            fontWeight="600"
            fontFamily="Inter, -apple-system, BlinkMacSystemFont, sans-serif"
          >
            MID ${formatPrice(market.markPrice, 2)}
          </text>
          <text
            x="704"
            y="233"
            textAnchor="end"
            fill="#848E9C"
            fontSize="10.5"
            fontFamily="Inter, -apple-system, BlinkMacSystemFont, sans-serif"
          >
            ${maxPrice.toFixed(1)}
          </text>
        </svg>
      </div>
    </>
  );

  if (embedded) {
    return <div className="flex min-h-0 flex-1 flex-col">{innerContent}</div>;
  }

  return (
    <section className="flex h-full min-h-0 flex-col overflow-hidden bg-[#0B0E11]">
      {innerContent}
    </section>
  );
}
