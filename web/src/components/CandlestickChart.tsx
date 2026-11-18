"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import {
  CandlestickSeries,
  ColorType,
  CrosshairMode,
  HistogramSeries,
  LineSeries,
  LineStyle,
  createChart,
  type IChartApi,
  type IPriceLine,
  type ISeriesApi,
  type UTCTimestamp,
} from "lightweight-charts";
import { ShieldAlert } from "lucide-react";
import type { MarketPair, OpenOrder } from "@/lib/api";
import { formatPrice } from "@/lib/format";
import { playClickSfx } from "@/lib/sound";
import type { MarketSnapshot, Timeframe } from "@/hooks/useMarketStream";
import { DepthChart } from "@/components/DepthChart";

interface CandlestickChartProps {
  pair: MarketPair;
  timeframe: Timeframe;
  onTimeframeChange: (tf: Timeframe) => void;
  market: MarketSnapshot;
  openOrders?: OpenOrder[];
}

type ChartView = "price" | "depth";

const TIMEFRAMES: { id: Timeframe; label: string }[] = [
  { id: "1m", label: "1m" },
  { id: "5m", label: "5m" },
  { id: "15m", label: "15m" },
  { id: "1h", label: "1H" },
  { id: "4h", label: "4H" },
  { id: "1d", label: "1D" },
];

interface CrosshairOhlcv {
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
  ema20: number;
  ema50: number;
}

export function CandlestickChart({
  pair,
  timeframe,
  onTimeframeChange,
  market,
  openOrders = [],
}: CandlestickChartProps) {
  const [view, setView] = useState<ChartView>("price");
  const [showEma20, setShowEma20] = useState<boolean>(true);
  const [showEma50, setShowEma50] = useState<boolean>(true);
  const [showVol, setShowVol] = useState<boolean>(true);
  const [showCollar, setShowCollar] = useState<boolean>(true);
  const [hoveredBar, setHoveredBar] = useState<CrosshairOhlcv | null>(null);

  const containerRef = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const candleSeriesRef = useRef<ISeriesApi<"Candlestick"> | null>(null);
  const volumeSeriesRef = useRef<ISeriesApi<"Histogram"> | null>(null);
  const ema20SeriesRef = useRef<ISeriesApi<"Line"> | null>(null);
  const ema50SeriesRef = useRef<ISeriesApi<"Line"> | null>(null);
  const upperBandRef = useRef<IPriceLine | null>(null);
  const lowerBandRef = useRef<IPriceLine | null>(null);
  const orderLinesRef = useRef<IPriceLine[]>([]);
  const barLookupRef = useRef<Map<number, CrosshairOhlcv>>(new Map());
  const lastViewKeyRef = useRef<string>("");

  const collarBounds = useMemo(() => {
    const mid = Number(market.markPrice);
    if (!Number.isFinite(mid) || mid <= 0) return { upper: 0, lower: 0 };
    return {
      upper: Number((mid * 1.1).toFixed(2)),
      lower: Number((mid * 0.9).toFixed(2)),
    };
  }, [market.markPrice]);

  const computedSeries = useMemo(() => {
    const k20 = 2 / (20 + 1);
    const k50 = 2 / (50 + 1);
    let ema20Prev = market.candles[0]?.close ?? 0;
    let ema50Prev = market.candles[0]?.close ?? 0;

    const lookup = new Map<number, CrosshairOhlcv>();
    const candleData: {
      time: UTCTimestamp;
      open: number;
      high: number;
      low: number;
      close: number;
    }[] = [];
    const volumeData: { time: UTCTimestamp; value: number; color: string }[] = [];
    const ema20Data: { time: UTCTimestamp; value: number }[] = [];
    const ema50Data: { time: UTCTimestamp; value: number }[] = [];

    market.candles.forEach((c, idx) => {
      ema20Prev = idx === 0 ? c.close : c.close * k20 + ema20Prev * (1 - k20);
      ema50Prev = idx === 0 ? c.close : c.close * k50 + ema50Prev * (1 - k50);
      const ema20Val = Number(ema20Prev.toFixed(2));
      const ema50Val = Number(ema50Prev.toFixed(2));
      const t = c.time as UTCTimestamp;

      candleData.push({
        time: t,
        open: c.open,
        high: c.high,
        low: c.low,
        close: c.close,
      });
      volumeData.push({
        time: t,
        value: c.volume,
        color: c.close >= c.open ? "rgba(14, 203, 129, 0.28)" : "rgba(246, 70, 93, 0.28)",
      });
      ema20Data.push({ time: t, value: ema20Val });
      ema50Data.push({ time: t, value: ema50Val });
      lookup.set(c.time, {
        open: c.open,
        high: c.high,
        low: c.low,
        close: c.close,
        volume: c.volume,
        ema20: ema20Val,
        ema50: ema50Val,
      });
    });

    const lastBar =
      market.candles.length > 0
        ? lookup.get(market.candles[market.candles.length - 1]!.time) ?? null
        : null;

    return { candleData, volumeData, ema20Data, ema50Data, lookup, lastBar };
  }, [market.candles]);

  useEffect(() => {
    barLookupRef.current = computedSeries.lookup;
  }, [computedSeries.lookup]);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;

    const chart = createChart(el, {
      layout: {
        background: { type: ColorType.Solid, color: "#0B0E11" },
        textColor: "#848E9C",
        fontFamily: 'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
        fontSize: 11,
      },
      grid: {
        vertLines: { color: "#151921" },
        horzLines: { color: "#151921" },
      },
      crosshair: {
        mode: CrosshairMode.Normal,
        vertLine: {
          color: "rgba(56, 189, 248, 0.45)",
          width: 1,
          style: LineStyle.Dashed,
          labelBackgroundColor: "#1A1F27",
        },
        horzLine: {
          color: "rgba(56, 189, 248, 0.45)",
          width: 1,
          style: LineStyle.Dashed,
          labelBackgroundColor: "#1A1F27",
        },
      },
      rightPriceScale: {
        borderColor: "#222831",
        scaleMargins: { top: 0.12, bottom: 0.2 },
      },
      timeScale: {
        borderColor: "#222831",
        timeVisible: true,
        secondsVisible: false,
        rightOffset: 10,
        barSpacing: 9,
        minBarSpacing: 3,
        fixLeftEdge: true,
      },
      width: el.clientWidth,
      height: el.clientHeight || 400,
    });

    const candleSeries = chart.addSeries(CandlestickSeries, {
      upColor: "#0ECB81",
      downColor: "#F6465D",
      borderUpColor: "#0ECB81",
      borderDownColor: "#F6465D",
      wickUpColor: "#0ECB81",
      wickDownColor: "#F6465D",
    });

    const ema20Series = chart.addSeries(LineSeries, {
      color: "#38BDF8",
      lineWidth: 1,
      priceLineVisible: false,
      lastValueVisible: false,
      crosshairMarkerVisible: false,
    });

    const ema50Series = chart.addSeries(LineSeries, {
      color: "#F59E0B",
      lineWidth: 1,
      priceLineVisible: false,
      lastValueVisible: false,
      crosshairMarkerVisible: false,
    });

    const volumeSeries = chart.addSeries(HistogramSeries, {
      priceFormat: { type: "volume" },
      priceScaleId: "",
    });
    volumeSeries.priceScale().applyOptions({
      scaleMargins: { top: 0.84, bottom: 0 },
    });

    chart.subscribeCrosshairMove((param) => {
      if (!param.time || typeof param.time !== "number") {
        setHoveredBar(null);
        return;
      }
      const bar = barLookupRef.current.get(param.time) ?? null;
      setHoveredBar(bar);
    });

    chartRef.current = chart;
    candleSeriesRef.current = candleSeries;
    ema20SeriesRef.current = ema20Series;
    ema50SeriesRef.current = ema50Series;
    volumeSeriesRef.current = volumeSeries;

    const observer = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (!entry || !chartRef.current) return;
      const { width, height } = entry.contentRect;
      if (width > 0 && height > 0) {
        chartRef.current.applyOptions({ width, height });
        chartRef.current.timeScale().fitContent();
      }
    });
    observer.observe(el);

    return () => {
      observer.disconnect();
      upperBandRef.current = null;
      lowerBandRef.current = null;
      orderLinesRef.current = [];
      chart.remove();
      chartRef.current = null;
      candleSeriesRef.current = null;
      ema20SeriesRef.current = null;
      ema50SeriesRef.current = null;
      volumeSeriesRef.current = null;
    };
  }, []);

  useEffect(() => {
    const candleSeries = candleSeriesRef.current;
    const volumeSeries = volumeSeriesRef.current;
    const ema20Series = ema20SeriesRef.current;
    const ema50Series = ema50SeriesRef.current;
    if (!candleSeries || !volumeSeries || !ema20Series || !ema50Series) return;

    candleSeries.setData(computedSeries.candleData);
    volumeSeries.setData(computedSeries.volumeData);
    ema20Series.setData(computedSeries.ema20Data);
    ema50Series.setData(computedSeries.ema50Data);

    const viewKey = `${pair}:${timeframe}`;
    if (lastViewKeyRef.current !== viewKey) {
      lastViewKeyRef.current = viewKey;
      chartRef.current?.timeScale().fitContent();
    }
  }, [computedSeries, pair, timeframe]);

  useEffect(() => {
    ema20SeriesRef.current?.applyOptions({ visible: showEma20 });
    ema50SeriesRef.current?.applyOptions({ visible: showEma50 });
    volumeSeriesRef.current?.applyOptions({ visible: showVol });
  }, [showEma20, showEma50, showVol]);

  useEffect(() => {
    if (view === "price" && chartRef.current) {
      chartRef.current.timeScale().fitContent();
    }
  }, [view]);

  // Circuit breaker ±10% collar price lines
  useEffect(() => {
    const candleSeries = candleSeriesRef.current;
    if (!candleSeries) return;

    if (upperBandRef.current) {
      candleSeries.removePriceLine(upperBandRef.current);
      upperBandRef.current = null;
    }
    if (lowerBandRef.current) {
      candleSeries.removePriceLine(lowerBandRef.current);
      lowerBandRef.current = null;
    }

    if (!showCollar || collarBounds.upper <= 0) return;

    upperBandRef.current = candleSeries.createPriceLine({
      price: collarBounds.upper,
      color: "rgba(246, 70, 93, 0.45)",
      lineWidth: 1,
      lineStyle: LineStyle.Dotted,
      axisLabelVisible: true,
      title: "+10% Collar",
    });

    lowerBandRef.current = candleSeries.createPriceLine({
      price: collarBounds.lower,
      color: "rgba(246, 70, 93, 0.45)",
      lineWidth: 1,
      lineStyle: LineStyle.Dotted,
      axisLabelVisible: true,
      title: "-10% Collar",
    });
  }, [collarBounds.upper, collarBounds.lower, showCollar]);

  // Resting open limit orders drawn directly on the candlestick chart
  useEffect(() => {
    const candleSeries = candleSeriesRef.current;
    if (!candleSeries) return;

    for (const line of orderLinesRef.current) {
      candleSeries.removePriceLine(line);
    }
    orderLinesRef.current = [];

    const pairOrders = openOrders.filter((o) => o.market_id === pair);

    for (const ord of pairOrders) {
      const px = Number(ord.price);
      if (!Number.isFinite(px) || px <= 0) continue;
      const isBuy = ord.side === "BUY";
      const line = candleSeries.createPriceLine({
        price: px,
        color: isBuy ? "rgba(14, 203, 129, 0.78)" : "rgba(246, 70, 93, 0.78)",
        lineWidth: 1,
        lineStyle: LineStyle.Dashed,
        axisLabelVisible: true,
        title: "",
      });
      orderLinesRef.current.push(line);
    }
  }, [openOrders, pair]);

  const activeBar = hoveredBar ?? computedSeries.lastBar;
  const activeTfLabel = TIMEFRAMES.find((t) => t.id === timeframe)?.label ?? timeframe;
  const isUpBar = activeBar ? activeBar.close >= activeBar.open : true;

  return (
    <section className="flex h-full min-h-0 flex-col overflow-hidden bg-[#0B0E11]">
      {/* TradingView Sub-Toolbar */}
      <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-[#222831] bg-[#13171D] px-3 py-1">
        <div className="flex items-center gap-2.5">
          {/* Timeframe Pills */}
          <div className="flex items-center gap-0.5 rounded bg-[#0B0E11] p-0.5">
            {TIMEFRAMES.map((tf) => (
              <button
                key={tf.id}
                type="button"
                onClick={() => {
                  playClickSfx();
                  onTimeframeChange(tf.id);
                }}
                className={`btn-tactile cursor-pointer rounded px-2 py-0.5 text-[11px] font-medium tabular-nums ${
                  timeframe === tf.id
                    ? "bg-[#1A1F27] text-[#EAECEF]"
                    : "text-[#848E9C] hover:text-[#EAECEF]"
                }`}
              >
                {tf.label}
              </button>
            ))}
          </div>

          <div className="hidden h-3.5 w-px bg-[#222831] md:block" />

          {/* Interactive Indicator Toggles */}
          <div className="hidden items-center gap-1.5 text-[10px] tabular-nums md:flex">
            <span className="text-[#848E9C]">Indicators:</span>
            <button
              type="button"
              onClick={() => {
                playClickSfx();
                setShowEma20((v) => !v);
              }}
              className={`btn-tactile cursor-pointer rounded px-1.5 py-0.5 font-medium ${
                showEma20 ? "bg-sky-400/15 text-[#38BDF8]" : "text-[#848E9C] hover:text-[#EAECEF]"
              }`}
            >
              EMA 20
            </button>
            <button
              type="button"
              onClick={() => {
                playClickSfx();
                setShowEma50((v) => !v);
              }}
              className={`btn-tactile cursor-pointer rounded px-1.5 py-0.5 font-medium ${
                showEma50 ? "bg-amber-400/15 text-amber-400" : "text-[#848E9C] hover:text-[#EAECEF]"
              }`}
            >
              EMA 50
            </button>
            <button
              type="button"
              onClick={() => {
                playClickSfx();
                setShowVol((v) => !v);
              }}
              className={`btn-tactile cursor-pointer rounded px-1.5 py-0.5 font-medium ${
                showVol ? "bg-[#1A1F27] text-[#EAECEF]" : "text-[#848E9C] hover:text-[#EAECEF]"
              }`}
            >
              Vol
            </button>
            <button
              type="button"
              onClick={() => {
                playClickSfx();
                setShowCollar((v) => !v);
              }}
              className={`btn-tactile cursor-pointer rounded px-1.5 py-0.5 font-medium ${
                showCollar ? "bg-[#0ECB81]/15 text-[#0ECB81]" : "text-[#848E9C] hover:text-[#EAECEF]"
              }`}
            >
              ±10% Collar
            </button>
          </div>
        </div>

        <div className="flex items-center gap-2">
          {market.circuitBreakerTripped && (
            <span className="inline-flex items-center gap-1 rounded border border-[#F6465D]/40 bg-[#F6465D]/15 px-2 py-0.5 text-[10px] font-semibold tracking-tight text-[#F6465D]">
              <ShieldAlert className="h-3 w-3" />
              HALTED (±10% COLLAR)
            </span>
          )}

          {/* TradingView | Depth Switcher */}
          <div className="flex items-center rounded border border-[#222831] bg-[#0B0E11] p-0.5">
            <button
              type="button"
              onClick={() => {
                playClickSfx();
                setView("price");
              }}
              className={`btn-tactile cursor-pointer rounded px-2.5 py-0.5 text-[11px] font-medium tracking-tight ${
                view === "price"
                  ? "bg-[#1A1F27] text-[#EAECEF]"
                  : "text-[#848E9C] hover:text-[#EAECEF]"
              }`}
            >
              TradingView
            </button>
            <button
              type="button"
              onClick={() => {
                playClickSfx();
                setView("depth");
              }}
              className={`btn-tactile cursor-pointer rounded px-2.5 py-0.5 text-[11px] font-medium tracking-tight ${
                view === "depth"
                  ? "bg-[#1A1F27] text-[#EAECEF]"
                  : "text-[#848E9C] hover:text-[#EAECEF]"
              }`}
            >
              Depth
            </button>
          </div>
        </div>
      </div>

      {/* Chart Canvas with Floating Top-Left OHLCV + EMA Legend Overlay */}
      <div className={view === "price" ? "relative min-h-0 w-full flex-1 bg-[#0B0E11]" : "hidden"}>
        <div ref={containerRef} className="h-full w-full" />

        {activeBar && (
          <div className="pointer-events-none absolute left-3 top-2 z-10 flex flex-wrap items-center gap-x-3 gap-y-0.5 rounded bg-[#0B0E11]/80 px-2 py-1 text-[11px] tabular-nums backdrop-blur-[2px]">
            <span className="font-semibold text-[#EAECEF]">
              {pair} · {activeTfLabel} · SettleCore CLOB
            </span>
            <span className="text-[#848E9C]">
              O{" "}
              <strong className={`font-medium ${isUpBar ? "text-[#0ECB81]" : "text-[#F6465D]"}`}>
                {formatPrice(activeBar.open.toString(), 2)}
              </strong>
            </span>
            <span className="text-[#848E9C]">
              H{" "}
              <strong className={`font-medium ${isUpBar ? "text-[#0ECB81]" : "text-[#F6465D]"}`}>
                {formatPrice(activeBar.high.toString(), 2)}
              </strong>
            </span>
            <span className="text-[#848E9C]">
              L{" "}
              <strong className={`font-medium ${isUpBar ? "text-[#0ECB81]" : "text-[#F6465D]"}`}>
                {formatPrice(activeBar.low.toString(), 2)}
              </strong>
            </span>
            <span className="text-[#848E9C]">
              C{" "}
              <strong className={`font-medium ${isUpBar ? "text-[#0ECB81]" : "text-[#F6465D]"}`}>
                {formatPrice(activeBar.close.toString(), 2)}
              </strong>
            </span>
            <span className="text-[#848E9C]">
              Vol <strong className="font-medium text-[#EAECEF]">{activeBar.volume.toFixed(1)}</strong>
            </span>
            {showEma20 && (
              <span className="text-[#38BDF8]">
                EMA20 <strong>{formatPrice(activeBar.ema20.toString(), 2)}</strong>
              </span>
            )}
            {showEma50 && (
              <span className="text-amber-400">
                EMA50 <strong>{formatPrice(activeBar.ema50.toString(), 2)}</strong>
              </span>
            )}
          </div>
        )}
      </div>

      {view === "depth" && <DepthChart market={market} embedded />}
    </section>
  );
}
