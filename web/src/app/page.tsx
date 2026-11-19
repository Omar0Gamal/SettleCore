"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  Activity,
  ArrowDown,
  ArrowUp,
  ChevronDown,
  Command,
  Flame,
  HelpCircle,
  Layers,
  ShieldAlert,
  ShieldCheck,
  Volume2,
  VolumeX,
  Wallet,
} from "lucide-react";
import { WagmiProvider, useConnection } from "wagmi";
import { CandlestickChart } from "@/components/CandlestickChart";
import { ChaosControlPanel } from "@/components/ChaosControlPanel";
import { CommandPalette, type CommandItem } from "@/components/CommandPalette";
import { GuidedTourBanner, TOUR_STEPS } from "@/components/GuidedTourBanner";
import { InvariantsModal } from "@/components/InvariantsModal";
import { OpenOrdersTable } from "@/components/OpenOrdersTable";
import { OrderBook } from "@/components/OrderBook";
import { OrderEntryForm } from "@/components/OrderEntryForm";
import {
  OrderLifecycleDrawer,
  buildInspectedOrderFromReceipt,
  type InspectedOrder,
} from "@/components/OrderLifecycleDrawer";
import { ProofOfReservesModal } from "@/components/ProofOfReservesModal";
import { TelemetryStrip } from "@/components/TelemetryStrip";
import { TradeTape } from "@/components/TradeTape";
import { WalletModal } from "@/components/WalletModal";
import { useMarketStream, type Timeframe } from "@/hooks/useMarketStream";
import { useTelemetryStream } from "@/hooks/useTelemetryStream";
import {
  getBalances,
  getOpenOrders,
  getRecentFills,
  isLiveGatewayReachable,
  resetChaos,
  triggerFlashCrash,
  triggerGasSpike,
  triggerGatewayCrash,
  triggerReorg,
  triggerRpcFailover,
  type AccountBalance,
  type MarketPair,
  type OpenOrder,
  type RecentFill,
  type SubmitOrderResponse,
} from "@/lib/api";
import { installDemoBridge, isDemoMode, isFrameSteppedDemo } from "@/lib/demo";
import { formatBps, formatPrice } from "@/lib/format";
import { evaluateInvariants } from "@/lib/invariants";
import { isSfxEnabled, playChaosAlertSfx, playClickSfx, setSfxEnabled } from "@/lib/sound";
import { wagmiConfig } from "@/lib/wagmi";

const queryClient = new QueryClient();
const PAIRS: MarketPair[] = ["ETH/USDC", "BTC/USDC"];

type ActiveWorkspace = "trade" | "custody" | "chaos" | "por";

const DEFAULT_INSPECTED_ORDER: InspectedOrder = {
  order_id: "9004128",
  market_id: "ETH/USDC",
  side: "BUY",
  order_type: "LIMIT",
  price: "3124.50000000",
  quantity: "2.50000000",
  filled_quantity: "1.00000000",
  remaining_quantity: "1.50000000",
  wal_seq: 148204,
  status: "PARTIALLY_FILLED",
  match_latency_ns: 183,
  source: "simulated",
  nonce: 42,
};

function TradingDeskInner() {
  const [activeWorkspace, setActiveWorkspace] = useState<ActiveWorkspace>("trade");
  const [pair, setPair] = useState<MarketPair>("ETH/USDC");
  const [pairMenuOpen, setPairMenuOpen] = useState<boolean>(false);
  const [timeframe, setTimeframe] = useState<Timeframe>("1m");
  const [orderType, setOrderType] = useState<"LIMIT" | "MARKET">("LIMIT");
  const [limitPrice, setLimitPrice] = useState<string>("3125.50");
  const [pricePulseTick, setPricePulseTick] = useState<number>(0);
  const [balances, setBalances] = useState<AccountBalance[]>([]);
  const [openOrders, setOpenOrders] = useState<OpenOrder[]>([]);
  const [recentFills, setRecentFills] = useState<RecentFill[]>([]);
  const [quickWalletOpen, setQuickWalletOpen] = useState<boolean>(false);
  const [sfxOn, setSfxOn] = useState<boolean>(() => isSfxEnabled());

  // Inspector & System Guide Overlays
  const [lifecycleOpen, setLifecycleOpen] = useState<boolean>(false);
  const [inspectedOrder, setInspectedOrder] = useState<InspectedOrder>(DEFAULT_INSPECTED_ORDER);
  const [invariantsOpen, setInvariantsOpen] = useState<boolean>(false);
  const [cmdkOpen, setCmdkOpen] = useState<boolean>(false);
  const [tourOpen, setTourOpen] = useState<boolean>(false);
  const [tourStep, setTourStep] = useState<number>(0);

  const market = useMarketStream(pair, timeframe);
  const telemetry = useTelemetryStream();
  const connection = useConnection();

  useEffect(() => {
    installDemoBridge();
    if (
      process.env.NEXT_PUBLIC_DEMO_MODE === "1" &&
      !isFrameSteppedDemo() &&
      typeof window !== "undefined"
    ) {
      const timer = window.setTimeout(() => {
        try {
          if (window.localStorage.getItem("settlecore_tour_seen") !== "1") {
            setTourOpen(true);
          }
        } catch {
          // Ignore storage restriction
        }
      }, 0);
      return () => window.clearTimeout(timer);
    }
    return undefined;
  }, []);

  const refreshAccountState = useCallback(async () => {
    const [balRes, ordRes, fillRes] = await Promise.all([
      getBalances(),
      getOpenOrders(),
      getRecentFills(),
    ]);
    setBalances(balRes.balances);
    setOpenOrders(ordRes);
    setRecentFills(fillRes);
  }, []);

  useEffect(() => {
    let cancelled = false;
    Promise.all([getBalances(), getOpenOrders(), getRecentFills()]).then(
      ([balRes, ordRes, fillRes]) => {
        if (cancelled) return;
        setBalances(balRes.balances);
        setOpenOrders(ordRes);
        setRecentFills(fillRes);
      }
    );
    return () => {
      cancelled = true;
    };
  }, []);

  // Keyboard shortcuts: 1..4 workspaces, Cmd/Ctrl+K palette, ? architecture guide
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        playClickSfx();
        setCmdkOpen((v) => !v);
        return;
      }

      const target = e.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.tagName === "SELECT" ||
          target.isContentEditable)
      ) {
        return;
      }
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key === "1") {
        playClickSfx();
        setActiveWorkspace("trade");
      } else if (e.key === "2") {
        playClickSfx();
        setActiveWorkspace("custody");
      } else if (e.key === "3") {
        playClickSfx();
        setActiveWorkspace("chaos");
      } else if (e.key === "4") {
        playClickSfx();
        setActiveWorkspace("por");
      } else if (e.key === "?") {
        e.preventDefault();
        playClickSfx();
        setTourStep(0);
        setTourOpen((v) => !v);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const handleWorkspaceSelect = (next: ActiveWorkspace) => {
    playClickSfx();
    setActiveWorkspace(next);
  };

  const handlePairChange = useCallback((nextPair: MarketPair) => {
    playClickSfx();
    setPair(nextPair);
    setLimitPrice(nextPair === "ETH/USDC" ? "3125.50" : "92450.00");
    setPairMenuOpen(false);
  }, []);

  const handleSelectBookPrice = (px: string) => {
    setOrderType("LIMIT");
    setLimitPrice(px);
    setPricePulseTick((t) => t + 1);
  };

  const handleToggleSfx = useCallback(() => {
    const next = !sfxOn;
    setSfxEnabled(next);
    setSfxOn(next);
    if (next) {
      playClickSfx();
    }
  }, [sfxOn]);

  const handleOrderSubmitted = useCallback(
    (res: SubmitOrderResponse) => {
      const latestOrder = buildInspectedOrderFromReceipt(
        res,
        "BUY",
        orderType,
        orderType === "MARKET" ? market.markPrice : limitPrice,
        res.filled_quantity !== "0.00000000" ? res.filled_quantity : res.remaining_quantity
      );
      setInspectedOrder(latestOrder);
      void refreshAccountState();
    },
    [limitPrice, market.markPrice, orderType, refreshAccountState]
  );

  const handleInspectOpenOrder = useCallback((order: OpenOrder) => {
    setInspectedOrder({
      order_id: order.order_id,
      market_id: order.market_id,
      side: order.side,
      order_type: order.order_type,
      price: order.price,
      quantity: order.quantity,
      filled_quantity: order.filled_quantity,
      remaining_quantity: order.remaining_quantity,
      wal_seq: order.wal_seq,
      status: order.status,
      match_latency_ns: 183,
      source: isDemoMode() ? "simulated" : "live",
      nonce: 42 + (Number(order.order_id) % 100),
    });
    setLifecycleOpen(true);
  }, []);

  const handleInspectFill = useCallback((fill: RecentFill) => {
    setInspectedOrder({
      order_id: fill.order_id,
      market_id: fill.market_id,
      side: fill.side,
      order_type: "LIMIT",
      price: fill.price,
      quantity: fill.quantity,
      filled_quantity: fill.quantity,
      remaining_quantity: "0.00000000",
      wal_seq: fill.wal_seq,
      status: "FILLED",
      match_latency_ns: fill.match_latency_ns,
      source: isDemoMode() ? "simulated" : "live",
      nonce: 42 + (Number(fill.order_id) % 100),
    });
    setLifecycleOpen(true);
  }, []);

  const handleTourStepSelect = useCallback((idx: number) => {
    const step = TOUR_STEPS[idx] ?? TOUR_STEPS[0]!;
    setTourStep(idx);
    setActiveWorkspace(step.workspace);
    setLifecycleOpen(Boolean(step.openLifecycle));
  }, []);

  const handleTourClose = useCallback(() => {
    setTourOpen(false);
    try {
      window.localStorage.setItem("settlecore_tour_seen", "1");
    } catch {
      // Ignore storage errors
    }
  }, []);

  const invariants = useMemo(() => evaluateInvariants(telemetry), [telemetry]);
  const passingInvariants = invariants.filter((i) => i.ok).length;
  const isLiveMode =
    !isDemoMode() && (telemetry.ws_connected || market.wsConnected || isLiveGatewayReachable());

  const commands: CommandItem[] = useMemo(
    () => [
      {
        id: "ws-trade",
        group: "Workspace",
        label: "Switch to Trade Terminal",
        shortcut: "1",
        run: () => setActiveWorkspace("trade"),
      },
      {
        id: "ws-custody",
        group: "Workspace",
        label: "Switch to Custody & Balances",
        shortcut: "2",
        run: () => setActiveWorkspace("custody"),
      },
      {
        id: "ws-chaos",
        group: "Workspace",
        label: "Switch to Chaos & Systems Lab",
        shortcut: "3",
        run: () => setActiveWorkspace("chaos"),
      },
      {
        id: "ws-por",
        group: "Workspace",
        label: "Switch to Proof of Reserves (Merkle Sum Tree)",
        shortcut: "4",
        run: () => setActiveWorkspace("por"),
      },
      {
        id: "pair-eth",
        group: "Market",
        label: "Switch Pair to ETH/USDC",
        run: () => handlePairChange("ETH/USDC"),
      },
      {
        id: "pair-btc",
        group: "Market",
        label: "Switch Pair to BTC/USDC",
        run: () => handlePairChange("BTC/USDC"),
      },
      {
        id: "ov-lifecycle",
        group: "Inspector",
        label: "Open Order Lifecycle Inspector Drawer",
        run: () => setLifecycleOpen(true),
      },
      {
        id: "ov-invariants",
        group: "Inspector",
        label: `Open Live Invariants (${passingInvariants}/6 OK)`,
        run: () => setInvariantsOpen(true),
      },
      {
        id: "ov-tour",
        group: "Guide",
        label: "Open 6-Step Architecture Guide",
        shortcut: "?",
        run: () => {
          handleTourStepSelect(0);
          setTourOpen(true);
        },
      },
      {
        id: "ov-wallet",
        group: "Custody",
        label: "Open Deposit / Withdraw Modal",
        run: () => setQuickWalletOpen(true),
      },
      {
        id: "drill-reorg",
        group: "Chaos Drill",
        label: "Fire Drill 1: 3-Block Chain Reorg Rollback",
        run: () => {
          setActiveWorkspace("chaos");
          void triggerReorg(3).then(() => {
            playChaosAlertSfx();
            void refreshAccountState();
          });
        },
      },
      {
        id: "drill-rpc",
        group: "Chaos Drill",
        label: "Fire Drill 2: Kill Primary RPC (Quorum Failover)",
        run: () => {
          setActiveWorkspace("chaos");
          void triggerRpcFailover("kill").then(() => {
            playChaosAlertSfx();
            void refreshAccountState();
          });
        },
      },
      {
        id: "drill-gas",
        group: "Chaos Drill",
        label: "Fire Drill 3: 10x EIP-1559 Gas Spike (RBF Bump)",
        run: () => {
          setActiveWorkspace("chaos");
          void triggerGasSpike(10).then(() => {
            playChaosAlertSfx();
            void refreshAccountState();
          });
        },
      },
      {
        id: "drill-flash",
        group: "Chaos Drill",
        label: "Fire Drill 4: -15% Flash Crash Circuit Breaker",
        run: () => {
          setActiveWorkspace("chaos");
          void triggerFlashCrash(pair, 1500).then(() => {
            playChaosAlertSfx();
            void refreshAccountState();
          });
        },
      },
      {
        id: "drill-gw",
        group: "Chaos Drill",
        label: "Fire Drill 5: Kill Gateway Mid-Flight (Pre-Trade Recovery)",
        run: () => {
          setActiveWorkspace("chaos");
          void triggerGatewayCrash().then(() => {
            playChaosAlertSfx();
            void refreshAccountState();
          });
        },
      },
      {
        id: "drill-reset",
        group: "Chaos Drill",
        label: "Reset Chaos Drills to Normal State",
        run: () => {
          void resetChaos().then(() => void refreshAccountState());
        },
      },
      {
        id: "toggle-sfx",
        group: "Preferences",
        label: sfxOn ? "Mute Web Audio SFX" : "Enable Web Audio SFX",
        run: handleToggleSfx,
      },
    ],
    [
      handlePairChange,
      handleToggleSfx,
      handleTourStepSelect,
      pair,
      passingInvariants,
      refreshAccountState,
      sfxOn,
    ]
  );

  const isConnected = connection.status === "connected" && connection.address;
  const isPositive = market.change24hBps >= 0;
  const isBreakerTripped =
    market.circuitBreakerTripped || telemetry.circuit_breaker_status !== "NORMAL";
  const compactTurnover = pair === "ETH/USDC" ? "$184.92M" : "$412.68M";

  return (
    <div className="flex h-screen w-screen flex-col overflow-hidden bg-[#0B0E11] text-[#EAECEF]">
      {/* 48px Primary Exchange Top Navigation Bar */}
      <header className="flex h-12 shrink-0 items-center justify-between gap-2 border-b border-[#222831] bg-[#13171D] px-3">
        {/* Left: Emblem + 4 Top-Level Workspace Tabs */}
        <div className="flex min-w-0 items-center gap-2.5">
          <div className="flex shrink-0 items-center gap-1.5">
            <div className="flex h-7 w-7 items-center justify-center rounded-md border border-[#38BDF8]/30 bg-[#1A1F27]">
              <Layers className="h-3.5 w-3.5 text-[#38BDF8]" />
            </div>
            <h1 className="font-display text-sm font-bold tracking-tight text-[#EAECEF]">
              SettleCore
            </h1>
            <span className="hidden rounded border border-[#222831] bg-[#1A1F27] px-1.5 py-0.5 text-[10px] font-medium text-[#848E9C] 2xl:inline">
              PRO
            </span>
          </div>

          <div className="h-4 w-px shrink-0 bg-[#222831]" />

          {/* 4 Top-Level Workspace Tabs */}
          <nav aria-label="Primary Exchange Workspaces" className="flex items-center gap-0.5">
            <button
              type="button"
              data-testid="nav-tab-trade"
              onClick={() => handleWorkspaceSelect("trade")}
              className={`btn-tactile inline-flex shrink-0 cursor-pointer items-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-semibold tracking-tight ${
                activeWorkspace === "trade"
                  ? "bg-[#1A1F27] text-[#EAECEF] shadow-[inset_0_-2px_0_0_#38BDF8]"
                  : "text-[#848E9C] hover:bg-[#1A1F27]/60 hover:text-[#EAECEF]"
              }`}
            >
              <Activity className="h-3.5 w-3.5 text-[#38BDF8]" />
              <span>Trade</span>
              <kbd className="hidden rounded border border-[#222831] bg-[#0B0E11] px-1 text-[9px] font-medium text-[#848E9C] xl:inline">
                1
              </kbd>
            </button>

            <button
              type="button"
              data-testid="nav-tab-custody"
              onClick={() => handleWorkspaceSelect("custody")}
              className={`btn-tactile inline-flex shrink-0 cursor-pointer items-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-semibold tracking-tight ${
                activeWorkspace === "custody"
                  ? "bg-[#1A1F27] text-[#EAECEF] shadow-[inset_0_-2px_0_0_#38BDF8]"
                  : "text-[#848E9C] hover:bg-[#1A1F27]/60 hover:text-[#EAECEF]"
              }`}
            >
              <Wallet className="h-3.5 w-3.5 text-[#38BDF8]" />
              <span>Custody &amp; Balances</span>
              <kbd className="hidden rounded border border-[#222831] bg-[#0B0E11] px-1 text-[9px] font-medium text-[#848E9C] xl:inline">
                2
              </kbd>
            </button>

            <button
              type="button"
              data-testid="nav-tab-chaos"
              onClick={() => handleWorkspaceSelect("chaos")}
              className={`btn-tactile inline-flex shrink-0 cursor-pointer items-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-semibold tracking-tight ${
                activeWorkspace === "chaos"
                  ? "bg-[#F6465D]/15 text-[#F6465D] shadow-[inset_0_-2px_0_0_#F6465D]"
                  : "text-[#848E9C] hover:bg-[#1A1F27]/60 hover:text-[#F6465D]"
              }`}
            >
              <span data-testid="chaos-panel-toggle" className="inline-flex items-center gap-1.5">
                <Flame className="h-3.5 w-3.5 text-[#F6465D]" />
                <span>Chaos &amp; Systems Lab</span>
              </span>
              <kbd className="hidden rounded border border-[#222831] bg-[#0B0E11] px-1 text-[9px] font-medium text-[#848E9C] xl:inline">
                3
              </kbd>
            </button>

            <button
              type="button"
              data-testid="nav-tab-por"
              onClick={() => handleWorkspaceSelect("por")}
              className={`btn-tactile inline-flex shrink-0 cursor-pointer items-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-semibold tracking-tight ${
                activeWorkspace === "por"
                  ? "bg-[#0ECB81]/15 text-[#0ECB81] shadow-[inset_0_-2px_0_0_#0ECB81]"
                  : "text-[#848E9C] hover:bg-[#1A1F27]/60 hover:text-[#0ECB81]"
              }`}
            >
              <span data-testid="open-por-modal-btn" className="inline-flex items-center gap-1.5">
                <ShieldCheck className="h-3.5 w-3.5 text-[#0ECB81]" />
                <span>Proof of Reserves</span>
              </span>
              <kbd className="hidden rounded border border-[#222831] bg-[#0B0E11] px-1 text-[9px] font-medium text-[#848E9C] xl:inline">
                4
              </kbd>
            </button>
          </nav>
        </div>

        {/* Right Dock: Mode Badge, Live Invariants, Order Lifecycle, Architecture Guide, ⌘K, SFX, Wallet */}
        <div className="flex shrink-0 items-center gap-1.5">
          <span
            data-testid="engine-mode-badge"
            title={
              isLiveMode
                ? "Connected to live Go Gateway & C++17 CLOB on :8080"
                : "Simulated market data & seeded deterministic engine (benchmarks real, see /docs)"
            }
            className={`hidden items-center gap-1.5 rounded border px-2 py-1 text-[10px] font-semibold tracking-tight xl:inline-flex ${
              isLiveMode
                ? "border-[#0ECB81]/40 bg-[#0ECB81]/10 text-[#0ECB81]"
                : "border-amber-400/35 bg-amber-400/10 text-amber-300"
            }`}
          >
            <span
              className={`h-1.5 w-1.5 rounded-full ${
                isLiveMode ? "bg-[#0ECB81]" : "bg-amber-300"
              }`}
            />
            {isLiveMode ? "LIVE GATEWAY :8080" : "SIMULATED ENGINE (SEEDED)"}
          </span>

          <button
            type="button"
            data-testid="open-invariants-btn"
            onClick={() => {
              playClickSfx();
              setInvariantsOpen(true);
            }}
            className={`btn-tactile inline-flex shrink-0 cursor-pointer items-center gap-1 rounded border px-2 py-1 text-[11px] font-semibold tabular-nums ${
              passingInvariants === 6
                ? "border-[#0ECB81]/40 bg-[#0ECB81]/10 text-[#0ECB81] hover:bg-[#0ECB81]/20"
                : "border-[#F6465D]/40 bg-[#F6465D]/15 text-[#F6465D]"
            }`}
          >
            <ShieldCheck className="h-3.5 w-3.5" />
            <span>Live Invariants ({passingInvariants}/6 OK)</span>
          </button>

          <button
            type="button"
            data-testid="open-lifecycle-btn"
            onClick={() => {
              playClickSfx();
              setLifecycleOpen(true);
            }}
            className="btn-tactile hidden shrink-0 cursor-pointer items-center gap-1 rounded border border-[#222831] bg-[#1A1F27] px-2 py-1 text-[11px] font-semibold text-[#EAECEF] hover:border-[#38BDF8]/50 sm:inline-flex"
          >
            <Layers className="h-3.5 w-3.5 text-[#38BDF8]" />
            <span>Order Lifecycle</span>
          </button>

          <button
            type="button"
            data-testid="open-tour-btn"
            onClick={() => {
              playClickSfx();
              handleTourStepSelect(0);
              setTourOpen(true);
            }}
            className="btn-tactile inline-flex shrink-0 cursor-pointer items-center gap-1 rounded border border-[#222831] bg-[#1A1F27] px-2 py-1 text-[11px] font-semibold text-[#EAECEF] hover:border-[#38BDF8]/50"
          >
            <HelpCircle className="h-3.5 w-3.5 text-[#38BDF8]" />
            <span className="hidden md:inline">System Guide (?)</span>
            <span className="md:hidden">?</span>
          </button>

          <button
            type="button"
            data-testid="open-cmdk-btn"
            onClick={() => {
              playClickSfx();
              setCmdkOpen(true);
            }}
            className="btn-tactile inline-flex shrink-0 cursor-pointer items-center gap-1 rounded border border-[#222831] bg-[#1A1F27] px-2 py-1 text-[11px] font-semibold text-[#EAECEF] hover:border-[#38BDF8]/50"
            title="Command Palette (Cmd/Ctrl+K)"
          >
            <Command className="h-3 w-3 text-[#38BDF8]" />
            <span>⌘K</span>
          </button>

          <button
            type="button"
            data-testid="sfx-toggle-btn"
            onClick={handleToggleSfx}
            className={`btn-tactile inline-flex shrink-0 cursor-pointer items-center gap-1 rounded border px-2 py-1 text-[11px] font-semibold tabular-nums ${
              sfxOn
                ? "border-[#38BDF8]/40 bg-[#38BDF8]/10 text-[#38BDF8]"
                : "border-[#222831] bg-[#1A1F27] text-[#848E9C]"
            }`}
          >
            {sfxOn ? <Volume2 className="h-3.5 w-3.5" /> : <VolumeX className="h-3.5 w-3.5" />}
            <span className="hidden lg:inline">SFX: {sfxOn ? "ON" : "OFF"}</span>
          </button>

          <button
            type="button"
            data-testid="open-wallet-modal-btn"
            onClick={() => {
              playClickSfx();
              setQuickWalletOpen(true);
            }}
            className="btn-tactile inline-flex shrink-0 cursor-pointer items-center gap-1.5 rounded border border-[#222831] bg-[#1A1F27] px-2 py-1 text-[11px] font-semibold tracking-tight text-[#EAECEF] hover:border-[#38BDF8]/50"
          >
            <Wallet className="h-3.5 w-3.5 text-[#38BDF8]" />
            <span>
              {isConnected
                ? `${connection.address!.slice(0, 6)}...`
                : "Deposit / Withdraw"}
            </span>
          </button>
        </div>
      </header>

      {/* Active Workspace Viewport */}
      {activeWorkspace === "trade" && (
        <div className="flex min-h-0 flex-1 flex-col">
          {/* 44px Sub-Header Market Ticker Ribbon */}
          <div className="relative flex h-11 shrink-0 items-center justify-between gap-4 border-b border-[#222831] bg-[#0B0E11] px-4">
            <div className="flex min-w-0 items-center gap-5 tabular-nums">
              {/* Interactive Pair Switcher (Popover + Quick Buttons) */}
              <div className="relative flex items-center gap-2">
                <button
                  type="button"
                  onClick={() => {
                    playClickSfx();
                    setPairMenuOpen((v) => !v);
                  }}
                  className="btn-tactile inline-flex cursor-pointer items-center gap-1.5 rounded border border-[#222831] bg-[#13171D] px-2.5 py-1 text-sm font-bold tracking-tight text-[#EAECEF] hover:border-[#38BDF8]/50"
                >
                  <span>{pair}</span>
                  <ChevronDown className="h-3.5 w-3.5 text-[#848E9C]" />
                </button>

                <div className="hidden items-center gap-1 rounded border border-[#222831] bg-[#13171D] p-0.5 sm:flex">
                  {PAIRS.map((p) => (
                    <button
                      key={p}
                      type="button"
                      onClick={() => handlePairChange(p)}
                      className={`btn-tactile cursor-pointer rounded px-2 py-0.5 text-[11px] font-semibold ${
                        pair === p
                          ? "bg-[#1A1F27] text-[#EAECEF]"
                          : "text-[#848E9C] hover:text-[#EAECEF]"
                      }`}
                    >
                      {p}
                    </button>
                  ))}
                </div>

                {pairMenuOpen && (
                  <div className="absolute left-0 top-9 z-30 w-52 rounded-md border border-[#222831] bg-[#13171D] p-1 shadow-xl">
                    {PAIRS.map((p) => (
                      <button
                        key={p}
                        type="button"
                        onClick={() => handlePairChange(p)}
                        className={`flex w-full cursor-pointer items-center justify-between rounded px-2.5 py-1.5 text-xs font-semibold ${
                          pair === p
                            ? "bg-[#1A1F27] text-[#38BDF8]"
                            : "text-[#EAECEF] hover:bg-[#1A1F27]/60"
                        }`}
                      >
                        <span>{p}</span>
                        <span className="text-[10px] text-[#848E9C]">Spot CLOB</span>
                      </button>
                    ))}
                  </div>
                )}
              </div>

              {/* Large Directional Live Price */}
              <div className="flex items-center gap-1.5">
                <span
                  className={`inline-flex items-center gap-1 text-lg font-bold tracking-tight ${
                    isPositive ? "text-[#0ECB81]" : "text-[#F6465D]"
                  }`}
                >
                  {formatPrice(market.markPrice, 2)}
                  {isPositive ? (
                    <ArrowUp className="h-4 w-4" />
                  ) : (
                    <ArrowDown className="h-4 w-4" />
                  )}
                </span>
              </div>

              {/* Market Ticker Metrics */}
              <div className="hidden items-center gap-4 text-[11px] lg:flex">
                <div>
                  <span className="block text-[10px] text-[#848E9C]">24h Change</span>
                  <strong className={isPositive ? "text-[#0ECB81]" : "text-[#F6465D]"}>
                    {formatBps(market.change24hBps)}
                  </strong>
                </div>
                <div>
                  <span className="block text-[10px] text-[#848E9C]">24h High</span>
                  <strong className="font-medium text-[#EAECEF]">
                    ${formatPrice(market.high24h, 2)}
                  </strong>
                </div>
                <div>
                  <span className="block text-[10px] text-[#848E9C]">24h Low</span>
                  <strong className="font-medium text-[#EAECEF]">
                    ${formatPrice(market.low24h, 2)}
                  </strong>
                </div>
                <div>
                  <span className="block text-[10px] text-[#848E9C]">24h Turnover</span>
                  <strong className="font-medium text-[#EAECEF]">{compactTurnover}</strong>
                </div>
                <div>
                  <span className="block text-[10px] text-[#848E9C]">Oracle Mark</span>
                  <strong className="font-medium text-[#38BDF8]">
                    ${formatPrice(market.markPrice, 2)}
                  </strong>
                </div>
                <div>
                  <span className="block text-[10px] text-[#848E9C]">Index</span>
                  <strong className="font-medium text-[#EAECEF]">
                    ${formatPrice(market.indexPrice, 2)}
                  </strong>
                </div>
              </div>
            </div>

            <div className="flex shrink-0 items-center gap-2 text-[11px] tabular-nums">
              {isBreakerTripped ? (
                <span className="inline-flex items-center gap-1 rounded border border-[#F6465D]/40 bg-[#F6465D]/15 px-2 py-0.5 font-semibold text-[#F6465D]">
                  <ShieldAlert className="h-3 w-3" />
                  Circuit Breaker Halted (±10.0%)
                </span>
              ) : (
                <span className="inline-flex items-center gap-1 rounded border border-[#0ECB81]/30 bg-[#0ECB81]/10 px-2 py-0.5 font-medium text-[#0ECB81]">
                  Circuit Breaker Collar ±10.0%
                </span>
              )}
            </div>
          </div>

          {/* Flush 1px Hairline Exchange Grid */}
          <main className="grid min-h-0 flex-1 grid-cols-12 gap-px bg-[#222831]">
            {/* Left/Center (7/12): Chart (64%) + Blotter (36%) */}
            <div className="col-span-7 flex min-h-0 flex-col gap-px">
              <div className="min-h-0 flex-[64]">
                <CandlestickChart
                  pair={pair}
                  timeframe={timeframe}
                  onTimeframeChange={setTimeframe}
                  market={market}
                  openOrders={openOrders}
                />
              </div>
              <div className="min-h-0 flex-[36]">
                <OpenOrdersTable
                  orders={openOrders}
                  fills={recentFills}
                  balances={balances}
                  telemetry={telemetry}
                  onRefresh={() => {
                    void refreshAccountState();
                  }}
                  onInspectOrder={handleInspectOpenOrder}
                  onInspectFill={handleInspectFill}
                />
              </div>
            </div>

            {/* Middle Column (2/12): OrderBook (64%) + TradeTape (36%) */}
            <div className="col-span-2 flex min-h-0 flex-col gap-px">
              <div className="min-h-0 flex-[64]">
                <OrderBook market={market} onSelectPrice={handleSelectBookPrice} />
              </div>
              <div className="min-h-0 flex-[36]">
                <TradeTape market={market} />
              </div>
            </div>

            {/* Right Column (3/12): Full-Height Dedicated Order Entry Ticket */}
            <div className="col-span-3 min-h-0">
              <OrderEntryForm
                key={pair}
                pair={pair}
                markPrice={market.markPrice}
                circuitBreakerTripped={isBreakerTripped}
                balances={balances}
                orderType={orderType}
                onOrderTypeChange={setOrderType}
                price={limitPrice}
                onPriceChange={setLimitPrice}
                pricePulseTick={pricePulseTick}
                onOrderSubmitted={handleOrderSubmitted}
                onInspectLifecycle={() => setLifecycleOpen(true)}
              />
            </div>
          </main>
        </div>
      )}

      {activeWorkspace === "custody" && (
        <main className="min-h-0 flex-1">
          <WalletModal
            open
            mode="workspace"
            onClose={() => setActiveWorkspace("trade")}
            balances={balances}
            transfers={telemetry.transfers}
            onRefresh={() => {
              void refreshAccountState();
            }}
          />
        </main>
      )}

      {activeWorkspace === "chaos" && (
        <main className="min-h-0 flex-1">
          <ChaosControlPanel
            pair={pair}
            telemetry={telemetry}
            onAfterDrill={() => {
              void refreshAccountState();
            }}
          />
        </main>
      )}

      {activeWorkspace === "por" && (
        <main className="min-h-0 flex-1">
          <ProofOfReservesModal
            open
            mode="workspace"
            telemetry={telemetry}
            onClose={() => setActiveWorkspace("trade")}
          />
        </main>
      )}

      {/* Bottom Telemetry Bar (28px) */}
      <TelemetryStrip telemetry={telemetry} />

      {/* Quick Deposit / Withdraw Modal Overlay */}
      <WalletModal
        open={quickWalletOpen}
        mode="modal"
        onClose={() => setQuickWalletOpen(false)}
        balances={balances}
        transfers={telemetry.transfers}
        onRefresh={() => {
          void refreshAccountState();
        }}
      />

      {/* Order Lifecycle Inspector Drawer */}
      <OrderLifecycleDrawer
        open={lifecycleOpen}
        order={inspectedOrder}
        onClose={() => setLifecycleOpen(false)}
      />

      {/* Live 6-Invariant Inspector Modal */}
      <InvariantsModal
        open={invariantsOpen}
        telemetry={telemetry}
        onClose={() => setInvariantsOpen(false)}
      />

      {/* Command Palette (Cmd/Ctrl+K) */}
      <CommandPalette
        open={cmdkOpen}
        commands={commands}
        onClose={() => setCmdkOpen(false)}
      />

      {/* 6-Step Platform Architecture Guide */}
      <GuidedTourBanner
        open={tourOpen}
        stepIndex={tourStep}
        onSelectStep={handleTourStepSelect}
        onClose={handleTourClose}
      />
    </div>
  );
}

export default function HomePage() {
  return (
    <WagmiProvider config={wagmiConfig}>
      <QueryClientProvider client={queryClient}>
        <TradingDeskInner />
      </QueryClientProvider>
    </WagmiProvider>
  );
}
