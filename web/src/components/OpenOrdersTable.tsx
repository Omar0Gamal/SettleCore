"use client";

import { useState } from "react";
import { AlertOctagon, CheckCircle2, Clock, Loader2, Trash2, X } from "lucide-react";
import {
  cancelAllOrders,
  cancelOrder,
  type AccountBalance,
  type CustodyTransfer,
  type OpenOrder,
  type RecentFill,
} from "@/lib/api";
import {
  formatLatencyUs,
  formatPrice,
  formatQuantity,
  formatTimestamp,
  truncateHash,
} from "@/lib/format";
import { playClickSfx } from "@/lib/sound";
import type { TelemetryState } from "@/hooks/useTelemetryStream";

interface OpenOrdersTableProps {
  orders: OpenOrder[];
  fills: RecentFill[];
  balances: AccountBalance[];
  telemetry: TelemetryState;
  onRefresh: () => void;
  onInspectOrder?: (order: OpenOrder) => void;
  onInspectFill?: (fill: RecentFill) => void;
}

type ActiveTab = "orders" | "fills" | "custody" | "balances";

export function OpenOrdersTable({
  orders,
  fills,
  balances,
  telemetry,
  onRefresh,
  onInspectOrder,
  onInspectFill,
}: OpenOrdersTableProps) {
  const [tab, setTab] = useState<ActiveTab>("orders");
  const [cancelingId, setCancelingId] = useState<string | null>(null);
  const [cancelingAll, setCancelingAll] = useState<boolean>(false);

  const handleCancel = async (order: OpenOrder) => {
    playClickSfx();
    setCancelingId(order.order_id);
    try {
      await cancelOrder(order.order_id, order.market_id);
      onRefresh();
    } finally {
      setCancelingId(null);
    }
  };

  const handleCancelAll = async () => {
    playClickSfx();
    setCancelingAll(true);
    try {
      await cancelAllOrders();
      onRefresh();
    } finally {
      setCancelingAll(false);
    }
  };

  const latestDeposit = telemetry.transfers.find((t) => t.direction === "DEPOSIT");
  const reorgStartBlock = latestDeposit ? Math.max(1, latestDeposit.block_number - 2) : 101;
  const reorgEndBlock = latestDeposit ? latestDeposit.block_number : 103;
  const usdcBal = balances.find((b) => b.asset === "USDC");
  const availMarginUsd = usdcBal ? formatPrice(usdcBal.available, 2) : "48,210.50";
  const lockedMarginUsd = usdcBal ? formatPrice(usdcBal.locked, 2) : "4,688.25";

  return (
    <section className="flex h-full min-h-0 flex-col overflow-hidden bg-[#0B0E11]">
      <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-[#222831] bg-[#13171D] px-3 py-1">
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={() => {
              playClickSfx();
              setTab("orders");
            }}
            className={`btn-tactile cursor-pointer rounded px-2.5 py-1 text-xs font-medium tracking-tight tabular-nums ${
              tab === "orders"
                ? "bg-[#1A1F27] text-[#EAECEF]"
                : "text-[#848E9C] hover:text-[#EAECEF]"
            }`}
          >
            Open Orders ({orders.length})
          </button>
          <button
            type="button"
            onClick={() => {
              playClickSfx();
              setTab("fills");
            }}
            className={`btn-tactile cursor-pointer rounded px-2.5 py-1 text-xs font-medium tracking-tight tabular-nums ${
              tab === "fills"
                ? "bg-[#1A1F27] text-[#EAECEF]"
                : "text-[#848E9C] hover:text-[#EAECEF]"
            }`}
          >
            Order &amp; Trade History ({fills.length})
          </button>
          <button
            type="button"
            onClick={() => {
              playClickSfx();
              setTab("custody");
            }}
            className={`btn-tactile cursor-pointer rounded px-2.5 py-1 text-xs font-medium tracking-tight tabular-nums ${
              tab === "custody"
                ? "bg-[#1A1F27] text-[#EAECEF]"
                : "text-[#848E9C] hover:text-[#EAECEF]"
            }`}
          >
            On-Chain Custody &amp; Finality ({telemetry.transfers.length})
          </button>
          <button
            type="button"
            onClick={() => {
              playClickSfx();
              setTab("balances");
            }}
            className={`btn-tactile cursor-pointer rounded px-2.5 py-1 text-xs font-medium tracking-tight tabular-nums ${
              tab === "balances"
                ? "bg-[#1A1F27] text-[#EAECEF]"
                : "text-[#848E9C] hover:text-[#EAECEF]"
            }`}
          >
            Assets / Balances ({balances.length})
          </button>
        </div>

        <div className="flex items-center gap-2">
          {latestDeposit && (
            <div className="flex items-center gap-2 rounded border border-[#222831] bg-[#0B0E11] px-2 py-0.5 text-[11px] tabular-nums">
              <span className="text-[#848E9C]">
                Deposit #{latestDeposit.block_number} ({formatQuantity(latestDeposit.amount, 2)}{" "}
                {latestDeposit.asset})
              </span>
              {latestDeposit.status === "ORPHANED" ? (
                <span
                  data-testid="deposit-status-badge"
                  className="inline-flex items-center gap-1 rounded border border-[#F6465D]/45 bg-[#F6465D]/15 px-1.5 py-0.5 text-[10px] font-semibold text-[#F6465D]"
                >
                  <AlertOctagon className="h-3 w-3" />
                  ORPHANED / ROLLED BACK (Reorg #{reorgStartBlock}..{reorgEndBlock})
                </span>
              ) : latestDeposit.status === "PENDING" ? (
                <span
                  data-testid="deposit-status-badge"
                  className="inline-flex items-center gap-1 rounded border border-[#38BDF8]/30 bg-[#38BDF8]/10 px-1.5 py-0.5 text-[10px] font-medium text-[#38BDF8]"
                >
                  <Clock className="h-3 w-3" />
                  PENDING ({latestDeposit.confirmations}/{latestDeposit.required_confirmations}{" "}
                  Blocks)
                </span>
              ) : (
                <span
                  data-testid="deposit-status-badge"
                  className="inline-flex items-center gap-1 rounded border border-[#0ECB81]/35 bg-[#0ECB81]/15 px-1.5 py-0.5 text-[10px] font-medium text-[#0ECB81]"
                >
                  <CheckCircle2 className="h-3 w-3" />
                  CONFIRMED ({latestDeposit.confirmations}/{latestDeposit.required_confirmations}{" "}
                  Blocks)
                </span>
              )}
            </div>
          )}

          {tab === "orders" && orders.length > 0 && (
            <button
              type="button"
              disabled={cancelingAll}
              onClick={handleCancelAll}
              className="btn-tactile inline-flex cursor-pointer items-center gap-1 rounded border border-[#F6465D]/35 bg-[#F6465D]/10 px-2 py-1 text-[11px] font-medium tracking-tight text-[#F6465D] hover:bg-[#F6465D]/20 disabled:opacity-50"
            >
              {cancelingAll ? (
                <Loader2 className="h-3 w-3 animate-spin" />
              ) : (
                <Trash2 className="h-3 w-3" />
              )}
              Cancel All
            </button>
          )}
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto overflow-x-auto bg-[#0B0E11]">
        {tab === "orders" && (
          <table className="w-full text-left text-xs tabular-nums">
            <thead className="sticky top-0 z-10 bg-[#0B0E11]">
              <tr className="border-b border-[#222831] text-[10px] font-medium uppercase tracking-wider text-[#848E9C]">
                <th className="px-3.5 py-2 font-medium">Order ID</th>
                <th className="px-3 py-2 font-medium">Pair</th>
                <th className="px-3 py-2 font-medium">Side</th>
                <th className="px-3 py-2 font-medium">Type / TIF</th>
                <th className="px-3 py-2 text-right font-medium">Price</th>
                <th className="px-3 py-2 text-right font-medium">Filled / Qty</th>
                <th className="px-3 py-2 text-right font-medium">WAL Seq</th>
                <th className="px-3.5 py-2 text-right font-medium">Action</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#222831]/60">
              {orders.length === 0 ? (
                <tr>
                  <td colSpan={8} className="px-3.5 py-6 text-center text-[#848E9C]">
                    No active resting orders in CLOB.
                  </td>
                </tr>
              ) : (
                orders.map((order) => (
                  <tr
                    key={order.order_id}
                    onClick={() => {
                      playClickSfx();
                      onInspectOrder?.(order);
                    }}
                    title="Click to inspect 2-phase order lifecycle"
                    className="cursor-pointer transition-colors hover:bg-[#13171D]/80"
                  >
                    <td className="px-3.5 py-1.5 font-medium text-[#38BDF8]">#{order.order_id}</td>
                    <td className="px-3 py-1.5 font-medium text-[#EAECEF]">{order.market_id}</td>
                    <td className="px-3 py-1.5">
                      <span
                        className={`rounded px-1.5 py-0.5 text-[10px] font-semibold ${
                          order.side === "BUY"
                            ? "bg-[#0ECB81]/15 text-[#0ECB81]"
                            : "bg-[#F6465D]/15 text-[#F6465D]"
                        }`}
                      >
                        {order.side}
                      </span>
                    </td>
                    <td className="px-3 py-1.5 text-[#848E9C]">
                      {order.order_type} · {order.time_in_force}
                    </td>
                    <td className="px-3 py-1.5 text-right font-medium text-[#EAECEF]">
                      ${formatPrice(order.price, 2)}
                    </td>
                    <td className="px-3 py-1.5 text-right text-[#EAECEF]">
                      {formatQuantity(order.filled_quantity, 2)} /{" "}
                      {formatQuantity(order.quantity, 2)}
                    </td>
                    <td className="px-3 py-1.5 text-right font-medium text-[#38BDF8]">
                      #{order.wal_seq.toLocaleString()}
                    </td>
                    <td className="px-3.5 py-1.5 text-right">
                      <button
                        type="button"
                        disabled={cancelingId === order.order_id}
                        onClick={(e) => {
                          e.stopPropagation();
                          void handleCancel(order);
                        }}
                        className="btn-tactile inline-flex cursor-pointer items-center gap-1 rounded border border-[#222831] bg-[#1A1F27] px-2 py-0.5 text-[11px] font-medium text-[#EAECEF] hover:border-[#F6465D]/50 hover:text-[#F6465D] disabled:opacity-50"
                      >
                        {cancelingId === order.order_id ? (
                          <Loader2 className="h-3 w-3 animate-spin" />
                        ) : (
                          <X className="h-3 w-3" />
                        )}
                        Cancel
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        )}

        {tab === "fills" && (
          <table className="w-full text-left text-xs tabular-nums">
            <thead className="sticky top-0 z-10 bg-[#0B0E11]">
              <tr className="border-b border-[#222831] text-[10px] font-medium uppercase tracking-wider text-[#848E9C]">
                <th className="px-3.5 py-2 font-medium">Trade ID</th>
                <th className="px-3 py-2 font-medium">Pair</th>
                <th className="px-3 py-2 font-medium">Side</th>
                <th className="px-3 py-2 text-right font-medium">Exec Price</th>
                <th className="px-3 py-2 text-right font-medium">Qty</th>
                <th className="px-3 py-2 text-right font-medium">Notional</th>
                <th className="px-3 py-2 text-right font-medium">Fee (USDC)</th>
                <th className="px-3 py-2 text-right font-medium">Match</th>
                <th className="px-3 py-2 text-right font-medium">WAL Seq</th>
                <th className="px-3.5 py-2 text-right font-medium">UTC</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#222831]/60">
              {fills.map((fill) => (
                <tr
                  key={`${fill.trade_id}-${fill.wal_seq}`}
                  onClick={() => {
                    playClickSfx();
                    onInspectFill?.(fill);
                  }}
                  title="Click to inspect 2-phase order lifecycle"
                  className="cursor-pointer transition-colors hover:bg-[#13171D]/80"
                >
                  <td className="px-3.5 py-1.5 text-[#EAECEF]">#{fill.trade_id}</td>
                  <td className="px-3 py-1.5 font-medium text-[#EAECEF]">{fill.market_id}</td>
                  <td className="px-3 py-1.5">
                    <span
                      className={`rounded px-1.5 py-0.5 text-[10px] font-semibold ${
                        fill.side === "BUY"
                          ? "bg-[#0ECB81]/15 text-[#0ECB81]"
                          : "bg-[#F6465D]/15 text-[#F6465D]"
                      }`}
                    >
                      {fill.side}
                    </span>
                  </td>
                  <td className="px-3 py-1.5 text-right font-medium text-[#EAECEF]">
                    ${formatPrice(fill.price, 2)}
                  </td>
                  <td className="px-3 py-1.5 text-right text-[#EAECEF]">
                    {formatQuantity(fill.quantity, 4)}
                  </td>
                  <td className="px-3 py-1.5 text-right text-[#EAECEF]">
                    ${formatPrice(fill.quote_notional, 2)}
                  </td>
                  <td className="px-3 py-1.5 text-right text-[#848E9C]">
                    ${formatPrice(fill.fee_usdc, 2)}
                  </td>
                  <td className="px-3 py-1.5 text-right font-medium text-[#38BDF8]">
                    {formatLatencyUs(fill.match_latency_ns, "ns")}
                  </td>
                  <td className="px-3 py-1.5 text-right text-[#848E9C]">
                    #{fill.wal_seq.toLocaleString()}
                  </td>
                  <td className="px-3.5 py-1.5 text-right text-[#848E9C]">
                    {formatTimestamp(fill.executed_at).slice(0, 8)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}

        {tab === "custody" && (
          <table className="w-full text-left text-xs tabular-nums">
            <thead className="sticky top-0 z-10 bg-[#0B0E11]">
              <tr className="border-b border-[#222831] text-[10px] font-medium uppercase tracking-wider text-[#848E9C]">
                <th className="px-3.5 py-2 font-medium">Direction</th>
                <th className="px-3 py-2 font-medium">Asset</th>
                <th className="px-3 py-2 text-right font-medium">Amount</th>
                <th className="px-3 py-2 font-medium">Tx Hash</th>
                <th className="px-3 py-2 text-right font-medium">Block</th>
                <th className="px-3 py-2 font-medium">Finality / Status</th>
                <th className="px-3.5 py-2 text-right font-medium">Time (UTC)</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#222831]/60">
              {telemetry.transfers.map((tx: CustodyTransfer) => (
                <tr key={tx.id} className="hover:bg-[#13171D]/70">
                  <td className="px-3.5 py-1.5">
                    <span
                      className={`rounded px-1.5 py-0.5 text-[10px] font-semibold ${
                        tx.direction === "DEPOSIT"
                          ? "bg-[#0ECB81]/15 text-[#0ECB81]"
                          : "bg-[#38BDF8]/15 text-[#38BDF8]"
                      }`}
                    >
                      {tx.direction}
                    </span>
                  </td>
                  <td className="px-3 py-1.5 font-semibold text-[#EAECEF]">{tx.asset}</td>
                  <td className="px-3 py-1.5 text-right text-[#EAECEF]">
                    {formatQuantity(tx.amount, 4, true)}
                  </td>
                  <td className="px-3 py-1.5 text-[#848E9C]">{truncateHash(tx.tx_hash)}</td>
                  <td className="px-3 py-1.5 text-right text-[#EAECEF]">#{tx.block_number}</td>
                  <td className="px-3 py-1.5">
                    {tx.status === "ORPHANED" ? (
                      <span className="inline-flex items-center gap-1 rounded border border-[#F6465D]/40 bg-[#F6465D]/15 px-2 py-0.5 text-[10px] font-semibold text-[#F6465D]">
                        ORPHANED / ROLLED BACK (Reorg #{Math.max(1, tx.block_number - 2)}..
                        {tx.block_number})
                      </span>
                    ) : tx.status === "CONFIRMED" ? (
                      <span className="inline-flex items-center gap-1 rounded border border-[#0ECB81]/30 bg-[#0ECB81]/15 px-2 py-0.5 text-[10px] font-medium text-[#0ECB81]">
                        CONFIRMED ({tx.confirmations}/{tx.required_confirmations} Blocks)
                        {tx.gas_bump_count ? ` · ${tx.gas_bump_count} bump` : ""}
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1 rounded border border-[#38BDF8]/30 bg-[#38BDF8]/15 px-2 py-0.5 text-[10px] font-medium text-[#38BDF8]">
                        {tx.status} ({tx.signing_tier ?? "PENDING"})
                      </span>
                    )}
                  </td>
                  <td className="px-3.5 py-1.5 text-right text-[#848E9C]">
                    {formatTimestamp(tx.timestamp).slice(0, 8)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}

        {tab === "balances" && (
          <table className="w-full text-left text-xs tabular-nums">
            <thead className="sticky top-0 z-10 bg-[#0B0E11]">
              <tr className="border-b border-[#222831] text-[10px] font-medium uppercase tracking-wider text-[#848E9C]">
                <th className="px-3.5 py-2 font-medium">Asset</th>
                <th className="px-3 py-2 text-right font-medium">Available</th>
                <th className="px-3 py-2 text-right font-medium">Locked (CLOB / Outbox)</th>
                <th className="px-3.5 py-2 text-right font-medium">Custody Standard</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#222831]/60">
              {balances.map((bal) => (
                <tr key={bal.asset} className="hover:bg-[#13171D]/70">
                  <td className="px-3.5 py-1.5 font-semibold text-[#EAECEF]">{bal.asset}</td>
                  <td className="px-3 py-1.5 text-right font-medium text-[#0ECB81]">
                    {formatQuantity(bal.available, 4, true)}
                  </td>
                  <td className="px-3 py-1.5 text-right text-[#EAECEF]">
                    {formatQuantity(bal.locked, 4, true)}
                  </td>
                  <td className="px-3.5 py-1.5 text-right text-[#848E9C]">
                    {bal.asset === "ETH"
                      ? "Native ETH"
                      : bal.asset === "USDT"
                        ? "SafeERC20 (No-Bool)"
                        : "ERC-20"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* Persistent Blotter Summary Footer Bar */}
      <div className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-[#222831] bg-[#13171D] px-3.5 py-1 text-[11px] tabular-nums text-[#848E9C]">
        <div className="flex flex-wrap items-center gap-3">
          <span>
            Resting Orders: <strong className="font-semibold text-[#EAECEF]">{orders.length}</strong>
          </span>
          <span className="text-[#474D57]">|</span>
          <span>
            Locked Margin:{" "}
            <strong className="font-semibold text-[#EAECEF]">${lockedMarginUsd} USDC</strong>
          </span>
          <span className="text-[#474D57]">|</span>
          <span>
            Available Margin:{" "}
            <strong className="font-semibold text-[#0ECB81]">${availMarginUsd} USDC</strong>
          </span>
        </div>

        <div className="flex items-center gap-3">
          <span>
            Reserve Root: <strong className="font-medium text-[#38BDF8]">0x8f4e...abcd</strong>
          </span>
          <span className="text-[#474D57]">|</span>
          <span>
            WAL Seq:{" "}
            <strong className="font-semibold text-[#EAECEF]">
              #{telemetry.wal_seq.toLocaleString()}
            </strong>
          </span>
        </div>
      </div>
    </section>
  );
}
