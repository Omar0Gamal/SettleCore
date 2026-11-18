"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  DEFAULT_USER_ID,
  subscribeChaosEvents,
  subscribeOrderEvents,
  type MarketPair,
  type OrderSide,
} from "@/lib/api";
import {
  DEMO_INITIAL_EPOCH_MS,
  getDemoFrame,
  getDemoNowMs,
  isDemoMode,
  isFrameSteppedDemo,
  mulberry32,
  subscribeDemoSeek,
} from "@/lib/demo";
import { addDecimalStrings } from "@/lib/format";

export type Timeframe = "1m" | "5m" | "15m" | "1h" | "4h" | "1d";

export interface BookLevel {
  price: string;
  quantity: string;
  cumulative: string;
}

export interface TradeTick {
  trade_id: string;
  wal_seq: number;
  price: string;
  quantity: string;
  taker_side: OrderSide;
  latency_us: number;
  executed_at: string;
  isNew?: boolean;
}

export interface CandleBar {
  time: number;
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
}

export interface MarketSnapshot {
  pair: MarketPair;
  markPrice: string;
  indexPrice: string;
  high24h: string;
  low24h: string;
  change24hBps: number;
  volume24hUsd: string;
  bids: BookLevel[];
  asks: BookLevel[];
  spread: string;
  spreadBps: number;
  trades: TradeTick[];
  candles: CandleBar[];
  circuitBreakerTripped: boolean;
  wsConnected: boolean;
}

const WS_URL = process.env.NEXT_PUBLIC_WS_URL ?? "ws://localhost:8080/v1/ws";

const PAIR_CONFIG: Record<
  MarketPair,
  {
    baseMark: number;
    step: number;
    qtyBase: number;
    change24hBps: number;
    volUsd: string;
    high24h: string;
    low24h: string;
  }
> = {
  "ETH/USDC": {
    baseMark: 3125.0,
    step: 0.5,
    qtyBase: 2.4,
    change24hBps: 342,
    volUsd: "184,920,410",
    high24h: "3168.50000000",
    low24h: "3012.00000000",
  },
  "BTC/USDC": {
    baseMark: 92450.0,
    step: 5.0,
    qtyBase: 0.35,
    change24hBps: 218,
    volUsd: "412,680,900",
    high24h: "93420.00000000",
    low24h: "90180.00000000",
  },
};

function timeframeSeconds(tf: Timeframe): number {
  switch (tf) {
    case "1m":
      return 60;
    case "5m":
      return 300;
    case "15m":
      return 900;
    case "1h":
      return 3600;
    case "4h":
      return 14400;
    case "1d":
      return 86400;
  }
}

function generateInitialCandles(pair: MarketPair, tf: Timeframe, count = 180): CandleBar[] {
  const cfg = PAIR_CONFIG[pair];
  const interval = timeframeSeconds(tf);
  const nowSec = Math.floor(DEMO_INITIAL_EPOCH_MS / 1000 / interval) * interval;
  const candles: CandleBar[] = [];
  let price = cfg.baseMark * 0.978;

  for (let i = count - 1; i >= 0; i--) {
    const t = nowSec - i * interval;
    const progress = (count - 1 - i) / Math.max(1, count - 1);
    const macroWave =
      Math.sin(progress * Math.PI * 3.4) * cfg.step * 14.0 +
      Math.cos(progress * Math.PI * 7.2) * cfg.step * 6.5;
    const microWave =
      Math.sin(i * 0.43) * cfg.step * 3.4 +
      Math.cos(i * 0.21) * cfg.step * 2.2 +
      Math.sin(i * 1.13) * cfg.step * 1.1;
    const target = cfg.baseMark * (0.982 + 0.018 * progress) + macroWave;
    const meanPull = (target - price) * 0.16;

    const open = Number(price.toFixed(2));
    const close =
      i === 0
        ? cfg.baseMark
        : Number((open + meanPull + microWave).toFixed(2));

    const bodySize = Math.abs(close - open);
    const upperWick = cfg.step * (1.1 + ((i * 5) % 7) * 0.35) + bodySize * 0.22;
    const lowerWick = cfg.step * (1.0 + ((i * 3 + 2) % 7) * 0.34) + bodySize * 0.2;
    const high = Number((Math.max(open, close) + upperWick).toFixed(2));
    const low = Number((Math.min(open, close) - lowerWick).toFixed(2));

    const impulseFactor = 1 + Math.min(2.5, bodySize / (cfg.step * 4));
    const volume = Number(
      (cfg.qtyBase * (10 + ((i * 11) % 23)) * impulseFactor).toFixed(2)
    );

    candles.push({ time: t, open, high, low, close, volume });
    price = close;
  }

  return candles;
}

function buildBookLevels(
  mid: number,
  step: number,
  qtyBase: number,
  side: "BID" | "ASK",
  seed: number
): BookLevel[] {
  const levels: BookLevel[] = [];
  let cumulative = 0;

  for (let i = 1; i <= 16; i++) {
    const px = side === "ASK" ? mid + i * step : mid - i * step;
    const jitter = 0.65 + ((Math.abs(Math.sin(seed * 0.7 + i * 1.3)) * 1.8) % 2.4);
    const qty = Number((qtyBase * jitter * (1 + i * 0.12)).toFixed(4));
    cumulative += qty;
    levels.push({
      price: px.toFixed(8),
      quantity: qty.toFixed(8),
      cumulative: cumulative.toFixed(8),
    });
  }

  return side === "ASK" ? levels.reverse() : levels;
}

function generateInitialTrades(pair: MarketPair): TradeTick[] {
  const cfg = PAIR_CONFIG[pair];
  const now = DEMO_INITIAL_EPOCH_MS;
  const trades: TradeTick[] = [];
  for (let i = 0; i < 18; i++) {
    const side: OrderSide = i % 2 === 0 ? "BUY" : "SELL";
    const offset = ((i % 5) - 2) * cfg.step * 0.5;
    trades.push({
      trade_id: String(55019 - i),
      wal_seq: 148209 - i,
      price: (cfg.baseMark + offset).toFixed(8),
      quantity: (cfg.qtyBase * (0.3 + ((i * 3) % 7) * 0.2)).toFixed(8),
      taker_side: side,
      latency_us: Number((0.41 + ((i * 13) % 9) * 0.08).toFixed(2)),
      executed_at: new Date(now - i * 1400).toISOString(),
    });
  }
  return trades;
}

interface StreamBookState {
  key: string;
  markNum: number;
  tickSeed: number;
  candles: CandleBar[];
  trades: TradeTick[];
  liveBids: BookLevel[] | null;
  liveAsks: BookLevel[] | null;
}

function applyCandleAndTradeTick(
  base: StreamBookState,
  pair: MarketPair,
  timeframe: Timeframe,
  price: number,
  qty: number,
  side: OrderSide,
  latencyUs: number,
  walSeq: number,
  tradeId: string,
  nowMs: number
): StreamBookState {
  const interval = timeframeSeconds(timeframe);
  const bucket = Math.floor(nowMs / 1000 / interval) * interval;
  const nowIso = new Date(nowMs).toISOString();

  const nextTrades: TradeTick[] = [
    {
      trade_id: tradeId,
      wal_seq: walSeq,
      price: price.toFixed(8),
      quantity: qty.toFixed(8),
      taker_side: side,
      latency_us: latencyUs,
      executed_at: nowIso,
      isNew: true,
    },
    ...base.trades.slice(0, 29).map((t) => ({ ...t, isNew: false })),
  ];

  const last = base.candles[base.candles.length - 1];
  let nextCandles = base.candles;
  if (last) {
    if (last.time === bucket) {
      const updated: CandleBar = {
        ...last,
        high: Math.max(last.high, price),
        low: Math.min(last.low, price),
        close: price,
        volume: Number((last.volume + qty).toFixed(4)),
      };
      nextCandles = [...base.candles.slice(0, -1), updated];
    } else {
      nextCandles = [
        ...base.candles.slice(1),
        {
          time: bucket,
          open: last.close,
          high: Math.max(last.close, price),
          low: Math.min(last.close, price),
          close: price,
          volume: qty,
        },
      ];
    }
  }

  return {
    ...base,
    markNum: price,
    tickSeed: base.tickSeed + 1,
    trades: nextTrades,
    candles: nextCandles,
  };
}

function createInitialBookState(pair: MarketPair, timeframe: Timeframe): StreamBookState {
  return {
    key: `${pair}:${timeframe}`,
    markNum: PAIR_CONFIG[pair].baseMark,
    tickSeed: 1,
    candles: generateInitialCandles(pair, timeframe),
    trades: generateInitialTrades(pair),
    liveBids: null,
    liveAsks: null,
  };
}

function buildDeterministicFrameState(
  pair: MarketPair,
  timeframe: Timeframe,
  frame: number,
  userTrades: TradeTick[]
): StreamBookState {
  const cfg = PAIR_CONFIG[pair];
  let state = createInitialBookState(pair, timeframe);
  const steps = Math.max(0, Math.floor(frame / 15));
  let mark = cfg.baseMark;

  for (let step = 1; step <= steps; step++) {
    const rng = mulberry32(0x5E771EC0 + step * 7919 + (pair === "BTC/USDC" ? 131 : 17));
    const drift = (rng() - 0.46) * cfg.step * 2.2;
    const pullToBase = (cfg.baseMark - mark) * 0.05;
    mark = Number((mark + drift + pullToBase).toFixed(2));
    const side: OrderSide = rng() > 0.45 ? "BUY" : "SELL";
    const qty = Number((cfg.qtyBase * (0.35 + rng() * 1.45)).toFixed(4));
    const lat = Number((0.18 + rng() * 0.42).toFixed(2));
    const tickMs = DEMO_INITIAL_EPOCH_MS + step * 1500;
    state = applyCandleAndTradeTick(
      state,
      pair,
      timeframe,
      mark,
      qty,
      side,
      lat,
      148209 + step,
      String(55019 + step),
      tickMs
    );
  }

  for (let i = userTrades.length - 1; i >= 0; i--) {
    const ut = userTrades[i]!;
    state = applyCandleAndTradeTick(
      state,
      pair,
      timeframe,
      Number(ut.price),
      Number(ut.quantity),
      ut.taker_side,
      ut.latency_us,
      ut.wal_seq,
      ut.trade_id,
      Date.parse(ut.executed_at) || getDemoNowMs()
    );
  }

  return state;
}

export function useMarketStream(pair: MarketPair, timeframe: Timeframe): MarketSnapshot {
  const activeKey = `${pair}:${timeframe}`;
  const [bookState, setBookState] = useState<StreamBookState>(() => {
    const frame = getDemoFrame();
    if (isFrameSteppedDemo() && frame > 0) {
      return buildDeterministicFrameState(pair, timeframe, frame, []);
    }
    return createInitialBookState(pair, timeframe);
  });
  const [wsConnected, setWsConnected] = useState<boolean>(false);
  const [circuitBreakerTripped, setCircuitBreakerTripped] = useState<boolean>(false);

  const walRef = useRef<number>(148210);
  const tradeIdRef = useRef<number>(55020);
  const markRef = useRef<number>(PAIR_CONFIG[pair].baseMark);
  const userOrderTradesRef = useRef<TradeTick[]>([]);

  const current =
    bookState.key === activeKey ? bookState : createInitialBookState(pair, timeframe);

  const appendTradeAndCandle = useCallback(
    (price: number, qty: number, side: OrderSide, latencyUs: number, walSeq?: number) => {
      const seq = walSeq ?? ++walRef.current;
      const tid = String(++tradeIdRef.current);
      const nowMs = getDemoNowMs();

      setBookState((prev) => {
        const base =
          prev.key === activeKey ? prev : createInitialBookState(pair, timeframe);
        return applyCandleAndTradeTick(
          base,
          pair,
          timeframe,
          price,
          qty,
          side,
          latencyUs,
          seq,
          tid,
          nowMs
        );
      });
    },
    [activeKey, pair, timeframe]
  );

  // Listen to live Gateway WebSocket when available (skipped in deterministic demo mode)
  useEffect(() => {
    markRef.current = PAIR_CONFIG[pair].baseMark;
    userOrderTradesRef.current = [];
    if (isDemoMode()) {
      return;
    }

    let ws: WebSocket | null = null;
    let disposed = false;

    try {
      ws = new WebSocket(WS_URL);
      ws.onopen = () => {
        if (disposed || !ws) return;
        setWsConnected(true);
        ws.send(
          JSON.stringify({
            op: "subscribe",
            cancel_on_disconnect: true,
            channels: [`l2:${pair}`, `trades:${pair}`, `user:${DEFAULT_USER_ID}`],
          })
        );
      };
      ws.onclose = () => {
        if (!disposed) setWsConnected(false);
      };
      ws.onerror = () => {
        if (!disposed) setWsConnected(false);
      };
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(String(ev.data)) as {
            channel?: string;
            bids?: [string, string][];
            asks?: [string, string][];
            circuit_breaker_tripped?: boolean;
            wal_seq?: number;
            price?: string;
            quantity?: string;
            taker_side?: OrderSide;
          };
          if (msg.channel === `l2:${pair}`) {
            if (typeof msg.circuit_breaker_tripped === "boolean") {
              setCircuitBreakerTripped(msg.circuit_breaker_tripped);
            }
            if ((msg.bids && msg.bids.length > 0) || (msg.asks && msg.asks.length > 0)) {
              setBookState((prev) => {
                const base =
                  prev.key === activeKey ? prev : createInitialBookState(pair, timeframe);
                let nextBids = base.liveBids;
                let nextAsks = base.liveAsks;
                if (msg.bids && msg.bids.length > 0) {
                  let cum = "0.00000000";
                  nextBids = msg.bids.slice(0, 16).map(([p, q]) => {
                    cum = addDecimalStrings(cum, q, 8);
                    return { price: p, quantity: q, cumulative: cum };
                  });
                }
                if (msg.asks && msg.asks.length > 0) {
                  let cum = "0.00000000";
                  nextAsks = msg.asks
                    .slice(0, 16)
                    .map(([p, q]) => {
                      cum = addDecimalStrings(cum, q, 8);
                      return { price: p, quantity: q, cumulative: cum };
                    })
                    .reverse();
                }
                return { ...base, liveBids: nextBids, liveAsks: nextAsks };
              });
            }
          } else if (msg.channel === `trades:${pair}` && msg.price && msg.quantity) {
            const px = Number(msg.price);
            const qty = Number(msg.quantity);
            if (!Number.isNaN(px) && !Number.isNaN(qty)) {
              markRef.current = px;
              appendTradeAndCandle(px, qty, msg.taker_side ?? "BUY", 0.68, msg.wal_seq);
            }
          }
        } catch {
          // Ignore malformed frame
        }
      };
    } catch {
      // Ignore WebSocket constructor errors; fallback simulation handles offline state
    }

    return () => {
      disposed = true;
      ws?.close();
    };
  }, [activeKey, pair, timeframe, appendTradeAndCandle]);

  // Listen to user order submissions & chaos drills for instant UI feedback
  useEffect(() => {
    const unsubOrders = subscribeOrderEvents((res, req) => {
      if (req.market_id !== pair) return;
      for (const fill of res.fills) {
        const px = Number(fill.price);
        const qty = Number(fill.quantity);
        if (!Number.isNaN(px) && !Number.isNaN(qty)) {
          markRef.current = px;
          const latUs = Number((res.match_latency_ns / 1000).toFixed(2));
          userOrderTradesRef.current.unshift({
            trade_id: fill.trade_id,
            wal_seq: res.wal_seq,
            price: px.toFixed(8),
            quantity: qty.toFixed(8),
            taker_side: req.side,
            latency_us: latUs,
            executed_at: new Date(getDemoNowMs()).toISOString(),
            isNew: true,
          });
          appendTradeAndCandle(px, qty, req.side, latUs, res.wal_seq);
        }
      }
      setBookState((prev) => {
        const base =
          prev.key === activeKey ? prev : createInitialBookState(pair, timeframe);
        return { ...base, tickSeed: base.tickSeed + 1 };
      });
    });

    const unsubChaos = subscribeChaosEvents((ev) => {
      if (ev.drill === "flash-crash") {
        setCircuitBreakerTripped(true);
        const crashedPx = Number((markRef.current * 0.901).toFixed(2));
        markRef.current = crashedPx;
        appendTradeAndCandle(crashedPx, 18.5, "SELL", 0.94);
      } else if (ev.drill === "reset") {
        setCircuitBreakerTripped(false);
        const base = PAIR_CONFIG[pair].baseMark;
        markRef.current = base;
        userOrderTradesRef.current = [];
        setBookState(createInitialBookState(pair, timeframe));
      }
    });

    return () => {
      unsubOrders();
      unsubChaos();
    };
  }, [activeKey, pair, timeframe, appendTradeAndCandle]);

  // Deterministic frame stepping when ?demo=1 is active
  useEffect(() => {
    const unsubSeek = subscribeDemoSeek((frame) => {
      if (circuitBreakerTripped) return;
      const nextState = buildDeterministicFrameState(
        pair,
        timeframe,
        frame,
        userOrderTradesRef.current
      );
      markRef.current = nextState.markNum;
      setBookState(nextState);
    });
    return () => {
      unsubSeek();
    };
  }, [pair, timeframe, circuitBreakerTripped]);

  // Smooth simulation ticker when WebSocket is offline and NOT frame-stepped (?demo=1)
  useEffect(() => {
    if (wsConnected || isFrameSteppedDemo()) return;
    const cfg = PAIR_CONFIG[pair];
    let step = 1;
    const timer = setInterval(() => {
      if (circuitBreakerTripped) return;
      const rng = mulberry32(0x1337BEEF + step++);
      const drift = (rng() - 0.495) * cfg.step * 1.4;
      const pullToBase = (cfg.baseMark - markRef.current) * 0.04;
      const next = Number((markRef.current + drift + pullToBase).toFixed(2));
      markRef.current = next;

      if (rng() > 0.35) {
        const side: OrderSide = rng() > 0.48 ? "BUY" : "SELL";
        const qty = Number((cfg.qtyBase * (0.2 + rng() * 1.4)).toFixed(4));
        const lat = Number((0.18 + rng() * 0.64).toFixed(2));
        appendTradeAndCandle(next, qty, side, lat);
      } else {
        setBookState((prev) => {
          const base =
            prev.key === activeKey ? prev : createInitialBookState(pair, timeframe);
          return { ...base, markNum: next, tickSeed: base.tickSeed + 1 };
        });
      }
    }, 650);

    return () => clearInterval(timer);
  }, [activeKey, pair, timeframe, wsConnected, circuitBreakerTripped, appendTradeAndCandle]);

  return useMemo(() => {
    const cfg = PAIR_CONFIG[pair];
    const bids =
      current.liveBids ??
      buildBookLevels(current.markNum, cfg.step, cfg.qtyBase, "BID", current.tickSeed);
    const asks =
      current.liveAsks ??
      buildBookLevels(current.markNum, cfg.step, cfg.qtyBase, "ASK", current.tickSeed + 3);
    const bestAsk = asks[asks.length - 1];
    const bestBid = bids[0];
    const spreadNum =
      bestAsk && bestBid ? Math.max(0, Number(bestAsk.price) - Number(bestBid.price)) : cfg.step;
    const spreadBps = current.markNum > 0 ? Math.round((spreadNum / current.markNum) * 10000) : 1;

    const indexOffset = pair === "ETH/USDC" ? -0.18 : -4.25;
    const indexNum = Math.max(0, current.markNum + indexOffset);

    return {
      pair,
      markPrice: current.markNum.toFixed(8),
      indexPrice: indexNum.toFixed(8),
      high24h: cfg.high24h,
      low24h: cfg.low24h,
      change24hBps: circuitBreakerTripped ? -1000 : cfg.change24hBps,
      volume24hUsd: cfg.volUsd,
      bids,
      asks,
      spread: spreadNum.toFixed(2),
      spreadBps,
      trades: current.trades,
      candles: current.candles,
      circuitBreakerTripped,
      wsConnected,
    };
  }, [pair, current, circuitBreakerTripped, wsConnected]);
}
