"use client";

import { ArrowLeft, ArrowRight, Compass, X } from "lucide-react";
import { playClickSfx } from "@/lib/sound";

export interface TourStep {
  step: number;
  title: string;
  workspace: "trade" | "custody" | "chaos" | "por";
  openLifecycle?: boolean;
  summary: string;
  whyItMatters: string;
}

export const TOUR_STEPS: TourStep[] = [
  {
    step: 1,
    title: "Two-Phase Order Matching",
    workspace: "trade",
    summary:
      "Every Limit or Market order reserves margin in Postgres 18 first, then crosses the C ABI into the single-threaded C++17 CLOB (0.183 µs median).",
    whyItMatters: "Zero float64 arithmetic across the entire matching and custody path (int64 1e8).",
  },
  {
    step: 2,
    title: "Order Lifecycle & Double-Entry",
    workspace: "trade",
    openLifecycle: true,
    summary:
      "Inspect all 5 stages from EIP-712 verification and Pre-Trade Reserve to Group-Commit CRC32C WAL and append-only double-entry ledger legs.",
    whyItMatters: "Postgres deferred constraint trigger forbids any unbalanced journal at commit.",
  },
  {
    step: 3,
    title: "3-Block Chain Reorg Rollback",
    workspace: "chaos",
    summary:
      "Trigger a 3-block EVM reorg in Chaos Lab: parent-hash mismatch orphans Block #103 and atomically reverses the credited 5.0 ETH deposit.",
    whyItMatters: "Deposits credit only after N_conf = 3 confirmations and reverse atomically on reorg.",
  },
  {
    step: 4,
    title: "Mid-Flight Gateway Crash Recovery",
    workspace: "chaos",
    summary:
      "Kill Gateway Mid-Flight simulates a crash between Postgres Reserve and C++17 WAL append. Startup recovery transitions RESERVED -> ABORTED_UNSUBMITTED.",
    whyItMatters: "Locked funds never get stranded even on process SIGKILL mid-request.",
  },
  {
    step: 5,
    title: "Rolling 24h Token-Bucket Vault",
    workspace: "custody",
    summary:
      "EscrowVault enforces a continuous rolling 24h token-bucket hot withdrawal cap (eliminating UTC-midnight double-drain) and cold EIP-712 co-signing.",
    whyItMatters: "Separate ATTESTER role isolates reserve root publication from the hot key.",
  },
  {
    step: 6,
    title: "Merkle Sum Tree Solvency",
    workspace: "por",
    summary:
      "Click 'Verify Merkle Proof' to hash your leaf up the ADR-008 Merkle Sum Tree and check EscrowVault.isSolvent(token) == true against total liabilities.",
    whyItMatters: "Proves both account inclusion AND that total liabilities never exceed on-chain reserves.",
  },
];

interface GuidedTourBannerProps {
  open: boolean;
  stepIndex: number;
  onSelectStep: (idx: number) => void;
  onClose: () => void;
}

export function GuidedTourBanner({
  open,
  stepIndex,
  onSelectStep,
  onClose,
}: GuidedTourBannerProps) {
  if (!open) return null;

  const active = TOUR_STEPS[stepIndex] ?? TOUR_STEPS[0]!;
  const isFirst = stepIndex === 0;
  const isLast = stepIndex === TOUR_STEPS.length - 1;

  return (
    <div
      data-testid="guided-tour-banner"
      role="dialog"
      aria-label="Platform Architecture Guide"
      className="fixed bottom-10 right-4 z-40 w-[390px] max-w-[calc(100vw-2rem)] rounded-xl border border-[#38BDF8]/45 bg-[#13171D]/95 p-3.5 shadow-[0_20px_60px_-10px_rgba(0,0,0,0.9)] backdrop-blur-md"
    >
      <div className="flex items-center justify-between gap-2 border-b border-[#222831] pb-2">
        <div className="flex items-center gap-2">
          <span className="inline-flex h-5 w-5 items-center justify-center rounded bg-[#38BDF8]/15 text-[#38BDF8]">
            <Compass className="h-3.5 w-3.5" />
          </span>
          <span className="text-[11px] font-bold uppercase tracking-wider text-[#38BDF8]">
            Step {active.step}/{TOUR_STEPS.length}
          </span>
          <span className="text-xs font-bold text-[#EAECEF]">{active.title}</span>
        </div>

        <button
          type="button"
          data-testid="tour-close-btn"
          onClick={() => {
            playClickSfx();
            onClose();
          }}
          className="btn-tactile cursor-pointer rounded p-1 text-[#848E9C] hover:bg-[#1A1F27] hover:text-[#EAECEF]"
          aria-label="Close architecture guide"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </div>

      <p className="mt-2 text-xs leading-relaxed text-[#EAECEF]">{active.summary}</p>
      <p className="mt-1.5 rounded border border-[#222831] bg-[#0B0E11] px-2.5 py-1.5 text-[11px] text-[#0ECB81]">
        {active.whyItMatters}
      </p>

      <div className="mt-3 flex items-center justify-between gap-2">
        <div className="flex items-center gap-1">
          {TOUR_STEPS.map((s, idx) => (
            <button
              key={s.step}
              type="button"
              aria-label={`Jump to step ${s.step}`}
              onClick={() => {
                playClickSfx();
                onSelectStep(idx);
              }}
              className={`h-1.5 cursor-pointer rounded-full transition-all ${
                idx === stepIndex ? "w-5 bg-[#38BDF8]" : "w-1.5 bg-[#2B3139] hover:bg-[#474D57]"
              }`}
            />
          ))}
        </div>

        <div className="flex items-center gap-1.5">
          {!isFirst && (
            <button
              type="button"
              onClick={() => {
                playClickSfx();
                onSelectStep(stepIndex - 1);
              }}
              className="btn-tactile inline-flex cursor-pointer items-center gap-1 rounded border border-[#222831] bg-[#1A1F27] px-2.5 py-1 text-[11px] font-semibold text-[#EAECEF] hover:bg-[#222831]"
            >
              <ArrowLeft className="h-3 w-3" />
              Prev
            </button>
          )}
          <button
            type="button"
            data-testid="tour-next-btn"
            onClick={() => {
              playClickSfx();
              if (isLast) {
                onClose();
              } else {
                onSelectStep(stepIndex + 1);
              }
            }}
            className="btn-tactile inline-flex cursor-pointer items-center gap-1 rounded bg-[#38BDF8] px-3 py-1 text-[11px] font-bold text-[#0B0E11] hover:bg-sky-300"
          >
            {isLast ? "Done" : "Next Step →"}
            {!isLast && <ArrowRight className="h-3 w-3" />}
          </button>
        </div>
      </div>
    </div>
  );
}
