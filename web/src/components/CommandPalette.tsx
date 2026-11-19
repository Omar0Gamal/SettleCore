"use client";

import { useEffect, useMemo, useState } from "react";
import { Command, Search, X } from "lucide-react";
import { playClickSfx } from "@/lib/sound";

export interface CommandItem {
  id: string;
  group: string;
  label: string;
  shortcut?: string;
  run: () => void;
}

interface CommandPaletteProps {
  open: boolean;
  commands: CommandItem[];
  onClose: () => void;
}

function CommandPaletteInner({
  commands,
  onClose,
}: {
  commands: CommandItem[];
  onClose: () => void;
}) {
  const [query, setQuery] = useState("");
  const [selectedIdx, setSelectedIdx] = useState(0);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return commands;
    return commands.filter(
      (c) => c.label.toLowerCase().includes(q) || c.group.toLowerCase().includes(q)
    );
  }, [commands, query]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      } else if (e.key === "ArrowDown") {
        e.preventDefault();
        setSelectedIdx((i) => (filtered.length > 0 ? (i + 1) % filtered.length : 0));
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setSelectedIdx((i) =>
          filtered.length > 0 ? (i - 1 + filtered.length) % filtered.length : 0
        );
      } else if (e.key === "Enter") {
        e.preventDefault();
        const item = filtered[selectedIdx];
        if (item) {
          playClickSfx();
          item.run();
          onClose();
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [filtered, selectedIdx, onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/75 pt-20 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        data-testid="cmdk-modal"
        onClick={(e) => e.stopPropagation()}
        className="w-full max-w-xl overflow-hidden rounded-xl border border-[#222831] bg-[#0B0E11] shadow-[0_24px_80px_-16px_rgba(0,0,0,0.9)]"
      >
        <div className="flex items-center gap-2.5 border-b border-[#222831] bg-[#13171D] px-3.5 py-2.5">
          <Search className="h-4 w-4 text-[#38BDF8]" />
          <input
            autoFocus
            type="text"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setSelectedIdx(0);
            }}
            placeholder="Jump to workspace, switch pair, inspect lifecycle, or fire chaos drill..."
            className="min-w-0 flex-1 bg-transparent text-xs text-[#EAECEF] placeholder-[#848E9C] outline-none"
          />
          <button
            type="button"
            onClick={onClose}
            className="cursor-pointer rounded p-1 text-[#848E9C] hover:bg-[#1A1F27] hover:text-[#EAECEF]"
            aria-label="Close command palette"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </div>

        <div className="max-h-80 overflow-y-auto p-1.5">
          {filtered.length === 0 ? (
            <div className="py-8 text-center text-xs text-[#848E9C]">
              No matching commands for &quot;{query}&quot;
            </div>
          ) : (
            filtered.map((cmd, idx) => (
              <button
                key={cmd.id}
                type="button"
                onClick={() => {
                  playClickSfx();
                  cmd.run();
                  onClose();
                }}
                onMouseEnter={() => setSelectedIdx(idx)}
                className={`flex w-full cursor-pointer items-center justify-between rounded-md px-3 py-2 text-left text-xs transition-colors ${
                  idx === selectedIdx
                    ? "bg-[#1A1F27] text-[#EAECEF]"
                    : "text-[#848E9C] hover:bg-[#13171D] hover:text-[#EAECEF]"
                }`}
              >
                <div className="flex items-center gap-2.5">
                  <span className="rounded border border-[#222831] bg-[#0B0E11] px-1.5 py-0.5 text-[10px] font-semibold uppercase text-[#38BDF8]">
                    {cmd.group}
                  </span>
                  <span className="font-medium text-[#EAECEF]">{cmd.label}</span>
                </div>
                {cmd.shortcut && (
                  <kbd className="rounded border border-[#222831] bg-[#0B0E11] px-1.5 py-0.5 text-[10px] font-medium text-[#848E9C]">
                    {cmd.shortcut}
                  </kbd>
                )}
              </button>
            ))
          )}
        </div>

        <div className="flex items-center justify-between border-t border-[#222831] bg-[#13171D] px-3.5 py-1.5 text-[10px] text-[#848E9C]">
          <span className="inline-flex items-center gap-1">
            <Command className="h-3 w-3 text-[#38BDF8]" />
            SettleCore Command Palette
          </span>
          <span>↑↓ Navigate · ↵ Execute · Esc Close</span>
        </div>
      </div>
    </div>
  );
}

export function CommandPalette({ open, commands, onClose }: CommandPaletteProps) {
  if (!open) return null;
  return <CommandPaletteInner commands={commands} onClose={onClose} />;
}
