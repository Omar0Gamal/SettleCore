"use client";

import { useEffect } from "react";
import { CheckCircle2, ShieldAlert, ShieldCheck, X } from "lucide-react";
import type { TelemetryState } from "@/hooks/useTelemetryStream";
import { evaluateInvariants } from "@/lib/invariants";
import { playClickSfx } from "@/lib/sound";

interface InvariantsModalProps {
  open: boolean;
  telemetry: TelemetryState;
  onClose: () => void;
}

export function InvariantsModal({ open, telemetry, onClose }: InvariantsModalProps) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  const invariants = evaluateInvariants(telemetry);
  const passingCount = invariants.filter((i) => i.ok).length;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 p-4 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        data-testid="invariants-modal"
        onClick={(e) => e.stopPropagation()}
        className="w-full max-w-3xl overflow-hidden rounded-xl border border-[#222831] bg-[#0B0E11] shadow-[0_24px_80px_-16px_rgba(0,0,0,0.9)]"
      >
        <div className="flex items-center justify-between border-b border-[#222831] bg-[#13171D] px-5 py-3.5">
          <div className="flex items-center gap-2.5">
            <div className="flex h-7 w-7 items-center justify-center rounded-md border border-[#0ECB81]/35 bg-[#0ECB81]/10">
              <ShieldCheck className="h-4 w-4 text-[#0ECB81]" />
            </div>
            <div>
              <h2 className="text-sm font-bold text-[#EAECEF]">
                Live System Invariants ({passingCount}/6 OK)
              </h2>
              <p className="text-[11px] text-[#848E9C]">
                Enforced continuously across Postgres 18, C++17 CLOB, Go 1.27 Services &amp; Solidity EscrowVault
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

        <div className="grid gap-2.5 p-4 sm:grid-cols-2">
          {invariants.map((inv) => (
            <div
              key={inv.id}
              className={`rounded-lg border p-3 text-xs tabular-nums ${
                inv.ok
                  ? "border-[#222831] bg-[#13171D]"
                  : "border-[#F6465D]/50 bg-[#F6465D]/10"
              }`}
            >
              <div className="flex items-center justify-between gap-2">
                <span className="font-bold text-[#EAECEF]">
                  <span className="mr-1.5 text-[#38BDF8]">{inv.id}</span>
                  {inv.title}
                </span>
                <span
                  className={`inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-bold ${
                    inv.ok
                      ? "bg-[#0ECB81]/15 text-[#0ECB81]"
                      : "bg-[#F6465D]/20 text-[#F6465D]"
                  }`}
                >
                  {inv.ok ? (
                    <CheckCircle2 className="h-3 w-3" />
                  ) : (
                    <ShieldAlert className="h-3 w-3" />
                  )}
                  {inv.ok ? "PASS" : "BREACH"}
                </span>
              </div>
              <div className="mt-1 text-[11px] font-semibold text-[#38BDF8]">{inv.valueText}</div>
              <div className="mt-1 text-[10px] text-[#848E9C]">{inv.enforcer}</div>
              <p className="mt-1.5 text-[11px] leading-snug text-[#EAECEF]/85">{inv.detail}</p>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
