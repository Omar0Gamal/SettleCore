"use client";

import { useMemo, useState } from "react";
import {
  ArrowDownToLine,
  ArrowUpFromLine,
  CheckCircle2,
  Coins,
  KeyRound,
  Loader2,
  Shield,
  ShieldCheck,
  Wallet,
  X,
} from "lucide-react";
import { parseUnits } from "viem";
import {
  useChainId,
  useConnect,
  useConnection,
  useConnectors,
  useDisconnect,
  useSwitchChain,
  useWriteContract,
} from "wagmi";
import {
  requestWithdrawal,
  type AccountBalance,
  type CustodyTransfer,
  type WithdrawalResponse,
} from "@/lib/api";
import {
  addDecimalStrings,
  compareDecimalStrings,
  formatQuantity,
  formatTimestamp,
  isValidPositiveDecimal,
  truncateHash,
} from "@/lib/format";
import { playClickSfx, playVerifiedSfx } from "@/lib/sound";
import {
  ASSET_SPECS,
  ERC20_ABI,
  ESCROW_VAULT_ABI,
  ESCROW_VAULT_ADDRESS,
  anvilChain,
  type CustodyAsset,
} from "@/lib/wagmi";
import { sepolia } from "wagmi/chains";

interface WalletModalProps {
  open: boolean;
  mode?: "workspace" | "modal";
  onClose: () => void;
  balances: AccountBalance[];
  transfers?: CustodyTransfer[];
  onRefresh: () => void;
}

export function WalletModal({
  open,
  mode: displayMode = "modal",
  onClose,
  balances,
  transfers = [],
  onRefresh,
}: WalletModalProps) {
  const connection = useConnection();
  const connectors = useConnectors();
  const connect = useConnect();
  const disconnect = useDisconnect();
  const chainId = useChainId();
  const switchChain = useSwitchChain();
  const writeContract = useWriteContract();

  const [actionTab, setActionTab] = useState<"deposit" | "withdraw">("deposit");
  const [asset, setAsset] = useState<CustodyAsset>("ETH");
  const [amount, setAmount] = useState<string>("2.50");
  const [recipient, setRecipient] = useState<string>(
    "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
  );
  const [busy, setBusy] = useState<boolean>(false);
  const [statusMsg, setStatusMsg] = useState<string | null>(null);
  const [wdReceipt, setWdReceipt] = useState<WithdrawalResponse | null>(null);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const spec = ASSET_SPECS[asset];
  const isColdTier = useMemo(() => {
    if (!isValidPositiveDecimal(amount, 8)) return false;
    return compareDecimalStrings(amount, spec.hotPerTxCap) > 0;
  }, [amount, spec.hotPerTxCap]);

  if (!open && displayMode === "modal") return null;

  const activeAddress = connection.address ?? "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266";
  const isConnected = connection.status === "connected";

  const handleDeposit = async () => {
    playClickSfx();
    setErrorMsg(null);
    setStatusMsg(null);
    if (!isValidPositiveDecimal(amount, spec.decimals)) {
      setErrorMsg(`Enter a valid positive ${asset} amount (max ${spec.decimals} decimals).`);
      return;
    }

    setBusy(true);
    try {
      const units = parseUnits(amount, spec.decimals);
      if (isConnected) {
        if (spec.isNative) {
          const txHash = await writeContract.mutateAsync({
            address: ESCROW_VAULT_ADDRESS,
            abi: ESCROW_VAULT_ABI,
            functionName: "depositETH",
            value: units,
          });
          setStatusMsg(`EscrowVault.depositETH() broadcast: ${txHash}`);
        } else {
          await writeContract.mutateAsync({
            address: spec.address,
            abi: ERC20_ABI,
            functionName: "approve",
            args: [ESCROW_VAULT_ADDRESS, units],
          });
          const txHash = await writeContract.mutateAsync({
            address: ESCROW_VAULT_ADDRESS,
            abi: ESCROW_VAULT_ABI,
            functionName: "depositERC20",
            args: [spec.address, units],
          });
          setStatusMsg(`EscrowVault.depositERC20(${asset}) broadcast: ${txHash}`);
        }
      } else {
        setStatusMsg(
          `Simulated ${spec.isNative ? "depositETH" : "approve + depositERC20"}(${amount} ${asset}) -> EscrowVault (${ESCROW_VAULT_ADDRESS.slice(0, 10)}...) queued for N_conf=3 finality.`
        );
      }
      playVerifiedSfx();
      onRefresh();
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : "On-chain deposit failed");
    } finally {
      setBusy(false);
    }
  };

  const handleMintMock = async () => {
    playClickSfx();
    setErrorMsg(null);
    setStatusMsg(null);
    if (spec.isNative) return;
    setBusy(true);
    try {
      const units = parseUnits("1000", spec.decimals);
      if (isConnected) {
        const txHash = await writeContract.mutateAsync({
          address: spec.address,
          abi: ERC20_ABI,
          functionName: "mint",
          args: [activeAddress as `0x${string}`, units],
        });
        setStatusMsg(`Minted 1,000 Mock${asset} to wallet: ${txHash}`);
      } else {
        setStatusMsg(`Simulated faucet mint: 1,000 Mock${asset} -> ${activeAddress.slice(0, 10)}...`);
      }
      playVerifiedSfx();
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : "Mock mint failed");
    } finally {
      setBusy(false);
    }
  };

  const handleWithdraw = async () => {
    playClickSfx();
    setErrorMsg(null);
    setStatusMsg(null);
    setWdReceipt(null);
    if (!isValidPositiveDecimal(amount, spec.decimals)) {
      setErrorMsg(`Enter a valid positive ${asset} withdrawal amount.`);
      return;
    }

    setBusy(true);
    try {
      const res = await requestWithdrawal({
        asset,
        amount,
        recipient_address: recipient,
      });
      setWdReceipt(res);
      playVerifiedSfx();
      onRefresh();
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : "Withdrawal request failed");
    } finally {
      setBusy(false);
    }
  };

  const availableBal =
    balances.find((b) => b.asset === asset)?.available ?? "0.00000000";

  const walletHeaderBar = (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-[#222831] bg-[#13171D] p-3">
      <div>
        <div className="flex items-center gap-2">
          <span
            className={`h-2 w-2 rounded-full ${
              isConnected ? "bg-[#0ECB81]" : "bg-[#38BDF8]"
            }`}
          />
          <span className="text-xs font-semibold tabular-nums text-[#EAECEF]">
            {isConnected
              ? `${activeAddress.slice(0, 6)}...${activeAddress.slice(-4)}`
              : "Anvil Dev Account (0xf39F...2266)"}
          </span>
        </div>
        <span className="mt-0.5 block text-[10px] tabular-nums text-[#848E9C]">
          Chain {chainId} · EscrowVault {ESCROW_VAULT_ADDRESS.slice(0, 12)}...
        </span>
      </div>

      <div className="flex items-center gap-1.5">
        <button
          type="button"
          onClick={() => {
            playClickSfx();
            switchChain.mutate({ chainId: anvilChain.id });
          }}
          className={`btn-tactile cursor-pointer rounded border px-2.5 py-1 text-[10px] font-medium tabular-nums ${
            chainId === anvilChain.id
              ? "border-[#0ECB81]/40 bg-[#0ECB81]/15 text-[#0ECB81]"
              : "border-[#222831] bg-[#1A1F27] text-[#848E9C] hover:text-[#EAECEF]"
          }`}
        >
          Anvil 31337
        </button>
        <button
          type="button"
          onClick={() => {
            playClickSfx();
            switchChain.mutate({ chainId: sepolia.id });
          }}
          className={`btn-tactile cursor-pointer rounded border px-2.5 py-1 text-[10px] font-medium ${
            chainId === sepolia.id
              ? "border-[#38BDF8]/40 bg-[#38BDF8]/15 text-[#38BDF8]"
              : "border-[#222831] bg-[#1A1F27] text-[#848E9C] hover:text-[#EAECEF]"
          }`}
        >
          Sepolia
        </button>

        {isConnected ? (
          <button
            type="button"
            onClick={() => {
              playClickSfx();
              disconnect.mutate();
            }}
            className="btn-tactile cursor-pointer rounded border border-[#222831] bg-[#1A1F27] px-2.5 py-1 text-[10px] font-medium text-[#EAECEF] hover:bg-[#222831]"
          >
            Disconnect
          </button>
        ) : (
          connectors[0] && (
            <button
              type="button"
              onClick={() => {
                playClickSfx();
                connect.mutate({ connector: connectors[0]! });
              }}
              className="btn-tactile cursor-pointer rounded bg-[#38BDF8]/15 px-2.5 py-1 text-[10px] font-semibold text-[#38BDF8] hover:bg-[#38BDF8]/25"
            >
              Connect Injected
            </button>
          )
        )}
      </div>
    </div>
  );

  const depositWithdrawConsole = (
    <div className="space-y-4">
      {walletHeaderBar}

      {/* Deposit / Withdraw Switch */}
      <div className="grid grid-cols-2 gap-1 rounded-md border border-[#222831] bg-[#13171D] p-1">
        <button
          type="button"
          onClick={() => {
            playClickSfx();
            setActionTab("deposit");
            setErrorMsg(null);
            setStatusMsg(null);
          }}
          className={`btn-tactile flex cursor-pointer items-center justify-center gap-1.5 rounded py-1.5 text-xs font-semibold ${
            actionTab === "deposit"
              ? "bg-[#1A1F27] text-[#EAECEF]"
              : "text-[#848E9C] hover:text-[#EAECEF]"
          }`}
        >
          <ArrowDownToLine className="h-3.5 w-3.5 text-[#0ECB81]" />
          Deposit to Vault
        </button>
        <button
          type="button"
          onClick={() => {
            playClickSfx();
            setActionTab("withdraw");
            setErrorMsg(null);
            setStatusMsg(null);
          }}
          className={`btn-tactile flex cursor-pointer items-center justify-center gap-1.5 rounded py-1.5 text-xs font-semibold ${
            actionTab === "withdraw"
              ? "bg-[#1A1F27] text-[#EAECEF]"
              : "text-[#848E9C] hover:text-[#EAECEF]"
          }`}
        >
          <ArrowUpFromLine className="h-3.5 w-3.5 text-[#38BDF8]" />
          EIP-712 Withdrawal
        </button>
      </div>

      {/* Asset Selector */}
      <div className="grid grid-cols-4 gap-1.5">
        {(Object.keys(ASSET_SPECS) as CustodyAsset[]).map((sym) => (
          <button
            key={sym}
            type="button"
            onClick={() => {
              playClickSfx();
              setAsset(sym);
            }}
            className={`btn-tactile cursor-pointer rounded-md border px-2.5 py-2 text-left ${
              asset === sym
                ? "border-[#38BDF8]/50 bg-[#38BDF8]/10 text-[#EAECEF]"
                : "border-[#222831] bg-[#13171D] text-[#848E9C] hover:text-[#EAECEF]"
            }`}
          >
            <div className="text-xs font-semibold">{sym}</div>
            <div className="text-[10px] text-[#848E9C]">
              {ASSET_SPECS[sym].isNonStandardErc20
                ? "SafeERC20"
                : ASSET_SPECS[sym].isNative
                  ? "Native"
                  : `${ASSET_SPECS[sym].decimals} dec`}
            </div>
          </button>
        ))}
      </div>

      {/* Amount */}
      <div>
        <div className="mb-1 flex items-center justify-between text-[11px] text-[#848E9C]">
          <span>Amount ({asset})</span>
          <span className="tabular-nums">
            Available: {formatQuantity(availableBal, 4, true)} {asset}
          </span>
        </div>
        <input
          type="text"
          inputMode="decimal"
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          className="w-full rounded-md border border-[#222831] bg-[#1A1F27] px-3 py-2 text-xs font-semibold tabular-nums text-[#EAECEF] outline-none focus:border-[#38BDF8]"
        />
      </div>

      {actionTab === "withdraw" && (
        <>
          <div>
            <label className="mb-1 block text-[11px] text-[#848E9C]">
              Recipient EVM Address
            </label>
            <input
              type="text"
              value={recipient}
              onChange={(e) => setRecipient(e.target.value)}
              className="w-full rounded-md border border-[#222831] bg-[#1A1F27] px-3 py-2 text-xs tabular-nums text-[#EAECEF] outline-none focus:border-[#38BDF8]"
            />
          </div>

          <div
            className={`flex items-start gap-2.5 rounded-md border p-3 ${
              isColdTier
                ? "border-amber-400/35 bg-amber-400/[0.08]"
                : "border-[#0ECB81]/35 bg-[#0ECB81]/[0.08]"
            }`}
          >
            {isColdTier ? (
              <KeyRound className="mt-0.5 h-4 w-4 shrink-0 text-amber-400" />
            ) : (
              <Shield className="mt-0.5 h-4 w-4 shrink-0 text-[#0ECB81]" />
            )}
            <div className="text-[11px] tabular-nums">
              <div className="font-semibold text-[#EAECEF]">
                Signing Tier:{" "}
                <span className={isColdTier ? "text-amber-300" : "text-[#0ECB81]"}>
                  {isColdTier ? "COLD_COSIGNED" : "HOT"}
                </span>
              </div>
              <div className="mt-0.5 text-[10px] text-[#848E9C]">
                Hot Per-Tx Cap: {formatQuantity(spec.hotPerTxCap, 2)} {asset} · Rolling 24h
                Cap: {formatQuantity(spec.hotDailyCap, 2)} {asset}.{" "}
                {isColdTier
                  ? "Exceeds Hot Cap: dual HOT_SIGNER + COLD_SIGNER EIP-712 co-signature required."
                  : "Within Hot Cap: immediate Relayer HOT_SIGNER EIP-712 execution."}
              </div>
            </div>
          </div>
        </>
      )}

      {spec.isNonStandardErc20 && (
        <div className="rounded border border-[#38BDF8]/25 bg-[#38BDF8]/[0.06] px-3 py-2 text-[10px] text-sky-300">
          Non-standard ERC-20 (`USDT`): `transfer` / `transferFrom` omit `bool` return value;
          handled safely via OpenZeppelin `SafeERC20`.
        </div>
      )}

      {errorMsg && (
        <div className="rounded border border-[#F6465D]/40 bg-[#F6465D]/10 px-3 py-2 text-[11px] text-[#F6465D]">
          {errorMsg}
        </div>
      )}

      {statusMsg && (
        <div className="flex items-start gap-2 rounded border border-[#0ECB81]/35 bg-[#0ECB81]/10 px-3 py-2 text-[11px] text-[#0ECB81]">
          <CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span className="break-all">{statusMsg}</span>
        </div>
      )}

      {wdReceipt && (
        <div className="rounded border border-[#38BDF8]/35 bg-[#38BDF8]/10 p-3 text-[11px] tabular-nums text-[#EAECEF]">
          <div className="font-semibold text-[#38BDF8]">
            Withdrawal Queued ({wdReceipt.signing_tier})
          </div>
          <div className="mt-1 text-[10px] text-[#848E9C]">
            Voucher Nonce: #{wdReceipt.voucher_nonce} · Raw Units: {wdReceipt.raw_amount_wei}
          </div>
        </div>
      )}

      <div className="flex items-center gap-2 pt-1">
        {actionTab === "deposit" && !spec.isNative && (
          <button
            type="button"
            disabled={busy}
            onClick={handleMintMock}
            className="btn-tactile inline-flex cursor-pointer items-center gap-1.5 rounded-md border border-[#222831] bg-[#1A1F27] px-3 py-2 text-xs font-medium text-[#EAECEF] hover:bg-[#222831] disabled:opacity-50"
          >
            <Coins className="h-3.5 w-3.5 text-amber-400" />
            Mint Mock{asset}
          </button>
        )}

        <button
          type="button"
          disabled={busy}
          onClick={actionTab === "deposit" ? handleDeposit : handleWithdraw}
          className="btn-tactile flex flex-1 cursor-pointer items-center justify-center gap-2 rounded-md bg-[#0ECB81] py-2 text-xs font-bold text-[#0B0E11] hover:bg-[#14D88B] disabled:opacity-50"
        >
          {busy && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          {actionTab === "deposit"
            ? `Deposit ${amount} ${asset} to EscrowVault`
            : `Request ${isColdTier ? "COLD_COSIGNED" : "HOT"} Withdrawal`}
        </button>
      </div>
    </div>
  );

  if (displayMode === "workspace") {
    const rateUsedPctByAsset: Record<CustodyAsset, number> = {
      ETH: 32,
      USDC: 18,
      WBTC: 12,
      USDT: 8,
    };

    return (
      <div className="flex h-full min-h-0 flex-col overflow-y-auto bg-[#0B0E11] p-4">
        <div className="mx-auto flex min-h-0 w-full max-w-[1520px] flex-1 flex-col gap-4">
          {/* Workspace Header */}
          <div className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-b border-[#222831] pb-3">
            <div className="flex items-center gap-3">
              <div className="flex h-9 w-9 items-center justify-center rounded-lg border border-[#38BDF8]/30 bg-[#13171D]">
                <Wallet className="h-4 w-4 text-[#38BDF8]" />
              </div>
              <div>
                <h2 className="font-display text-base font-bold tracking-tight text-[#EAECEF]">
                  Custody &amp; EscrowVault On-Chain Balances
                </h2>
                <p className="text-xs text-[#848E9C]">
                  N_conf = 3 Block Finality · EIP-712 Tiered Hot/Cold Signers · Rolling 24h RateLimiter
                </p>
              </div>
            </div>

            <div className="flex items-center gap-2 text-xs tabular-nums">
              <span className="inline-flex items-center gap-1.5 rounded-md border border-[#0ECB81]/30 bg-[#0ECB81]/10 px-2.5 py-1 font-medium text-[#0ECB81]">
                <ShieldCheck className="h-3.5 w-3.5" />
                EscrowVault Verified ({ESCROW_VAULT_ADDRESS.slice(0, 10)}...)
              </span>
            </div>
          </div>

          {/* 4 Asset Vault Cards */}
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
            {(Object.keys(ASSET_SPECS) as CustodyAsset[]).map((sym) => {
              const s = ASSET_SPECS[sym];
              const bal = balances.find((b) => b.asset === sym);
              const avail = bal?.available ?? "0.00000000";
              const locked = bal?.locked ?? "0.00000000";
              const total = addDecimalStrings(avail, locked, 8);
              const usedPct = rateUsedPctByAsset[sym];

              return (
                <div
                  key={sym}
                  className="rounded-lg border border-[#222831] bg-[#13171D] p-4 tabular-nums"
                >
                  <div className="flex items-center justify-between">
                    <div>
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-bold text-[#EAECEF]">{sym}</span>
                        <span className="rounded bg-[#1A1F27] px-1.5 py-0.5 text-[10px] font-medium text-[#38BDF8]">
                          {s.isNonStandardErc20
                            ? "SafeERC20"
                            : s.isNative
                              ? "Native ETH"
                              : `ERC-20 (${s.decimals}d)`}
                        </span>
                      </div>
                      <p className="mt-0.5 text-[10px] text-[#848E9C]">{s.name}</p>
                    </div>
                    <span className="text-right text-sm font-bold text-[#EAECEF]">
                      {formatQuantity(total, 4, true)}
                    </span>
                  </div>

                  <div className="mt-3 grid grid-cols-2 gap-2 rounded border border-[#222831] bg-[#0B0E11] p-2 text-[11px]">
                    <div>
                      <span className="block text-[10px] text-[#848E9C]">Available</span>
                      <strong className="font-semibold text-[#0ECB81]">
                        {formatQuantity(avail, 4, true)}
                      </strong>
                    </div>
                    <div className="text-right">
                      <span className="block text-[10px] text-[#848E9C]">Locked in CLOB</span>
                      <strong className="font-semibold text-[#EAECEF]">
                        {formatQuantity(locked, 4, true)}
                      </strong>
                    </div>
                  </div>

                  {/* Rolling 24h RateLimiter Gauge */}
                  <div className="mt-3 space-y-1">
                    <div className="flex items-center justify-between text-[10px] text-[#848E9C]">
                      <span>24h Hot RateLimiter</span>
                      <span>
                        {usedPct}% · Cap {formatQuantity(s.hotDailyCap, 1)} {sym}
                      </span>
                    </div>
                    <div className="h-1.5 w-full overflow-hidden rounded-full bg-[#0B0E11]">
                      <div
                        className="h-full rounded-full bg-[#38BDF8]"
                        style={{ width: `${usedPct}%` }}
                      />
                    </div>
                  </div>
                </div>
              );
            })}
          </div>

          {/* Interactive Deposit/Withdraw Console + On-Chain Custody & Finality Table */}
          <div className="grid min-h-0 flex-1 gap-4 lg:grid-cols-12">
            <div className="rounded-lg border border-[#222831] bg-[#0B0E11] p-4 lg:col-span-5">
              <h3 className="mb-3 text-xs font-semibold uppercase tracking-wider text-[#EAECEF]">
                Deposit &amp; Tiered EIP-712 Withdrawal Console
              </h3>
              {depositWithdrawConsole}
            </div>

            <div className="flex min-h-0 flex-col overflow-hidden rounded-lg border border-[#222831] bg-[#0B0E11] lg:col-span-7">
              <div className="flex shrink-0 items-center justify-between border-b border-[#222831] bg-[#13171D] px-4 py-2.5">
                <h3 className="text-xs font-semibold uppercase tracking-wider text-[#EAECEF]">
                  On-Chain Custody &amp; Block Finality Ledger ({transfers.length})
                </h3>
                <span className="text-[10px] font-medium text-[#848E9C]">
                  N_conf = 3 Confirmation Depth
                </span>
              </div>

              <div className="min-h-0 flex-1 overflow-auto">
                <table className="w-full text-left text-xs tabular-nums">
                  <thead>
                    <tr className="border-b border-[#222831] text-[10px] font-medium uppercase tracking-wider text-[#848E9C]">
                      <th className="px-4 py-2.5">Direction</th>
                      <th className="px-3 py-2.5">Asset</th>
                      <th className="px-3 py-2.5 text-right">Amount</th>
                      <th className="px-3 py-2.5">Tx Hash</th>
                      <th className="px-3 py-2.5 text-right">Block</th>
                      <th className="px-3 py-2.5">Finality / Tier</th>
                      <th className="px-4 py-2.5 text-right">UTC</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[#222831]/60">
                    {transfers.map((tx) => (
                      <tr key={tx.id} className="hover:bg-[#13171D]/70">
                        <td className="px-4 py-2.5">
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
                        <td className="px-3 py-2.5 font-semibold text-[#EAECEF]">{tx.asset}</td>
                        <td className="px-3 py-2.5 text-right text-[#EAECEF]">
                          {formatQuantity(tx.amount, 4, true)}
                        </td>
                        <td className="px-3 py-2.5 text-[#848E9C]">{truncateHash(tx.tx_hash)}</td>
                        <td className="px-3 py-2.5 text-right text-[#EAECEF]">#{tx.block_number}</td>
                        <td className="px-3 py-2.5">
                          {tx.status === "ORPHANED" ? (
                            <span className="rounded border border-[#F6465D]/40 bg-[#F6465D]/15 px-2 py-0.5 text-[10px] font-semibold text-[#F6465D]">
                              ORPHANED / ROLLED BACK
                            </span>
                          ) : tx.status === "CONFIRMED" ? (
                            <span className="rounded border border-[#0ECB81]/30 bg-[#0ECB81]/15 px-2 py-0.5 text-[10px] font-medium text-[#0ECB81]">
                              CONFIRMED ({tx.confirmations}/{tx.required_confirmations})
                              {tx.signing_tier ? ` · ${tx.signing_tier}` : ""}
                            </span>
                          ) : (
                            <span className="rounded border border-[#38BDF8]/30 bg-[#38BDF8]/15 px-2 py-0.5 text-[10px] font-medium text-[#38BDF8]">
                              {tx.status} ({tx.signing_tier ?? "PENDING"})
                            </span>
                          )}
                        </td>
                        <td className="px-4 py-2.5 text-right text-[#848E9C]">
                          {formatTimestamp(tx.timestamp).slice(0, 8)}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-4 backdrop-blur-md">
      <div className="w-full max-w-lg overflow-hidden rounded-xl border border-[#222831] bg-[#0B0E11] shadow-[0_24px_80px_-16px_rgba(0,0,0,0.85)]">
        <div className="flex items-center justify-between border-b border-[#222831] bg-[#13171D] px-5 py-3.5">
          <div className="flex items-center gap-2.5">
            <div className="flex h-7 w-7 items-center justify-center rounded-md border border-[#38BDF8]/30 bg-[#38BDF8]/10">
              <Wallet className="h-4 w-4 text-[#38BDF8]" />
            </div>
            <div>
              <h2 className="text-sm font-semibold text-[#EAECEF]">
                EscrowVault Custody &amp; Wallet
              </h2>
              <p className="text-[11px] text-[#848E9C]">
                N_conf = 3 Finality · EIP-712 Hot/Cold Tiering
              </p>
            </div>
          </div>
          <button
            type="button"
            onClick={() => {
              playClickSfx();
              onClose();
            }}
            className="btn-tactile cursor-pointer rounded-md p-1.5 text-[#848E9C] hover:bg-[#1A1F27] hover:text-[#EAECEF]"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="p-5">{depositWithdrawConsole}</div>
      </div>
    </div>
  );
}
