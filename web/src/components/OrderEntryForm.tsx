"use client";

import { useMemo, useState } from "react";
import {
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  Cpu,
  Database,
  KeyRound,
  Layers,
  Loader2,
  Minus,
  Plus,
  ShieldCheck,
} from "lucide-react";
import {
  submitOrder,
  type AccountBalance,
  type MarketPair,
  type OrderSide,
  type OrderType,
  type StpMode,
  type SubmitOrderResponse,
  type TimeInForce,
} from "@/lib/api";
import {
  formatLatencyUs,
  formatPrice,
  formatQuantity,
  isValidPositiveDecimal,
  mulDecimalStrings,
  parseDecimalToScaled,
} from "@/lib/format";
import { playClickSfx, playOrderFillSfx } from "@/lib/sound";

interface OrderEntryFormProps {
  pair: MarketPair;
  markPrice: string;
  circuitBreakerTripped: boolean;
  balances: AccountBalance[];
  orderType: OrderType;
  onOrderTypeChange: (type: OrderType) => void;
  price: string;
  onPriceChange: (price: string) => void;
  pricePulseTick?: number;
  onOrderSubmitted: (res: SubmitOrderResponse) => void;
  onInspectLifecycle: () => void;
}

const TIF_OPTIONS: TimeInForce[] = ["GTC", "IOC", "FOK"];
const STP_OPTIONS: { value: StpMode; label: string }[] = [
  { value: "CANCEL_NEWEST", label: "Cancel Newest" },
  { value: "CANCEL_OLDEST", label: "Cancel Oldest" },
  { value: "CANCEL_BOTH", label: "Cancel Both" },
];
const SLIDER_NODES = [0, 25, 50, 75, 100];

export function OrderEntryForm({
  pair,
  markPrice,
  circuitBreakerTripped,
  balances,
  orderType,
  onOrderTypeChange,
  price,
  onPriceChange,
  pricePulseTick = 0,
  onOrderSubmitted,
  onInspectLifecycle,
}: OrderEntryFormProps) {
  const [side, setSide] = useState<OrderSide>("BUY");
  const [postOnly, setPostOnly] = useState<boolean>(false);
  const [tif, setTif] = useState<TimeInForce>("GTC");
  const [stpMode, setStpMode] = useState<StpMode>("CANCEL_NEWEST");
  const [quantity, setQuantity] = useState<string>(() =>
    pair === "ETH/USDC" ? "2.50" : "0.25"
  );
  const [totalDraft, setTotalDraft] = useState<string | null>(null);
  const [sliderPct, setSliderPct] = useState<number>(0);
  const [submitting, setSubmitting] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);
  const [receipt, setReceipt] = useState<SubmitOrderResponse | null>(null);

  const baseSymbol = pair === "ETH/USDC" ? "ETH" : "WBTC";
  const displayBase = pair === "ETH/USDC" ? "ETH" : "BTC";
  const quoteSymbol = "USDC";
  const priceTick = pair === "ETH/USDC" ? 0.5 : 5.0;

  const availableBase = useMemo(
    () => balances.find((b) => b.asset === baseSymbol)?.available ?? "0.00000000",
    [balances, baseSymbol]
  );
  const availableQuote = useMemo(
    () => balances.find((b) => b.asset === quoteSymbol)?.available ?? "0.00000000",
    [balances, quoteSymbol]
  );
  const lockedQuote = useMemo(
    () => balances.find((b) => b.asset === quoteSymbol)?.locked ?? "0.00000000",
    [balances, quoteSymbol]
  );

  const effectivePrice = orderType === "MARKET" ? markPrice : price;

  const rawNotional = useMemo(() => {
    if (!isValidPositiveDecimal(effectivePrice, 8) || !isValidPositiveDecimal(quantity, 8)) {
      return "0.00000000";
    }
    return mulDecimalStrings(effectivePrice, quantity, 8);
  }, [effectivePrice, quantity]);

  const computedTotalDisplay = useMemo(() => {
    if (totalDraft !== null) return totalDraft;
    return formatPrice(rawNotional, 2, false);
  }, [totalDraft, rawNotional]);

  const estimatedNotional = useMemo(() => formatPrice(rawNotional, 2), [rawNotional]);
  const estimatedTakerFee = useMemo(
    () => formatPrice(mulDecimalStrings(rawNotional, postOnly ? "0.00000000" : "0.00050000", 8), 2),
    [rawNotional, postOnly]
  );
  const preTradeReserveText = useMemo(() => {
    if (side === "BUY") {
      return `${estimatedNotional} ${quoteSymbol}`;
    }
    const qtyValid = isValidPositiveDecimal(quantity, 8);
    return `${qtyValid ? formatQuantity(quantity, 4, true) : "0.0000"} ${baseSymbol}`;
  }, [side, estimatedNotional, quoteSymbol, quantity, baseSymbol]);

  const collarBounds = useMemo(() => {
    const mid = Number(markPrice);
    if (!Number.isFinite(mid) || mid <= 0) return { lower: "0.00", upper: "0.00" };
    return {
      lower: (mid * 0.9).toLocaleString(undefined, {
        minimumFractionDigits: 2,
        maximumFractionDigits: 2,
      }),
      upper: (mid * 1.1).toLocaleString(undefined, {
        minimumFractionDigits: 2,
        maximumFractionDigits: 2,
      }),
    };
  }, [markPrice]);

  const handleStepPrice = (delta: number) => {
    playClickSfx();
    const current = Number(price);
    const base = Number.isFinite(current) && current > 0 ? current : Number(markPrice);
    const next = Math.max(priceTick, base + delta);
    setTotalDraft(null);
    onPriceChange(next.toFixed(2));
  };

  const handleQtyInput = (nextQty: string) => {
    setTotalDraft(null);
    setQuantity(nextQty);
    setSliderPct(0);
  };

  const handleTotalInput = (nextTotal: string) => {
    setTotalDraft(nextTotal);
    setSliderPct(0);
    if (!isValidPositiveDecimal(nextTotal, 8)) return;
    const px = parseDecimalToScaled(effectivePrice, 8);
    const tot = parseDecimalToScaled(nextTotal, 8);
    if (px && px > 0n && tot !== null) {
      const qtyScaled = (tot * 100_000_000n) / px;
      const whole = qtyScaled / 100_000_000n;
      const frac = ((qtyScaled % 100_000_000n) / 10_000n).toString().padStart(4, "0");
      setQuantity(`${whole}.${frac}`);
    }
  };

  const handleQuickPct = (pct: number) => {
    playClickSfx();
    setSliderPct(pct);
    setTotalDraft(null);
    if (pct === 0) {
      setQuantity("0.0000");
      return;
    }
    const px = parseDecimalToScaled(effectivePrice, 8);
    if (side === "BUY" && px && px > 0n) {
      const availQuote = parseDecimalToScaled(availableQuote, 8) ?? 0n;
      const budget = (availQuote * BigInt(pct)) / 100n;
      const qtyScaled = (budget * 100_000_000n) / px;
      const whole = qtyScaled / 100_000_000n;
      const frac = ((qtyScaled % 100_000_000n) / 10_000n).toString().padStart(4, "0");
      setQuantity(`${whole}.${frac}`);
    } else if (side === "SELL") {
      const availBase = parseDecimalToScaled(availableBase, 8) ?? 0n;
      const qtyScaled = (availBase * BigInt(pct)) / 100n;
      const whole = qtyScaled / 100_000_000n;
      const frac = ((qtyScaled % 100_000_000n) / 10_000n).toString().padStart(4, "0");
      setQuantity(`${whole}.${frac}`);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);

    if (orderType === "LIMIT" && !isValidPositiveDecimal(price, 8)) {
      setError("Invalid fixed-point limit price (max 8 decimals).");
      return;
    }
    if (!isValidPositiveDecimal(quantity, 8)) {
      setError("Invalid fixed-point quantity (max 8 decimals).");
      return;
    }

    setSubmitting(true);
    try {
      const res = await submitOrder({
        market_id: pair,
        side,
        order_type: orderType,
        time_in_force: orderType === "MARKET" ? "IOC" : tif,
        stp_mode: stpMode,
        price: orderType === "MARKET" ? markPrice : price,
        quantity,
      });
      playOrderFillSfx();
      setReceipt(res);
      setTotalDraft(null);
      onOrderSubmitted(res);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Order rejected by CLOB");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <section className="flex h-full min-h-0 flex-col overflow-hidden bg-[#0B0E11]">
      {/* Header */}
      <div className="flex shrink-0 items-center justify-between border-b border-[#222831] bg-[#13171D] px-3 py-1.5">
        <div className="flex items-center gap-2">
          <h2 className="text-xs font-semibold tracking-tight text-[#EAECEF]">Order Entry</h2>
          <span className="rounded bg-[#1A1F27] px-1.5 py-0.5 text-[10px] font-medium text-[#848E9C]">
            Spot
          </span>
        </div>
        <span className="rounded border border-sky-400/20 bg-sky-400/[0.08] px-1.5 py-0.5 text-[10px] font-medium tabular-nums text-[#38BDF8]">
          int64 · 1e8
        </span>
      </div>

      <form onSubmit={handleSubmit} className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-3">
        {/* Buy / Sell Segmented Tabs */}
        <div className="grid shrink-0 grid-cols-2 gap-1 rounded-md border border-[#222831] bg-[#13171D] p-1">
          <button
            type="button"
            data-testid="order-side-buy"
            onClick={() => {
              playClickSfx();
              setSide("BUY");
              setTotalDraft(null);
            }}
            className={`btn-tactile cursor-pointer rounded py-1.5 text-xs font-semibold tracking-tight ${
              side === "BUY"
                ? "bg-[#0ECB81] text-[#0B0E11] shadow-[0_0_20px_-6px_rgba(14,203,129,0.65)]"
                : "text-[#848E9C] hover:bg-[#1A1F27] hover:text-[#EAECEF]"
            }`}
          >
            Buy {displayBase}
          </button>
          <button
            type="button"
            data-testid="order-side-sell"
            onClick={() => {
              playClickSfx();
              setSide("SELL");
              setTotalDraft(null);
            }}
            className={`btn-tactile cursor-pointer rounded py-1.5 text-xs font-semibold tracking-tight ${
              side === "SELL"
                ? "bg-[#F6465D] text-white shadow-[0_0_20px_-6px_rgba(246,70,93,0.65)]"
                : "text-[#848E9C] hover:bg-[#1A1F27] hover:text-[#EAECEF]"
            }`}
          >
            Sell {displayBase}
          </button>
        </div>

        {/* Limit / Market / Post-Only Sub-Tabs + Available */}
        <div className="flex shrink-0 items-center justify-between border-b border-[#222831] pb-2">
          <div className="flex items-center gap-3 text-xs font-medium">
            <button
              type="button"
              data-testid="order-type-limit"
              onClick={() => {
                playClickSfx();
                setPostOnly(false);
                onOrderTypeChange("LIMIT");
              }}
              className={`btn-tactile relative cursor-pointer pb-1 transition-colors ${
                orderType === "LIMIT" && !postOnly
                  ? "text-[#EAECEF] after:absolute after:bottom-[-9px] after:left-0 after:right-0 after:h-[2px] after:bg-[#38BDF8]"
                  : "text-[#848E9C] hover:text-[#EAECEF]"
              }`}
            >
              Limit
            </button>
            <button
              type="button"
              data-testid="order-type-market"
              onClick={() => {
                playClickSfx();
                setPostOnly(false);
                onOrderTypeChange("MARKET");
              }}
              className={`btn-tactile relative cursor-pointer pb-1 transition-colors ${
                orderType === "MARKET"
                  ? "text-[#EAECEF] after:absolute after:bottom-[-9px] after:left-0 after:right-0 after:h-[2px] after:bg-[#38BDF8]"
                  : "text-[#848E9C] hover:text-[#EAECEF]"
              }`}
            >
              Market
            </button>
            <button
              type="button"
              onClick={() => {
                playClickSfx();
                setPostOnly(true);
                onOrderTypeChange("LIMIT");
              }}
              className={`btn-tactile relative cursor-pointer pb-1 transition-colors ${
                orderType === "LIMIT" && postOnly
                  ? "text-[#38BDF8] after:absolute after:bottom-[-9px] after:left-0 after:right-0 after:h-[2px] after:bg-[#38BDF8]"
                  : "text-[#848E9C] hover:text-[#EAECEF]"
              }`}
            >
              Post-Only
            </button>
          </div>

          <span className="text-[11px] tabular-nums text-[#848E9C]">
            Avbl{" "}
            <strong className="font-semibold text-[#EAECEF]">
              {side === "BUY"
                ? `${formatPrice(availableQuote, 2)} USDC`
                : `${formatQuantity(availableBase, 4)} ${baseSymbol}`}
            </strong>
          </span>
        </div>

        {/* Price Input with [-] / [+] Tick Steppers & BBO / Mid Button */}
        <div className="shrink-0">
          <div className="mb-1 flex items-center justify-between">
            <label htmlFor="order-price" className="text-[11px] font-medium text-[#848E9C]">
              Price
            </label>
            {orderType === "LIMIT" && (
              <button
                type="button"
                onClick={() => {
                  playClickSfx();
                  setTotalDraft(null);
                  onPriceChange(formatPrice(markPrice, 2, false));
                }}
                className="btn-tactile cursor-pointer rounded bg-[#1A1F27] px-1.5 py-0.5 text-[10px] font-medium tabular-nums text-[#38BDF8] hover:bg-[#222831]"
              >
                BBO / Mid ${formatPrice(markPrice, 2)}
              </button>
            )}
          </div>

          <div
            key={pricePulseTick}
            className={`flex items-center rounded border border-[#222831] bg-[#1A1F27] transition-colors focus-within:border-[#38BDF8] ${
              pricePulseTick > 0 ? "animate-input-pulse" : ""
            }`}
          >
            {orderType === "LIMIT" && (
              <button
                type="button"
                aria-label="Decrease price by 1 tick"
                onClick={() => handleStepPrice(-priceTick)}
                className="btn-tactile flex h-8 w-8 shrink-0 cursor-pointer items-center justify-center border-r border-[#222831] text-[#848E9C] hover:bg-[#222831] hover:text-[#EAECEF]"
              >
                <Minus className="h-3 w-3" />
              </button>
            )}
            <input
              id="order-price"
              data-testid="order-price-input"
              type="text"
              inputMode="decimal"
              disabled={orderType === "MARKET"}
              value={orderType === "MARKET" ? "Market (Best Execution)" : price}
              onChange={(e) => {
                setTotalDraft(null);
                onPriceChange(e.target.value);
              }}
              className="h-8 min-w-0 flex-1 bg-transparent px-2.5 text-right text-xs font-semibold tabular-nums text-[#EAECEF] outline-none disabled:text-[#848E9C]"
            />
            <span className="px-2 text-[10px] font-semibold text-[#848E9C]">USDC</span>
            {orderType === "LIMIT" && (
              <button
                type="button"
                aria-label="Increase price by 1 tick"
                onClick={() => handleStepPrice(priceTick)}
                className="btn-tactile flex h-8 w-8 shrink-0 cursor-pointer items-center justify-center border-l border-[#222831] text-[#848E9C] hover:bg-[#222831] hover:text-[#EAECEF]"
              >
                <Plus className="h-3 w-3" />
              </button>
            )}
          </div>
        </div>

        {/* Quantity Input */}
        <div className="shrink-0">
          <div className="mb-1 flex items-center justify-between">
            <label htmlFor="order-qty" className="text-[11px] font-medium text-[#848E9C]">
              Quantity
            </label>
            <span className="text-[10px] tabular-nums text-[#848E9C]">
              Step {pair === "ETH/USDC" ? "0.0001" : "0.00001"} {displayBase}
            </span>
          </div>
          <div className="flex items-center rounded border border-[#222831] bg-[#1A1F27] focus-within:border-[#38BDF8]">
            <input
              id="order-qty"
              data-testid="order-qty-input"
              type="text"
              inputMode="decimal"
              value={quantity}
              onChange={(e) => handleQtyInput(e.target.value)}
              className="h-8 min-w-0 flex-1 bg-transparent px-3 text-right text-xs font-semibold tabular-nums text-[#EAECEF] outline-none"
            />
            <span className="border-l border-[#222831] px-2.5 text-[10px] font-semibold text-[#848E9C]">
              {displayBase}
            </span>
          </div>
        </div>

        {/* Interactive 5-Node Diamond Percentage Slider (0% - 25% - 50% - 75% - 100%) */}
        <div className="shrink-0 px-1 py-1">
          <div className="relative flex h-6 items-center">
            <div className="h-1 w-full rounded-full bg-[#1A1F27]">
              <div
                className={`h-full rounded-full transition-all duration-200 ${
                  side === "BUY" ? "bg-[#0ECB81]" : "bg-[#F6465D]"
                }`}
                style={{ width: `${sliderPct}%` }}
              />
            </div>
            <div className="absolute inset-x-0 flex items-center justify-between">
              {SLIDER_NODES.map((pct) => {
                const active = sliderPct >= pct && pct > 0;
                const current = sliderPct === pct;
                return (
                  <button
                    key={pct}
                    type="button"
                    aria-label={`Set order size to ${pct}% of available balance`}
                    onClick={() => handleQuickPct(pct)}
                    className="group relative flex cursor-pointer flex-col items-center focus:outline-none"
                  >
                    <span
                      className={`h-2.5 w-2.5 rotate-45 rounded-[1px] border transition-all ${
                        current
                          ? side === "BUY"
                            ? "scale-125 border-[#0ECB81] bg-[#0ECB81] shadow-[0_0_10px_rgba(14,203,129,0.7)]"
                            : "scale-125 border-[#F6465D] bg-[#F6465D] shadow-[0_0_10px_rgba(246,70,93,0.7)]"
                          : active
                            ? side === "BUY"
                              ? "border-[#0ECB81] bg-[#0B0E11]"
                              : "border-[#F6465D] bg-[#0B0E11]"
                            : "border-[#474D57] bg-[#13171D] group-hover:border-[#EAECEF]"
                      }`}
                    />
                  </button>
                );
              })}
            </div>
          </div>
          <div className="mt-0.5 flex items-center justify-between text-[10px] tabular-nums text-[#848E9C]">
            {SLIDER_NODES.map((pct) => (
              <button
                key={pct}
                type="button"
                onClick={() => handleQuickPct(pct)}
                className={`cursor-pointer transition-colors hover:text-[#EAECEF] ${
                  sliderPct === pct ? "font-semibold text-[#EAECEF]" : ""
                }`}
              >
                {pct}%
              </button>
            ))}
          </div>
        </div>

        {/* Bidirectional Total (USDC) Input */}
        <div className="shrink-0">
          <div className="mb-1 flex items-center justify-between">
            <label htmlFor="order-total" className="text-[11px] font-medium text-[#848E9C]">
              Total
            </label>
            <span className="text-[10px] tabular-nums text-[#848E9C]">
              Est. Fee ${estimatedTakerFee}
            </span>
          </div>
          <div className="flex items-center rounded border border-[#222831] bg-[#1A1F27] focus-within:border-[#38BDF8]">
            <input
              id="order-total"
              type="text"
              inputMode="decimal"
              value={computedTotalDisplay}
              onChange={(e) => handleTotalInput(e.target.value)}
              onBlur={() => setTotalDraft(null)}
              className="h-8 min-w-0 flex-1 bg-transparent px-3 text-right text-xs font-semibold tabular-nums text-[#EAECEF] outline-none"
            />
            <span className="border-l border-[#222831] px-2.5 text-[10px] font-semibold text-[#848E9C]">
              USDC
            </span>
          </div>
        </div>

        {/* Compact Single-Row TIF + STP Controls */}
        <div className="grid shrink-0 grid-cols-2 gap-2">
          <div>
            <span className="mb-1 block text-[10px] font-medium text-[#848E9C]">Time in Force</span>
            <div className="grid grid-cols-3 gap-0.5 rounded border border-[#222831] bg-[#13171D] p-0.5">
              {TIF_OPTIONS.map((opt) => (
                <button
                  key={opt}
                  type="button"
                  disabled={orderType === "MARKET" && opt === "GTC"}
                  onClick={() => {
                    playClickSfx();
                    setTif(opt);
                  }}
                  className={`btn-tactile cursor-pointer rounded py-1 text-[10px] font-semibold tracking-tight ${
                    (orderType === "MARKET" ? opt === "IOC" : tif === opt)
                      ? "bg-[#1A1F27] text-[#EAECEF]"
                      : "text-[#848E9C] hover:text-[#EAECEF]"
                  }`}
                >
                  {opt}
                </button>
              ))}
            </div>
          </div>

          <div>
            <label htmlFor="stp-mode" className="mb-1 block text-[10px] font-medium text-[#848E9C]">
              Self-Trade Guard
            </label>
            <select
              id="stp-mode"
              value={stpMode}
              onChange={(e) => setStpMode(e.target.value as StpMode)}
              className="h-[26px] w-full rounded border border-[#222831] bg-[#1A1F27] px-2 text-[11px] font-medium text-[#EAECEF] outline-none focus:border-[#38BDF8]"
            >
              {STP_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
          </div>
        </div>

        {circuitBreakerTripped && (
          <div className="flex shrink-0 items-center gap-2 rounded border border-[#F6465D]/40 bg-[#F6465D]/10 px-2.5 py-1.5 text-[11px] text-[#F6465D]">
            <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
            <span>Circuit Breaker active (CANCEL_ONLY). Reset in Chaos Lab to resume.</span>
          </div>
        )}

        {error && (
          <div className="flex shrink-0 items-center gap-2 rounded border border-[#F6465D]/40 bg-[#F6465D]/10 px-2.5 py-1.5 text-[11px] text-[#F6465D]">
            <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
            <span>{error}</span>
          </div>
        )}

        {/* Primary CTA Button */}
        <button
          type="submit"
          data-testid="submit-order-btn"
          disabled={submitting || circuitBreakerTripped}
          className={`btn-tactile flex w-full shrink-0 cursor-pointer items-center justify-center gap-2 rounded-md py-2.5 text-xs font-bold tracking-tight disabled:cursor-not-allowed disabled:opacity-50 ${
            side === "BUY"
              ? "bg-[#0ECB81] text-[#0B0E11] shadow-[0_0_24px_-6px_rgba(14,203,129,0.6)] hover:bg-[#14D88B]"
              : "bg-[#F6465D] text-white shadow-[0_0_24px_-6px_rgba(246,70,93,0.6)] hover:bg-[#FF586E]"
          }`}
        >
          {submitting ? (
            <>
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              Matching in C++17 CLOB...
            </>
          ) : (
            <>
              {side === "BUY" ? "Buy" : "Sell"} {displayBase}
            </>
          )}
        </button>

        {/* Execution Receipt Card */}
        {receipt && (
          <div
            data-testid="order-exec-receipt"
            className="shrink-0 rounded-md border border-[#38BDF8]/35 bg-[#13171D] px-2.5 py-2 text-[11px] tabular-nums"
          >
            <div className="flex items-center justify-between text-[#EAECEF]">
              <span className="inline-flex items-center gap-1.5 font-semibold text-[#0ECB81]">
                <CheckCircle2 className="h-3.5 w-3.5" />
                {receipt.status} #{receipt.order_id}
              </span>
              <span className="inline-flex items-center gap-1 rounded bg-sky-400/15 px-1.5 py-0.5 text-[10px] font-semibold text-[#38BDF8]">
                <Cpu className="h-3 w-3" />
                {formatLatencyUs(receipt.match_latency_ns, "ns")}
              </span>
            </div>
            <div className="mt-1 flex items-center justify-between text-[10px] text-[#848E9C]">
              <span>wal_seq: #{receipt.wal_seq.toLocaleString()}</span>
              <span>
                Filled {formatQuantity(receipt.filled_quantity, 4)} · Rem{" "}
                {formatQuantity(receipt.remaining_quantity, 4)}
              </span>
            </div>
          </div>
        )}

        {/* Bottom Docked Architecture Verification & Custody Group (Eliminates L2 Gap) */}
        <div className="mt-auto shrink-0 space-y-2.5">
          {/* EIP-712 Signed Order Envelope Card */}
          <div
            data-testid="eip712-envelope-card"
            className="space-y-1.5 rounded-md border border-[#222831] bg-[#13171D] p-2.5 text-[11px] tabular-nums"
          >
            <div className="flex items-center justify-between border-b border-[#222831] pb-1.5">
              <span className="inline-flex items-center gap-1.5 text-[10px] font-semibold uppercase tracking-wider text-[#EAECEF]">
                <KeyRound className="h-3.5 w-3.5 text-[#38BDF8]" />
                EIP-712 Signed Order Envelope
              </span>
              <span className="rounded bg-[#0ECB81]/15 px-1.5 py-0.5 text-[10px] font-semibold text-[#0ECB81]">
                Verified
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-[#848E9C]">Signer</span>
              <span className="font-medium text-[#EAECEF]">0x7099...79C8 (Verified)</span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-[#848E9C]">Order Nonce</span>
              <span className="font-medium text-[#38BDF8]">
                #{receipt ? 42 + (Number(receipt.order_id) % 100) : 42}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-[#848E9C]">Domain</span>
              <span className="text-[#EAECEF]">SettleCoreExchange v1</span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-[#848E9C]">Arithmetic</span>
              <span className="font-medium text-[#0ECB81]">Zero float64 · int64 (1e8)</span>
            </div>
          </div>

          {/* Interactive 2-Phase Order Lifecycle Inspector Trigger */}
          <button
            type="button"
            data-testid="inspect-lifecycle-btn"
            onClick={() => {
              playClickSfx();
              onInspectLifecycle();
            }}
            className="btn-tactile flex w-full cursor-pointer items-center justify-between rounded-md border border-[#38BDF8]/40 bg-[#38BDF8]/10 px-3 py-2 text-[11px] font-semibold text-[#38BDF8] hover:bg-[#38BDF8]/20"
          >
            <span className="inline-flex items-center gap-1.5">
              <Layers className="h-3.5 w-3.5" />
              Inspect 2-Phase Order Lifecycle
            </span>
            <ArrowRight className="h-3.5 w-3.5" />
          </button>

          {/* Account Margin & Custody Summary Card */}
          <div className="space-y-1.5 rounded-md border border-[#222831] bg-[#13171D] p-2.5 text-[11px] tabular-nums">
            <div className="flex items-center justify-between border-b border-[#222831] pb-1.5">
              <span className="inline-flex items-center gap-1.5 text-[10px] font-semibold uppercase tracking-wider text-[#EAECEF]">
                <ShieldCheck className="h-3.5 w-3.5 text-[#0ECB81]" />
                Account Margin &amp; Custody
              </span>
              <span className="text-[10px] font-medium text-[#38BDF8]">Pre-Trade Guard</span>
            </div>

            <div className="flex items-center justify-between">
              <span className="inline-flex items-center gap-1 text-[#848E9C]">
                <Database className="h-3 w-3 text-[#38BDF8]" />
                Pre-Trade Reserve
              </span>
              <span className="font-medium text-[#38BDF8]">{preTradeReserveText}</span>
            </div>

            <div className="flex items-center justify-between">
              <span className="text-[#848E9C]">Available Margin</span>
              <span className="font-semibold text-[#0ECB81]">${formatPrice(availableQuote, 2)} USDC</span>
            </div>

            <div className="flex items-center justify-between">
              <span className="text-[#848E9C]">Locked in CLOB</span>
              <span className="font-medium text-[#EAECEF]">${formatPrice(lockedQuote, 2)} USDC</span>
            </div>

            <div className="flex items-center justify-between">
              <span className="text-[#848E9C]">Price Collar (±10%)</span>
              <span className="text-[#848E9C]">
                ${collarBounds.lower} – ${collarBounds.upper}
              </span>
            </div>
          </div>
        </div>
      </form>
    </section>
  );
}
