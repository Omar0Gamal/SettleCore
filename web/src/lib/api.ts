import { encodePacked, keccak256, padHex, type Hex } from "viem";
import { getDemoFrame, getDemoNowMs, isDemoMode, mulberry32 } from "./demo";
import {
  addDecimalStrings,
  compareDecimalStrings,
  mulDecimalStrings,
  parseDecimalToScaled,
  subDecimalStrings,
} from "./format";
import { ASSET_SPECS, type CustodyAsset } from "./wagmi";

export type MarketPair = "ETH/USDC" | "BTC/USDC";
export type OrderSide = "BUY" | "SELL";
export type OrderType = "LIMIT" | "MARKET";
export type TimeInForce = "GTC" | "IOC" | "FOK";
export type StpMode = "CANCEL_NEWEST" | "CANCEL_OLDEST" | "CANCEL_BOTH";

export const DEFAULT_USER_ID = "8e3b91c0-4a12-4e8d-9b1c-001122334455";
export const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

export interface SubmitOrderRequest {
  market_id: MarketPair;
  side: OrderSide;
  order_type: OrderType;
  time_in_force: TimeInForce;
  stp_mode: StpMode;
  price: string;
  quantity: string;
}

export interface OrderFill {
  trade_id: string;
  maker_order_id: string;
  price: string;
  quantity: string;
  quote_notional: string;
}

export interface SubmitOrderResponse {
  order_id: string;
  idempotency_key: string;
  wal_seq: number;
  market_id: MarketPair;
  status: "OPEN" | "PARTIALLY_FILLED" | "FILLED" | "CANCELED" | "REJECTED";
  filled_quantity: string;
  remaining_quantity: string;
  match_latency_ns: number;
  fills: OrderFill[];
  source: "live" | "simulated";
}

export interface CancelOrderResponse {
  order_id: string;
  wal_seq: number;
  status: "CANCELED";
  released_locked_amount: string;
}

export interface AccountBalance {
  asset: CustodyAsset;
  available: string;
  locked: string;
}

export interface BalancesResponse {
  user_id: string;
  balances: AccountBalance[];
}

export interface OpenOrder {
  order_id: string;
  market_id: MarketPair;
  side: OrderSide;
  order_type: OrderType;
  time_in_force: TimeInForce;
  stp_mode: StpMode;
  price: string;
  quantity: string;
  filled_quantity: string;
  remaining_quantity: string;
  wal_seq: number;
  status: "OPEN" | "PARTIALLY_FILLED";
  created_at: string;
}

export interface RecentFill {
  trade_id: string;
  order_id: string;
  market_id: MarketPair;
  side: OrderSide;
  price: string;
  quantity: string;
  quote_notional: string;
  fee_usdc: string;
  wal_seq: number;
  match_latency_ns: number;
  executed_at: string;
}

export interface CustodyTransfer {
  id: string;
  direction: "DEPOSIT" | "WITHDRAWAL";
  asset: CustodyAsset;
  amount: string;
  tx_hash: string;
  block_number: number;
  confirmations: number;
  required_confirmations: number;
  status: "PENDING" | "CONFIRMED" | "ORPHANED" | "QUEUED" | "SUBMITTED";
  signing_tier?: "HOT" | "COLD_COSIGNED";
  gas_bump_count?: number;
  timestamp: string;
}

export interface WithdrawalRequest {
  asset: CustodyAsset;
  amount: string;
  recipient_address: string;
}

export interface WithdrawalResponse {
  withdrawal_id: string;
  asset: CustodyAsset;
  amount: string;
  raw_amount_wei: string;
  signing_tier: "HOT" | "COLD_COSIGNED";
  voucher_nonce: number;
  status: "QUEUED";
}

export type AssetBalanceMap = Record<CustodyAsset, string>;

export interface MerklePathNode {
  position: "LEFT" | "RIGHT";
  sibling_hash: Hex;
  sibling_sums?: AssetBalanceMap;
  cumulative_sums?: AssetBalanceMap;
}

export interface SolvencyProofResponse {
  snapshot_id: string;
  merkle_root: Hex;
  evm_block_number: number;
  publish_tx_hash: Hex;
  attester_address?: string;
  invariant_ok: boolean;
  is_solvent?: Record<CustodyAsset, boolean>;
  root_sums?: AssetBalanceMap;
  vault_reserves: AssetBalanceMap;
  total_liabilities: AssetBalanceMap;
  user_leaf: {
    user_id: string;
    nonce: number;
    balances: AssetBalanceMap;
    leaf_hash: Hex;
  };
  merkle_path: MerklePathNode[];
}

export interface ChaosActionResponse {
  ok: boolean;
  drill: "reorg" | "rpc-failover" | "gas-spike" | "flash-crash" | "gateway-crash" | "reset";
  message: string;
  reorg_rollbacks_total?: number;
  active_rpc?: string;
  stuck_tx_gas_bumps?: number;
  circuit_breaker_status?: "NORMAL" | "CANCEL_ONLY" | "HALTED (>10% DEV)";
  reserves_invariant_ok?: boolean;
  gateway_crash_rollbacks?: number;
  aborted_order_id?: string;
}

export type ChaosListener = (event: ChaosActionResponse) => void;
export type OrderListener = (order: SubmitOrderResponse, req: SubmitOrderRequest) => void;

const chaosListeners = new Set<ChaosListener>();
const orderListeners = new Set<OrderListener>();

export function subscribeChaosEvents(fn: ChaosListener): () => void {
  chaosListeners.add(fn);
  return () => chaosListeners.delete(fn);
}

export function subscribeOrderEvents(fn: OrderListener): () => void {
  orderListeners.add(fn);
  return () => orderListeners.delete(fn);
}

function createIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `sc-${Date.now()}-${Math.floor(Math.random() * 1_000_000)}`;
}

/**
 * Computes domain-separated Keccak-256 leaf matching ADR-005:
 * Leaf_u = keccak256(0x00 || user_id || nonce || ETH || USDC || WBTC || USDT)
 */
export function computeMerkleLeafHash(
  userId: string,
  nonce: number,
  balances: Record<CustodyAsset, string>
): Hex {
  const cleanedHex = userId
    .replace(/-/g, "")
    .replace(/[^0-9a-fA-F]/g, "")
    .slice(0, 32)
    .padStart(32, "0");
  const userBytes = padHex(`0x${cleanedHex}` as Hex, { size: 16 });
  return keccak256(
    encodePacked(
      ["uint8", "bytes16", "uint64", "uint256", "uint256", "uint256", "uint256"],
      [
        0,
        userBytes,
        BigInt(nonce),
        BigInt(balances.ETH),
        BigInt(balances.USDC),
        BigInt(balances.WBTC),
        BigInt(balances.USDT),
      ]
    )
  );
}

/**
 * Computes domain-separated internal Merkle node matching ADR-005:
 * Node_{L,R} = keccak256(0x01 || Hash_L || Hash_R)
 */
export function computeMerkleInternalNode(left: Hex, right: Hex): Hex {
  return keccak256(encodePacked(["uint8", "bytes32", "bytes32"], [1, left, right]));
}

/**
 * Sums two raw uint256 asset maps deterministically via BigInt.
 */
export function sumRawAssetMaps(a: AssetBalanceMap, b: AssetBalanceMap): AssetBalanceMap {
  return {
    ETH: (BigInt(a.ETH) + BigInt(b.ETH)).toString(),
    USDC: (BigInt(a.USDC) + BigInt(b.USDC)).toString(),
    WBTC: (BigInt(a.WBTC) + BigInt(b.WBTC)).toString(),
    USDT: (BigInt(a.USDT) + BigInt(b.USDT)).toString(),
  };
}

/**
 * Computes domain-separated internal Merkle Sum Tree node matching ADR-008:
 * Node_{L,R} = keccak256(0x01 || Hash_L || Hash_R || sumETH || sumUSDC || sumWBTC || sumUSDT)
 */
export function computeMerkleSumInternalNode(
  leftHash: Hex,
  rightHash: Hex,
  leftSums: AssetBalanceMap,
  rightSums: AssetBalanceMap
): { hash: Hex; sums: AssetBalanceMap } {
  const sums = sumRawAssetMaps(leftSums, rightSums);
  const hash = keccak256(
    encodePacked(
      ["uint8", "bytes32", "bytes32", "uint256", "uint256", "uint256", "uint256"],
      [
        1,
        leftHash,
        rightHash,
        BigInt(sums.ETH),
        BigInt(sums.USDC),
        BigInt(sums.WBTC),
        BigInt(sums.USDT),
      ]
    )
  );
  return { hash, sums };
}

// Deterministic local simulation state so the UI is 100% interactive without a running backend.
interface LocalSimState {
  walSeq: number;
  nextOrderId: number;
  nextTradeId: number;
  nextNonce: number;
  blockNumber: number;
  circuitBreaker: "NORMAL" | "CANCEL_ONLY" | "HALTED (>10% DEV)";
  activeRpc: string;
  reorgRollbacks: number;
  gasBumps: number;
  gatewayCrashRollbacks: number;
  lastAbortedOrderId: string | null;
  reservesInvariantOk: boolean;
  markPrices: Record<MarketPair, string>;
  balances: AccountBalance[];
  openOrders: OpenOrder[];
  recentFills: RecentFill[];
  transfers: CustodyTransfer[];
}

const INITIAL_SIM_EPOCH_MS = Date.parse("2026-11-20T01:30:00.000Z");

function createInitialSimState(): LocalSimState {
  return {
    walSeq: 148209,
    nextOrderId: 9004130,
    nextTradeId: 55020,
    nextNonce: 10043,
    blockNumber: 103,
    circuitBreaker: "NORMAL",
    activeRpc: "rpc-proxy-1 (PRIMARY)",
    reorgRollbacks: 0,
    gasBumps: 2,
    gatewayCrashRollbacks: 0,
    lastAbortedOrderId: null,
    reservesInvariantOk: true,
    markPrices: {
      "ETH/USDC": "3125.00000000",
      "BTC/USDC": "92450.00000000",
    },
    balances: [
      { asset: "ETH", available: "14.25000000", locked: "2.00000000" },
      { asset: "USDC", available: "48210.50000000", locked: "4688.25000000" },
      { asset: "WBTC", available: "0.85000000", locked: "0.00000000" },
      { asset: "USDT", available: "10000.00000000", locked: "0.00000000" },
    ],
    openOrders: [
      {
        order_id: "9004128",
        market_id: "ETH/USDC",
        side: "BUY",
        order_type: "LIMIT",
        time_in_force: "GTC",
        stp_mode: "CANCEL_NEWEST",
        price: "3124.50000000",
        quantity: "2.50000000",
        filled_quantity: "1.00000000",
        remaining_quantity: "1.50000000",
        wal_seq: 148204,
        status: "PARTIALLY_FILLED",
        created_at: new Date(INITIAL_SIM_EPOCH_MS - 45_000).toISOString(),
      },
      {
        order_id: "9004125",
        market_id: "ETH/USDC",
        side: "SELL",
        order_type: "LIMIT",
        time_in_force: "GTC",
        stp_mode: "CANCEL_NEWEST",
        price: "3128.00000000",
        quantity: "2.00000000",
        filled_quantity: "0.00000000",
        remaining_quantity: "2.00000000",
        wal_seq: 148192,
        status: "OPEN",
        created_at: new Date(INITIAL_SIM_EPOCH_MS - 120_000).toISOString(),
      },
      {
        order_id: "9004122",
        market_id: "ETH/USDC",
        side: "BUY",
        order_type: "LIMIT",
        time_in_force: "GTC",
        stp_mode: "CANCEL_NEWEST",
        price: "3118.00000000",
        quantity: "4.20000000",
        filled_quantity: "0.00000000",
        remaining_quantity: "4.20000000",
        wal_seq: 148185,
        status: "OPEN",
        created_at: new Date(INITIAL_SIM_EPOCH_MS - 195_000).toISOString(),
      },
      {
        order_id: "9004119",
        market_id: "BTC/USDC",
        side: "SELL",
        order_type: "LIMIT",
        time_in_force: "GTC",
        stp_mode: "CANCEL_OLDEST",
        price: "92680.00000000",
        quantity: "0.35000000",
        filled_quantity: "0.10000000",
        remaining_quantity: "0.25000000",
        wal_seq: 148179,
        status: "PARTIALLY_FILLED",
        created_at: new Date(INITIAL_SIM_EPOCH_MS - 260_000).toISOString(),
      },
      {
        order_id: "9004114",
        market_id: "ETH/USDC",
        side: "SELL",
        order_type: "LIMIT",
        time_in_force: "GTC",
        stp_mode: "CANCEL_NEWEST",
        price: "3134.50000000",
        quantity: "3.50000000",
        filled_quantity: "0.00000000",
        remaining_quantity: "3.50000000",
        wal_seq: 148168,
        status: "OPEN",
        created_at: new Date(INITIAL_SIM_EPOCH_MS - 310_000).toISOString(),
      },
      {
        order_id: "9004109",
        market_id: "BTC/USDC",
        side: "BUY",
        order_type: "LIMIT",
        time_in_force: "GTC",
        stp_mode: "CANCEL_BOTH",
        price: "92150.00000000",
        quantity: "0.40000000",
        filled_quantity: "0.00000000",
        remaining_quantity: "0.40000000",
        wal_seq: 148155,
        status: "OPEN",
        created_at: new Date(INITIAL_SIM_EPOCH_MS - 410_000).toISOString(),
      },
      {
        order_id: "9004104",
        market_id: "ETH/USDC",
        side: "BUY",
        order_type: "LIMIT",
        time_in_force: "GTC",
        stp_mode: "CANCEL_NEWEST",
        price: "3105.00000000",
        quantity: "5.00000000",
        filled_quantity: "0.00000000",
        remaining_quantity: "5.00000000",
        wal_seq: 148142,
        status: "OPEN",
        created_at: new Date(INITIAL_SIM_EPOCH_MS - 520_000).toISOString(),
      },
    ],
    recentFills: [
      {
        trade_id: "55019",
        order_id: "9004128",
        market_id: "ETH/USDC",
        side: "BUY",
        price: "3124.50000000",
        quantity: "1.00000000",
        quote_notional: "3124.50000000",
        fee_usdc: "1.56225000",
        wal_seq: 148204,
        match_latency_ns: 192,
        executed_at: new Date(INITIAL_SIM_EPOCH_MS - 45_000).toISOString(),
      },
      {
        trade_id: "55018",
        order_id: "9004119",
        market_id: "BTC/USDC",
        side: "SELL",
        price: "92680.00000000",
        quantity: "0.10000000",
        quote_notional: "9268.00000000",
        fee_usdc: "4.63400000",
        wal_seq: 148179,
        match_latency_ns: 185,
        executed_at: new Date(INITIAL_SIM_EPOCH_MS - 110_000).toISOString(),
      },
      {
        trade_id: "55016",
        order_id: "9004118",
        market_id: "ETH/USDC",
        side: "SELL",
        price: "3126.00000000",
        quantity: "1.50000000",
        quote_notional: "4689.00000000",
        fee_usdc: "2.34450000",
        wal_seq: 148188,
        match_latency_ns: 178,
        executed_at: new Date(INITIAL_SIM_EPOCH_MS - 165_000).toISOString(),
      },
      {
        trade_id: "55011",
        order_id: "9004102",
        market_id: "BTC/USDC",
        side: "BUY",
        price: "92440.00000000",
        quantity: "0.25000000",
        quote_notional: "23110.00000000",
        fee_usdc: "11.55500000",
        wal_seq: 148160,
        match_latency_ns: 214,
        executed_at: new Date(INITIAL_SIM_EPOCH_MS - 340_000).toISOString(),
      },
    ],
    transfers: [
      {
        id: "dep-0x9a41",
        direction: "DEPOSIT",
        asset: "ETH",
        amount: "5.00000000",
        tx_hash: "0x9a41e72b88c01d3f4a5b6c7d8e9f0123456789abcdef0123456789abcdef1102",
        block_number: 103,
        confirmations: 3,
        required_confirmations: 3,
        status: "CONFIRMED",
        timestamp: new Date(INITIAL_SIM_EPOCH_MS - 180_000).toISOString(),
      },
      {
        id: "wd-10041",
        direction: "WITHDRAWAL",
        asset: "USDC",
        amount: "5000.00000000",
        tx_hash: "0x7c83f10293847561029384756102938475610293847561029384756102938475",
        block_number: 101,
        confirmations: 5,
        required_confirmations: 3,
        status: "CONFIRMED",
        signing_tier: "HOT",
        gas_bump_count: 1,
        timestamp: new Date(INITIAL_SIM_EPOCH_MS - 260_000).toISOString(),
      },
      {
        id: "dep-0x4b12",
        direction: "DEPOSIT",
        asset: "USDC",
        amount: "25000.00000000",
        tx_hash: "0x4b12c890123456789abcdef0123456789abcdef0123456789abcdef012345678",
        block_number: 98,
        confirmations: 8,
        required_confirmations: 3,
        status: "CONFIRMED",
        timestamp: new Date(INITIAL_SIM_EPOCH_MS - 420_000).toISOString(),
      },
      {
        id: "dep-0x8d2e",
        direction: "DEPOSIT",
        asset: "WBTC",
        amount: "0.85000000",
        tx_hash: "0x8d2e9911aabbccdd00112233445566778899aabbccdd00112233445566778899",
        block_number: 94,
        confirmations: 12,
        required_confirmations: 3,
        status: "CONFIRMED",
        timestamp: new Date(INITIAL_SIM_EPOCH_MS - 640_000).toISOString(),
      },
      {
        id: "dep-0x3f71",
        direction: "DEPOSIT",
        asset: "USDT",
        amount: "10000.00000000",
        tx_hash: "0x3f71a1234567890abcdef1234567890abcdef1234567890abcdef1234567890a",
        block_number: 90,
        confirmations: 16,
        required_confirmations: 3,
        status: "CONFIRMED",
        timestamp: new Date(INITIAL_SIM_EPOCH_MS - 880_000).toISOString(),
      },
      {
        id: "wd-10039",
        direction: "WITHDRAWAL",
        asset: "ETH",
        amount: "12.50000000",
        tx_hash: "0x6e54b0987654321fedcba0987654321fedcba0987654321fedcba0987654321f",
        block_number: 86,
        confirmations: 20,
        required_confirmations: 3,
        status: "CONFIRMED",
        signing_tier: "COLD_COSIGNED",
        gas_bump_count: 1,
        timestamp: new Date(INITIAL_SIM_EPOCH_MS - 1_120_000).toISOString(),
      },
    ],
  };
}

const sim: LocalSimState = createInitialSimState();
let liveGatewayReachable = false;

export function isLiveGatewayReachable(): boolean {
  return liveGatewayReachable;
}

export function getSimSnapshot(): Readonly<LocalSimState> {
  return sim;
}

async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  if (isDemoMode()) {
    liveGatewayReachable = false;
    throw new Error("DEMO_MODE_SIMULATED");
  }
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 1600);
  try {
    const res = await fetch(`${API_BASE_URL}${path}`, {
      ...init,
      signal: controller.signal,
      headers: {
        "Content-Type": "application/json",
        "X-SettleCore-User-Id": DEFAULT_USER_ID,
        ...(init?.headers ?? {}),
      },
    });
    if (!res.ok) {
      const errBody = (await res.json().catch(() => ({}))) as { error?: string; code?: string };
      throw new Error(errBody.code ?? errBody.error ?? `HTTP ${res.status}`);
    }
    liveGatewayReachable = true;
    return (await res.json()) as T;
  } catch (err) {
    if (!(err instanceof Error && err.message.startsWith("ERR_"))) {
      liveGatewayReachable = false;
    }
    throw err;
  } finally {
    clearTimeout(timeout);
  }
}

export async function submitOrder(req: SubmitOrderRequest): Promise<SubmitOrderResponse> {
  const idempotencyKey = createIdempotencyKey();
  try {
    const live = await apiFetch<Omit<SubmitOrderResponse, "source">>("/v1/orders", {
      method: "POST",
      headers: { "Idempotency-Key": idempotencyKey },
      body: JSON.stringify(req),
    });
    const result: SubmitOrderResponse = { ...live, source: "live" };
    orderListeners.forEach((fn) => fn(result, req));
    return result;
  } catch (err) {
    // Re-throw explicit validation/exchange errors from live server
    if (err instanceof Error && err.message.startsWith("ERR_")) {
      throw err;
    }
    return simulateOrderLocally(req, idempotencyKey);
  }
}

function simulateOrderLocally(
  req: SubmitOrderRequest,
  idempotencyKey: string
): SubmitOrderResponse {
  const mark = sim.markPrices[req.market_id];
  const priceScaled = parseDecimalToScaled(req.price, 8) ?? 0n;
  const markScaled = parseDecimalToScaled(mark, 8) ?? 1n;

  if (sim.circuitBreaker !== "NORMAL") {
    throw new Error("ERR_CIRCUIT_BREAKER_TRIPPED");
  }

  if (req.order_type === "LIMIT" && priceScaled > 0n) {
    const diff = priceScaled > markScaled ? priceScaled - markScaled : markScaled - priceScaled;
    if (diff * 10000n > markScaled * 1000n) {
      throw new Error("ERR_CIRCUIT_BREAKER_TRIPPED");
    }
  }

  const baseAsset: CustodyAsset = req.market_id === "ETH/USDC" ? "ETH" : "WBTC";
  const quoteAsset: CustodyAsset = "USDC";

  const effectivePrice = req.order_type === "MARKET" ? mark : req.price;
  const requiredQuote = mulDecimalStrings(effectivePrice, req.quantity, 8);

  const baseBal = sim.balances.find((b) => b.asset === baseAsset);
  const quoteBal = sim.balances.find((b) => b.asset === quoteAsset);

  if (req.side === "BUY" && quoteBal) {
    if (compareDecimalStrings(quoteBal.available, requiredQuote) < 0) {
      throw new Error("ERR_INSUFFICIENT_AVAILABLE_BALANCE");
    }
  }
  if (req.side === "SELL" && baseBal) {
    if (compareDecimalStrings(baseBal.available, req.quantity) < 0) {
      throw new Error("ERR_INSUFFICIENT_AVAILABLE_BALANCE");
    }
  }

  sim.walSeq += 1;
  const orderId = String(sim.nextOrderId++);
  const fills: OrderFill[] = [];
  let filledQty = "0.00000000";
  let remainingQty = req.quantity;
  let status: SubmitOrderResponse["status"] = "OPEN";

  const crossesMarket =
    req.order_type === "MARKET" ||
    (req.side === "BUY" && compareDecimalStrings(req.price, mark) >= 0) ||
    (req.side === "SELL" && compareDecimalStrings(req.price, mark) <= 0);

  if (crossesMarket) {
    const isPartial = req.time_in_force === "GTC" && req.order_type === "LIMIT" && compareDecimalStrings(req.quantity, "1.00000000") > 0;
    const fillAmount = isPartial ? "1.00000000" : req.quantity;
    filledQty = fillAmount;
    remainingQty = subDecimalStrings(req.quantity, fillAmount, 8);
    status = compareDecimalStrings(remainingQty, "0.00000000") === 0 ? "FILLED" : "PARTIALLY_FILLED";

    const fillNotional = mulDecimalStrings(mark, fillAmount, 8);
    const tradeId = String(sim.nextTradeId++);
    fills.push({
      trade_id: tradeId,
      maker_order_id: String(orderId === "9004130" ? 9004110 : sim.nextOrderId - 20),
      price: mark,
      quantity: fillAmount,
      quote_notional: fillNotional,
    });

    const feeUsdc = mulDecimalStrings(fillNotional, "0.00050000", 8);
    const rng = mulberry32(0x5E771EC0 + sim.walSeq + getDemoFrame());
    const detLatencyNs = 183 + Math.floor(rng() * 64);
    sim.recentFills.unshift({
      trade_id: tradeId,
      order_id: orderId,
      market_id: req.market_id,
      side: req.side,
      price: mark,
      quantity: fillAmount,
      quote_notional: fillNotional,
      fee_usdc: feeUsdc,
      wal_seq: sim.walSeq,
      match_latency_ns: detLatencyNs,
      executed_at: new Date(getDemoNowMs()).toISOString(),
    });

    if (req.side === "BUY" && quoteBal && baseBal) {
      quoteBal.available = subDecimalStrings(quoteBal.available, fillNotional, 8);
      baseBal.available = addDecimalStrings(baseBal.available, fillAmount, 8);
    } else if (req.side === "SELL" && quoteBal && baseBal) {
      baseBal.available = subDecimalStrings(baseBal.available, fillAmount, 8);
      quoteBal.available = addDecimalStrings(quoteBal.available, fillNotional, 8);
    }
  }

  if (status === "OPEN" || status === "PARTIALLY_FILLED") {
    if (req.time_in_force === "IOC" || req.time_in_force === "FOK") {
      status = filledQty === "0.00000000" ? "CANCELED" : "FILLED";
      remainingQty = "0.00000000";
    } else {
      if (req.side === "BUY" && quoteBal) {
        const lockQuote = mulDecimalStrings(effectivePrice, remainingQty, 8);
        quoteBal.available = subDecimalStrings(quoteBal.available, lockQuote, 8);
        quoteBal.locked = addDecimalStrings(quoteBal.locked, lockQuote, 8);
      } else if (req.side === "SELL" && baseBal) {
        baseBal.available = subDecimalStrings(baseBal.available, remainingQty, 8);
        baseBal.locked = addDecimalStrings(baseBal.locked, remainingQty, 8);
      }

      sim.openOrders.unshift({
        order_id: orderId,
        market_id: req.market_id,
        side: req.side,
        order_type: req.order_type,
        time_in_force: req.time_in_force,
        stp_mode: req.stp_mode,
        price: effectivePrice,
        quantity: req.quantity,
        filled_quantity: filledQty,
        remaining_quantity: remainingQty,
        wal_seq: sim.walSeq,
        status,
        created_at: new Date(getDemoNowMs()).toISOString(),
      });
    }
  }

  const resRng = mulberry32(0x5E771EC0 + sim.walSeq + 7);
  const latencyNs = 183 + Math.floor(resRng() * 58);
  const res: SubmitOrderResponse = {
    order_id: orderId,
    idempotency_key: idempotencyKey,
    wal_seq: sim.walSeq,
    market_id: req.market_id,
    status,
    filled_quantity: filledQty,
    remaining_quantity: remainingQty,
    match_latency_ns: latencyNs,
    fills,
    source: "simulated",
  };

  orderListeners.forEach((fn) => fn(res, req));
  return res;
}

export async function cancelOrder(orderId: string, marketId: MarketPair): Promise<CancelOrderResponse> {
  try {
    return await apiFetch<CancelOrderResponse>(
      `/v1/orders/${encodeURIComponent(orderId)}?market_id=${encodeURIComponent(marketId)}`,
      { method: "DELETE" }
    );
  } catch {
    sim.walSeq += 1;
    const idx = sim.openOrders.findIndex((o) => o.order_id === orderId);
    let released = "0.00000000";
    if (idx !== -1) {
      const order = sim.openOrders[idx];
      if (order) {
        if (order.side === "BUY") {
          const quoteBal = sim.balances.find((b) => b.asset === "USDC");
          released = mulDecimalStrings(order.price, order.remaining_quantity, 8);
          if (quoteBal) {
            quoteBal.locked = subDecimalStrings(quoteBal.locked, released, 8);
            quoteBal.available = addDecimalStrings(quoteBal.available, released, 8);
          }
        } else {
          const baseAsset: CustodyAsset = order.market_id === "ETH/USDC" ? "ETH" : "WBTC";
          const baseBal = sim.balances.find((b) => b.asset === baseAsset);
          released = order.remaining_quantity;
          if (baseBal) {
            baseBal.locked = subDecimalStrings(baseBal.locked, released, 8);
            baseBal.available = addDecimalStrings(baseBal.available, released, 8);
          }
        }
        sim.openOrders.splice(idx, 1);
      }
    }
    return {
      order_id: orderId,
      wal_seq: sim.walSeq,
      status: "CANCELED",
      released_locked_amount: released,
    };
  }
}

export async function cancelAllOrders(): Promise<number> {
  const open = await getOpenOrders();
  for (const order of open) {
    await cancelOrder(order.order_id, order.market_id);
  }
  return open.length;
}

export async function getBalances(): Promise<BalancesResponse> {
  try {
    return await apiFetch<BalancesResponse>("/v1/accounts/balances");
  } catch {
    return {
      user_id: DEFAULT_USER_ID,
      balances: sim.balances.map((b) => ({ ...b })),
    };
  }
}

export async function getOpenOrders(): Promise<OpenOrder[]> {
  try {
    const data = await apiFetch<{ orders: OpenOrder[] }>("/v1/orders");
    return data.orders;
  } catch {
    return sim.openOrders.map((o) => ({ ...o }));
  }
}

export async function getRecentFills(): Promise<RecentFill[]> {
  try {
    const data = await apiFetch<{ fills: RecentFill[] }>("/v1/trades");
    return data.fills;
  } catch {
    return sim.recentFills.map((f) => ({ ...f }));
  }
}

export async function getTransfers(): Promise<CustodyTransfer[]> {
  try {
    const data = await apiFetch<{ transfers: CustodyTransfer[] }>("/v1/transfers");
    return data.transfers;
  } catch {
    return sim.transfers.map((t) => ({ ...t }));
  }
}

export async function requestWithdrawal(req: WithdrawalRequest): Promise<WithdrawalResponse> {
  const idempotencyKey = createIdempotencyKey();
  try {
    return await apiFetch<WithdrawalResponse>("/v1/withdrawals", {
      method: "POST",
      headers: { "Idempotency-Key": idempotencyKey },
      body: JSON.stringify(req),
    });
  } catch {
    const spec = ASSET_SPECS[req.asset];
    const isCold = compareDecimalStrings(req.amount, spec.hotPerTxCap) > 0;
    const bal = sim.balances.find((b) => b.asset === req.asset);
    if (bal && compareDecimalStrings(bal.available, req.amount) >= 0) {
      bal.available = subDecimalStrings(bal.available, req.amount, 8);
      bal.locked = addDecimalStrings(bal.locked, req.amount, 8);
    }
    const rawWei = (parseDecimalToScaled(req.amount, spec.decimals) ?? 0n).toString();
    const nonce = sim.nextNonce++;
    const wdId = createIdempotencyKey();
    sim.transfers.unshift({
      id: `wd-${nonce}`,
      direction: "WITHDRAWAL",
      asset: req.asset,
      amount: req.amount,
      tx_hash: `0x${nonce.toString(16).padStart(8, "0")}a8f0211928374655647382910abcdef1234567890abcdef1234567890`,
      block_number: sim.blockNumber,
      confirmations: 0,
      required_confirmations: 3,
      status: "QUEUED",
      signing_tier: isCold ? "COLD_COSIGNED" : "HOT",
      gas_bump_count: 0,
      timestamp: new Date().toISOString(),
    });
    return {
      withdrawal_id: wdId,
      asset: req.asset,
      amount: req.amount,
      raw_amount_wei: rawWei,
      signing_tier: isCold ? "COLD_COSIGNED" : "HOT",
      voucher_nonce: nonce,
      status: "QUEUED",
    };
  }
}

export async function getSolvencyProof(): Promise<SolvencyProofResponse> {
  try {
    return await apiFetch<SolvencyProofResponse>("/v1/solvency/proof");
  } catch {
    const ethBal = sim.balances.find((b) => b.asset === "ETH");
    const usdcBal = sim.balances.find((b) => b.asset === "USDC");
    const wbtcBal = sim.balances.find((b) => b.asset === "WBTC");
    const usdtBal = sim.balances.find((b) => b.asset === "USDT");

    const ethTotal = addDecimalStrings(ethBal?.available ?? "0", ethBal?.locked ?? "0", 8);
    const usdcTotal = addDecimalStrings(usdcBal?.available ?? "0", usdcBal?.locked ?? "0", 8);
    const wbtcTotal = addDecimalStrings(wbtcBal?.available ?? "0", wbtcBal?.locked ?? "0", 8);
    const usdtTotal = addDecimalStrings(usdtBal?.available ?? "0", usdtBal?.locked ?? "0", 8);

    const userRawBalances: AssetBalanceMap = {
      ETH: (parseDecimalToScaled(ethTotal, 18) ?? 0n).toString(),
      USDC: (parseDecimalToScaled(usdcTotal, 6) ?? 0n).toString(),
      WBTC: (parseDecimalToScaled(wbtcTotal, 8) ?? 0n).toString(),
      USDT: (parseDecimalToScaled(usdtTotal, 6) ?? 0n).toString(),
    };

    const sib1RawSums: AssetBalanceMap = {
      ETH: (parseDecimalToScaled("48.00000000", 18) ?? 0n).toString(),
      USDC: (parseDecimalToScaled("412500.00000000", 6) ?? 0n).toString(),
      WBTC: (parseDecimalToScaled("1.80000000", 8) ?? 0n).toString(),
      USDT: (parseDecimalToScaled("112000.00000000", 6) ?? 0n).toString(),
    };

    const sib2RawSums: AssetBalanceMap = {
      ETH: (parseDecimalToScaled("64.00000000", 18) ?? 0n).toString(),
      USDC: (parseDecimalToScaled("526000.00000000", 6) ?? 0n).toString(),
      WBTC: (parseDecimalToScaled("2.35000000", 8) ?? 0n).toString(),
      USDT: (parseDecimalToScaled("137500.00000000", 6) ?? 0n).toString(),
    };

    const nonce = 1048;
    const leafHash = computeMerkleLeafHash(DEFAULT_USER_ID, nonce, userRawBalances);
    const sibling1: Hex = "0x77b12c34d56e78f90123456789abcdef0123456789abcdef0123456789abcdef";
    const sibling2: Hex = "0x44e98a12b34c56d7890123456789abcdef0123456789abcdef0123456789abcd";
    const h1 = computeMerkleSumInternalNode(leafHash, sibling1, userRawBalances, sib1RawSums);
    const rootNode = computeMerkleSumInternalNode(sibling2, h1.hash, sib2RawSums, h1.sums);

    return {
      snapshot_id: "a1b2c3d4-0000-4000-8000-010203040506",
      merkle_root: rootNode.hash,
      evm_block_number: sim.blockNumber + 945,
      publish_tx_hash: "0x3c91a8f0211928374655647382910abcdef1234567890abcdef1234567890abc",
      attester_address: "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC",
      invariant_ok: sim.reservesInvariantOk,
      is_solvent: {
        ETH: sim.reservesInvariantOk,
        USDC: sim.reservesInvariantOk,
        WBTC: sim.reservesInvariantOk,
        USDT: sim.reservesInvariantOk,
      },
      root_sums: rootNode.sums,
      vault_reserves: {
        ETH: addDecimalStrings(ethTotal, "112.50000000", 8),
        USDC: addDecimalStrings(usdcTotal, "940000.00000000", 8),
        WBTC: addDecimalStrings(wbtcTotal, "4.20000000", 8),
        USDT: addDecimalStrings(usdtTotal, "250000.00000000", 8),
      },
      total_liabilities: {
        ETH: addDecimalStrings(ethTotal, "112.00000000", 8),
        USDC: addDecimalStrings(usdcTotal, "938500.00000000", 8),
        WBTC: addDecimalStrings(wbtcTotal, "4.15000000", 8),
        USDT: addDecimalStrings(usdtTotal, "249500.00000000", 8),
      },
      user_leaf: {
        user_id: DEFAULT_USER_ID,
        nonce,
        balances: userRawBalances,
        leaf_hash: leafHash,
      },
      merkle_path: [
        {
          position: "RIGHT",
          sibling_hash: sibling1,
          sibling_sums: sib1RawSums,
          cumulative_sums: h1.sums,
        },
        {
          position: "LEFT",
          sibling_hash: sibling2,
          sibling_sums: sib2RawSums,
          cumulative_sums: rootNode.sums,
        },
      ],
    };
  }
}

export async function triggerReorg(depth = 3): Promise<ChaosActionResponse> {
  try {
    const res = await apiFetch<ChaosActionResponse>("/v1/chaos/reorg", {
      method: "POST",
      body: JSON.stringify({ depth }),
    });
    chaosListeners.forEach((fn) => fn(res));
    return res;
  } catch {
    sim.reorgRollbacks += 1;
    sim.blockNumber += 4;
    const latestDeposit = sim.transfers.find((t) => t.direction === "DEPOSIT");
    if (latestDeposit) {
      latestDeposit.status = "ORPHANED";
      latestDeposit.confirmations = 0;
      const bal = sim.balances.find((b) => b.asset === latestDeposit.asset);
      if (bal && compareDecimalStrings(bal.available, latestDeposit.amount) >= 0) {
        bal.available = subDecimalStrings(bal.available, latestDeposit.amount, 8);
      }
    }
    const event: ChaosActionResponse = {
      ok: true,
      drill: "reorg",
      message: `3-Block Reorg detected at Block #${sim.blockNumber - 4}. Parent-hash rollback reversed orphaned deposit; reserves_invariant_ok = 1.`,
      reorg_rollbacks_total: sim.reorgRollbacks,
      reserves_invariant_ok: true,
    };
    chaosListeners.forEach((fn) => fn(event));
    return event;
  }
}

export async function triggerRpcFailover(action: "kill" | "restore" = "kill"): Promise<ChaosActionResponse> {
  try {
    const res = await apiFetch<ChaosActionResponse>("/v1/chaos/rpc-failover", {
      method: "POST",
      body: JSON.stringify({ action }),
    });
    chaosListeners.forEach((fn) => fn(res));
    return res;
  } catch {
    sim.activeRpc =
      action === "kill" ? "rpc-proxy-2 (FAILOVER)" : "rpc-proxy-1 (PRIMARY)";
    const event: ChaosActionResponse = {
      ok: true,
      drill: "rpc-failover",
      message:
        action === "kill"
          ? "Primary rpc-proxy-1 severed. Failover pool switched to rpc-proxy-2 in 184 ms (0 missed blocks)."
          : "Primary rpc-proxy-1 restored and health-checked.",
      active_rpc: sim.activeRpc,
    };
    chaosListeners.forEach((fn) => fn(event));
    return event;
  }
}

export async function triggerGasSpike(baseFeeMultiplier = 10): Promise<ChaosActionResponse> {
  try {
    const res = await apiFetch<ChaosActionResponse>("/v1/chaos/gas-spike", {
      method: "POST",
      body: JSON.stringify({ base_fee_multiplier: baseFeeMultiplier }),
    });
    chaosListeners.forEach((fn) => fn(res));
    return res;
  } catch {
    sim.gasBumps += 1;
    const latestWd = sim.transfers.find((t) => t.direction === "WITHDRAWAL");
    if (latestWd) {
      latestWd.gas_bump_count = (latestWd.gas_bump_count ?? 0) + 1;
      latestWd.status = "CONFIRMED";
    }
    const event: ChaosActionResponse = {
      ok: true,
      drill: "gas-spike",
      message: `${baseFeeMultiplier}x EIP-1559 base fee spike injected. Relayer replaced stuck tx at same EOA nonce (+15% fee bump).`,
      stuck_tx_gas_bumps: sim.gasBumps,
    };
    chaosListeners.forEach((fn) => fn(event));
    return event;
  }
}

export async function triggerFlashCrash(pair: MarketPair = "ETH/USDC", dropBps = 1500): Promise<ChaosActionResponse> {
  try {
    const res = await apiFetch<ChaosActionResponse>("/v1/chaos/flash-crash", {
      method: "POST",
      body: JSON.stringify({ pair, drop_bps: dropBps }),
    });
    chaosListeners.forEach((fn) => fn(res));
    return res;
  } catch {
    sim.circuitBreaker = "HALTED (>10% DEV)";
    const event: ChaosActionResponse = {
      ok: true,
      drill: "flash-crash",
      message: `-15% (${dropBps} bps) aggressive sweep on ${pair} tripped ±10% Circuit Breaker collar. Market switched to CANCEL_ONLY.`,
      circuit_breaker_status: sim.circuitBreaker,
    };
    chaosListeners.forEach((fn) => fn(event));
    return event;
  }
}

export async function triggerGatewayCrash(): Promise<ChaosActionResponse> {
  try {
    const res = await apiFetch<ChaosActionResponse>("/v1/chaos/gateway-crash", {
      method: "POST",
    });
    chaosListeners.forEach((fn) => fn(res));
    return res;
  } catch {
    sim.gatewayCrashRollbacks += 1;
    const abortedOrderId = String(sim.nextOrderId++);
    sim.lastAbortedOrderId = abortedOrderId;
    const event: ChaosActionResponse = {
      ok: true,
      drill: "gateway-crash",
      message: `Gateway killed mid-flight between PG Pre-Trade Reserve and C++17 WAL append. Startup recovery rolled back #${abortedOrderId} (RESERVED -> ABORTED_UNSUBMITTED); I-1 Double-Entry preserved.`,
      gateway_crash_rollbacks: sim.gatewayCrashRollbacks,
      aborted_order_id: abortedOrderId,
      reserves_invariant_ok: true,
    };
    chaosListeners.forEach((fn) => fn(event));
    return event;
  }
}

export async function resetChaos(): Promise<ChaosActionResponse> {
  try {
    const res = await apiFetch<ChaosActionResponse>("/v1/chaos/reset", {
      method: "POST",
    });
    chaosListeners.forEach((fn) => fn(res));
    return res;
  } catch {
    const fresh = createInitialSimState();
    sim.circuitBreaker = "NORMAL";
    sim.activeRpc = "rpc-proxy-1 (PRIMARY)";
    sim.transfers = fresh.transfers;
    sim.balances = fresh.balances;
    sim.recentFills = fresh.recentFills;
    sim.blockNumber = fresh.blockNumber;
    sim.reorgRollbacks = 0;
    sim.gasBumps = 2;
    sim.gatewayCrashRollbacks = 0;
    sim.lastAbortedOrderId = null;
    sim.reservesInvariantOk = true;
    const event: ChaosActionResponse = {
      ok: true,
      drill: "reset",
      message: "Normal state restored: rpc-proxy-1 primary active, Circuit Breaker NORMAL, confirmed deposits restored.",
      active_rpc: sim.activeRpc,
      circuit_breaker_status: sim.circuitBreaker,
      reorg_rollbacks_total: sim.reorgRollbacks,
      stuck_tx_gas_bumps: sim.gasBumps,
      gateway_crash_rollbacks: sim.gatewayCrashRollbacks,
      reserves_invariant_ok: true,
    };
    chaosListeners.forEach((fn) => fn(event));
    return event;
  }
}
