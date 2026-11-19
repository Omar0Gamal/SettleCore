"""Generate dual-theme dark/light vector SVG system architecture diagrams for README.md."""

from __future__ import annotations

import argparse
from dataclasses import dataclass
from pathlib import Path
from xml.sax.saxutils import escape


@dataclass(frozen=True)
class ArchTheme:
    """Color tokens for dark (#0B0E11) and light (#FFFFFF) SVG system topology."""

    name: str
    canvas_bg: str
    zone_bg: str
    zone_border: str
    card_bg: str
    card_border: str
    text_primary: str
    text_secondary: str
    text_muted: str
    cyan: str
    cyan_soft: str
    emerald: str
    emerald_soft: str
    indigo: str
    indigo_soft: str
    amber: str
    amber_soft: str
    arrow: str
    pill_bg: str


THEMES: dict[str, ArchTheme] = {
    "dark": ArchTheme(
        name="dark",
        canvas_bg="#0B0E11",
        zone_bg="#11161D",
        zone_border="#242C37",
        card_bg="#161C24",
        card_border="#2B3543",
        text_primary="#F0F6FC",
        text_secondary="#CBD5E1",
        text_muted="#8B949E",
        cyan="#38BDF8",
        cyan_soft="#102A3A",
        emerald="#34D399",
        emerald_soft="#0F2E24",
        indigo="#818CF8",
        indigo_soft="#1E2242",
        amber="#FBBF24",
        amber_soft="#2D2310",
        arrow="#64748B",
        pill_bg="#0D1117",
    ),
    "light": ArchTheme(
        name="light",
        canvas_bg="#FFFFFF",
        zone_bg="#F8FAFC",
        zone_border="#DCE3EC",
        card_bg="#FFFFFF",
        card_border="#CBD5E1",
        text_primary="#0F172A",
        text_secondary="#334155",
        text_muted="#64748B",
        cyan="#0284C7",
        cyan_soft="#E0F2FE",
        emerald="#059669",
        emerald_soft="#D1FAE5",
        indigo="#4F46E5",
        indigo_soft="#E0E7FF",
        amber="#D97706",
        amber_soft="#FEF3C7",
        arrow="#64748B",
        pill_bg="#F1F5F9",
    ),
}

FONT_SANS = "Inter, Segoe UI, Helvetica Neue, Arial, sans-serif"
FONT_MONO = "JetBrains Mono, Cascadia Code, Consolas, monospace"


def _card(
    x: int,
    y: int,
    w: int,
    h: int,
    accent: str,
    accent_soft: str,
    badge: str,
    title: str,
    subtitle: str,
    lines: list[str],
    t: ArchTheme,
) -> str:
    parts: list[str] = [
        f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="10" '
        f'fill="{t.card_bg}" stroke="{t.card_border}" stroke-width="1.25"/>',
        f'<rect x="{x}" y="{y + 10}" width="4" height="{h - 20}" rx="2" fill="{accent}"/>',
        f'<rect x="{x + 16}" y="{y + 12}" width="{len(badge) * 6.8 + 16:.0f}" height="19" '
        f'rx="5" fill="{accent_soft}"/>',
        f'<text x="{x + 24}" y="{y + 25}" font-family="{FONT_MONO}" font-size="9.5" '
        f'font-weight="700" fill="{accent}">{escape(badge)}</text>',
        f'<text x="{x + 16}" y="{y + 49}" font-family="{FONT_SANS}" font-size="13.5" '
        f'font-weight="700" fill="{t.text_primary}">{escape(title)}</text>',
        f'<text x="{x + 16}" y="{y + 66}" font-family="{FONT_SANS}" font-size="11" '
        f'fill="{t.text_muted}">{escape(subtitle)}</text>',
    ]
    for idx, line in enumerate(lines):
        ly = y + 86 + idx * 17
        parts.append(
            f'<text x="{x + 16}" y="{ly}" font-family="{FONT_MONO}" font-size="10" '
            f'fill="{t.text_secondary}">{escape(line)}</text>'
        )
    return "\n  ".join(parts)


def _zone(
    x: int,
    y: int,
    w: int,
    h: int,
    label: str,
    accent: str,
    t: ArchTheme,
) -> str:
    pill_w = len(label) * 8.2 + 36
    return "\n  ".join(
        [
            f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="14" '
            f'fill="{t.zone_bg}" stroke="{t.zone_border}" stroke-width="1.4"/>',
            f'<rect x="{x + 16}" y="{y + 12}" width="{pill_w:.0f}" height="22" rx="6" '
            f'fill="{t.pill_bg}" stroke="{t.zone_border}" stroke-width="1"/>',
            f'<circle cx="{x + 28}" cy="{y + 23}" r="3.5" fill="{accent}"/>',
            f'<text x="{x + 38}" y="{y + 27}" font-family="{FONT_MONO}" font-size="10.2" '
            f'font-weight="700" letter-spacing="0.2" fill="{t.text_primary}">'
            f"{escape(label)}</text>",
        ]
    )


def _edge_label(
    cx: int,
    cy: int,
    text: str,
    color: str,
    t: ArchTheme,
    width: int | None = None,
) -> str:
    w = width if width is not None else int(len(text) * 6.4 + 16)
    h = 20
    return "\n  ".join(
        [
            f'<rect x="{cx - w // 2}" y="{cy - h // 2}" width="{w}" height="{h}" rx="5" '
            f'fill="{t.canvas_bg}" stroke="{t.zone_border}" stroke-width="1"/>',
            f'<text x="{cx}" y="{cy + 3.5}" text-anchor="middle" font-family="{FONT_MONO}" '
            f'font-size="9.5" font-weight="600" fill="{color}">{escape(text)}</text>',
        ]
    )


def render_system_topology(t: ArchTheme) -> str:
    """Render the 5-zone SettleCore system topology diagram as crisp vector SVG."""
    svg: list[str] = [
        '<svg xmlns="http://www.w3.org/2000/svg" width="1440" height="896" '
        'viewBox="0 0 1440 896" role="img" '
        'aria-label="SettleCore 5-Zone Event-Sourced Architecture">',
        "  <defs>",
        f'    <marker id="arrow-{t.name}" viewBox="0 0 10 10" refX="8.5" refY="5" '
        'markerWidth="7" markerHeight="7" orient="auto-start-reverse">',
        f'      <path d="M 0 1.5 L 9 5 L 0 8.5 z" fill="{t.arrow}"/>',
        "    </marker>",
        f'    <marker id="arrow-cyan-{t.name}" viewBox="0 0 10 10" refX="8.5" refY="5" '
        'markerWidth="7" markerHeight="7" orient="auto-start-reverse">',
        f'      <path d="M 0 1.5 L 9 5 L 0 8.5 z" fill="{t.cyan}"/>',
        "    </marker>",
        f'    <marker id="arrow-emerald-{t.name}" viewBox="0 0 10 10" refX="8.5" refY="5" '
        'markerWidth="7" markerHeight="7" orient="auto-start-reverse">',
        f'      <path d="M 0 1.5 L 9 5 L 0 8.5 z" fill="{t.emerald}"/>',
        "    </marker>",
        f'    <marker id="arrow-indigo-{t.name}" viewBox="0 0 10 10" refX="8.5" refY="5" '
        'markerWidth="7" markerHeight="7" orient="auto-start-reverse">',
        f'      <path d="M 0 1.5 L 9 5 L 0 8.5 z" fill="{t.indigo}"/>',
        "    </marker>",
        "  </defs>",
        f'  <rect width="1440" height="896" fill="{t.canvas_bg}"/>',
        # Header
        f'  <text x="32" y="42" font-family="{FONT_SANS}" font-size="21" font-weight="700" '
        f'fill="{t.text_primary}">SettleCore — Event-Sourced Two-Phase Architecture</text>',
        f'  <text x="32" y="64" font-family="{FONT_SANS}" font-size="12.5" '
        f'fill="{t.text_muted}">'
        "Sub-microsecond C++17 matching · PostgreSQL 18 double-entry ledger · "
        "Reorg-safe Solidity 0.8.38 custody</text>",
        # Legend pills
        f'  <rect x="910" y="28" width="154" height="28" rx="7" fill="{t.zone_bg}" '
        f'stroke="{t.zone_border}" stroke-width="1"/>',
        f'  <circle cx="926" cy="42" r="4" fill="{t.cyan}"/>',
        f'  <text x="938" y="46" font-family="{FONT_SANS}" font-size="11" font-weight="600" '
        f'fill="{t.text_secondary}">Zero-Lock Hot Path</text>',
        f'  <rect x="1074" y="28" width="166" height="28" rx="7" fill="{t.zone_bg}" '
        f'stroke="{t.zone_border}" stroke-width="1"/>',
        f'  <circle cx="1090" cy="42" r="4" fill="{t.emerald}"/>',
        f'  <text x="1102" y="46" font-family="{FONT_SANS}" font-size="11" font-weight="600" '
        f'fill="{t.text_secondary}">Double-Entry Ledger</text>',
        f'  <rect x="1250" y="28" width="158" height="28" rx="7" fill="{t.zone_bg}" '
        f'stroke="{t.zone_border}" stroke-width="1"/>',
        f'  <circle cx="1266" cy="42" r="4" fill="{t.indigo}"/>',
        f'  <text x="1278" y="46" font-family="{FONT_SANS}" font-size="11" font-weight="600" '
        f'fill="{t.text_secondary}">EVM Custody Layer</text>',
        # =====================================================================
        # TOP ROW (y = 88..476): Zones 1, 2, 3
        # =====================================================================
        "  " + _zone(32, 88, 304, 388, "ZONE 1 · CLIENTS & LIQUIDITY", t.cyan, t),
        "  "
        + _card(
            48,
            134,
            272,
            152,
            t.cyan,
            t.cyan_soft,
            "NEXT.JS 16.4 + REACT 19",
            "Trading Terminal",
            "lightweight-charts · wagmi v3 · viem v2",
            [
                "REST + WS + EIP-712 Order Signer",
                "2-Phase Order Lifecycle Inspector",
                "Merkle Sum Tree Proof Verifier",
            ],
            t,
        ),
        "  "
        + _card(
            48,
            306,
            272,
            152,
            t.cyan,
            t.cyan_soft,
            "GO 1.27 BOT",
            "External Market-Maker",
            "Public Tick Feed · Unprivileged Client",
            [
                "Two-Sided Ladder Quoting via WS",
                "Cancel-on-Disconnect Armed",
                "Zero DB or Shared Memory Access",
            ],
            t,
        ),
        # Zone 2
        "  " + _zone(416, 88, 412, 388, "ZONE 2 · GATEWAY & DOUBLE-ENTRY LEDGER", t.emerald, t),
        "  "
        + _card(
            432,
            134,
            380,
            142,
            t.cyan,
            t.cyan_soft,
            "GO 1.27 · GIN + CODER/WEBSOCKET",
            "Gateway Service",
            "EIP-712 Verification · Idempotency-Key Guard",
            [
                "65,536-slot ingress ring (>85% sheds non-cancel)",
                "25 ms coalesced L2 order book & trade fan-out",
                "Crash recovery rolls back RESERVED -> ABORTED",
            ],
            t,
        ),
        "  "
        + _card(
            432,
            320,
            380,
            140,
            t.emerald,
            t.emerald_soft,
            "POSTGRESQL 18 · PGXPOOL + SQLC",
            "Double-Entry ACID Ledger",
            "NUMERIC(38,18) · Row Lock Ordering (account_id ASC)",
            [
                "trg_ledger_entries_balanced: SUM(Dr) == SUM(Cr)",
                "trg_ledger_entries_immutable (append-only)",
                "Pre-Trade Reserve: available -> locked",
            ],
            t,
        ),
        # Zone 3
        "  " + _zone(908, 88, 500, 388, "ZONE 3 · C++17 CLOB & PAIRED JOURNALS", t.cyan, t),
        "  "
        + _card(
            924,
            134,
            468,
            134,
            t.cyan,
            t.cyan_soft,
            "C++17 · OS-PINNED THREAD (LockOSThread)",
            "Single-Threaded Zero-Lock CLOB",
            "64B OrderNode Slab · Price-Time FIFO · ±10% Collar",
            [
                "0.215 µs p50 match · 4.90M ops/s · 0 hot-path allocs",
                "Self-Trade Prevention (STP) · Batched C ABI",
            ],
            t,
        ),
        "  "
        + _card(
            924,
            298,
            226,
            104,
            t.amber,
            t.amber_soft,
            "64B CRC32C",
            "Command WAL (.wal)",
            'Magic: "SCWL"',
            [
                "GROUP_COMMIT fdatasync",
            ],
            t,
        ),
        "  "
        + _card(
            1166,
            298,
            226,
            104,
            t.emerald,
            t.emerald_soft,
            "64B CRC32C",
            "Execution Log (.evlog)",
            'Magic: "SCEV"',
            [
                "Accepts · Fills · Cancels",
            ],
            t,
        ),
        # Compact replay-cli bar inside Zone 3 bottom
        f'  <rect x="924" y="416" width="468" height="44" rx="8" fill="{t.card_bg}" '
        f'stroke="{t.card_border}" stroke-width="1.2"/>',
        f'  <rect x="924" y="424" width="4" height="28" rx="2" fill="{t.amber}"/>',
        f'  <text x="940" y="442" font-family="{FONT_MONO}" font-size="11" font-weight="700" '
        f'fill="{t.text_primary}">replay-cli</text>',
        f'  <text x="1024" y="442" font-family="{FONT_SANS}" font-size="11" '
        f'fill="{t.text_muted}">Cold .wal + binary snapshot SHA-256 verifier</text>',
        # =====================================================================
        # BOTTOM ROW (y = 548..860): Zone 5 (left) and Zone 4 (right)
        # =====================================================================
        "  "
        + _zone(
            32,
            548,
            840,
            312,
            "ZONE 5 · MULTI-RPC WATCHER, GAS-BUMPING RELAYER & SOLIDITY 0.8.38 CUSTODY",
            t.indigo,
            t,
        ),
        "  "
        + _card(
            48,
            594,
            262,
            138,
            t.indigo,
            t.indigo_soft,
            "SOLIDITY 0.8.38 · OZ v5.6.1",
            "EscrowVault Custody",
            "RateLimited · ReserveAttestation",
            [
                "EIP-712 tiered withdraw vouchers",
                "Rolling 24h token-bucket cap",
                "On-chain isSolvent(token) check",
            ],
            t,
        ),
        "  "
        + _card(
            48,
            752,
            262,
            92,
            t.indigo,
            t.indigo_soft,
            "EVM FAILOVER POOL",
            "Dual RPC Proxies",
            ":8545 Primary · :8546 Failover",
            [
                "<500 ms health-scored switch",
            ],
            t,
        ),
        "  "
        + _card(
            344,
            594,
            252,
            250,
            t.indigo,
            t.indigo_soft,
            "GO 1.27 · LINEAGE ENGINE",
            "Finality Watcher",
            "Block Parent-Hash Verifier",
            [
                "N-confirmation deposit credit",
                "Detects chain reorgs <= 64 blks",
                "Marks orphaned blocks/deposits",
                "Posts REORG_REVERSAL debit",
                "Updates indexer_block_lag",
            ],
            t,
        ),
        "  "
        + _card(
            612,
            594,
            244,
            250,
            t.indigo,
            t.indigo_soft,
            "GO 1.27 · NONCE + FEE BUMPER",
            "Withdrawal Relayer",
            "Outbox SKIP LOCKED Consumer",
            [
                "Hot/Cold EIP-712 voucher signer",
                "Serialized EOA nonce allocator",
                "+15% EIP-1559 replacement bump",
                "Finalizes locked -> VAULT",
                "Tracks stuck_tx_gas_bumps",
            ],
            t,
        ),
        # Zone 4 (bottom right)
        "  "
        + _zone(
            908,
            548,
            500,
            312,
            "ZONE 4 · EVENT-LOG SETTLEMENT & MERKLE RECONCILER",
            t.emerald,
            t,
        ),
        "  "
        + _card(
            924,
            594,
            468,
            118,
            t.emerald,
            t.emerald_soft,
            "GO 1.27 · PURE .EVLOG CONSUMER",
            "Settlement Worker",
            "Zero Matching Re-Execution · Atomic Batch Commit",
            [
                "Tails SCEV frames > engine_checkpoints.last_settled_wal_seq",
                "Batch Settle: fills, fees & release locked",
            ],
            t,
        ),
        "  "
        + _card(
            924,
            728,
            468,
            116,
            t.emerald,
            t.emerald_soft,
            "GO 1.27 · PROOF-OF-RESERVES",
            "Merkle Sum Tree Reconciler",
            "60s Solvency Invariant · Hourly Keccak-256 Attestation",
            [
                "Keccak256(0x01 || L || R || sumL+sumR) over REPEATABLE READ",
                "Publishes root via attester -> publishReserveAttestation",
            ],
            t,
        ),
        # =====================================================================
        # CLEAN ORTHOGONAL CONNECTORS & EDGE LABELS
        # =====================================================================
        # Zone 1 -> Zone 2
        f'  <path d="M 320 202 L 430 202" fill="none" stroke="{t.cyan}" stroke-width="2" '
        f'marker-end="url(#arrow-cyan-{t.name})"/>',
        "  " + _edge_label(375, 185, "REST / WS", t.cyan, t, width=72),
        f'  <path d="M 320 382 L 374 382 L 374 236 L 430 236" fill="none" stroke="{t.cyan}" '
        f'stroke-width="1.8" marker-end="url(#arrow-cyan-{t.name})"/>',
        "  " + _edge_label(375, 314, "WS Quotes", t.cyan, t, width=72),
        # Zone 2A -> Zone 2B
        f'  <path d="M 622 276 L 622 318" fill="none" stroke="{t.emerald}" stroke-width="2" '
        f'marker-end="url(#arrow-emerald-{t.name})"/>',
        "  " + _edge_label(622, 297, "Pre-Trade Reserve", t.emerald, t, width=124),
        # Zone 2A <-> Zone 3A (C ABI)
        f'  <path d="M 812 202 L 922 202" fill="none" stroke="{t.cyan}" stroke-width="2.2" '
        f'marker-start="url(#arrow-cyan-{t.name})" marker-end="url(#arrow-cyan-{t.name})"/>',
        "  " + _edge_label(867, 184, "C ABI (cgo)", t.cyan, t, width=82),
        # Zone 3A -> Zone 3B (.wal) and Zone 3C (.evlog)
        f'  <path d="M 1037 268 L 1037 296" fill="none" stroke="{t.amber}" stroke-width="1.8" '
        f'marker-end="url(#arrow-{t.name})"/>',
        f'  <path d="M 1279 268 L 1279 296" fill="none" stroke="{t.emerald}" stroke-width="1.8" '
        f'marker-end="url(#arrow-emerald-{t.name})"/>',
        # Zone 3B (.wal) -> replay-cli
        f'  <path d="M 1037 402 L 1037 415" fill="none" stroke="{t.amber}" stroke-width="1.6" '
        f'marker-end="url(#arrow-{t.name})"/>',
        # Zone 3C (.evlog) -> Zone 4 (Settlement Worker) straight vertical drop
        f'  <path d="M 1279 402 L 1279 592" fill="none" stroke="{t.emerald}" stroke-width="2.2" '
        f'marker-end="url(#arrow-emerald-{t.name})"/>',
        "  " + _edge_label(1279, 520, "Tail .evlog Frames", t.emerald, t, width=134),
        # Zone 4 Settlement -> Zone 2B PostgreSQL 18 (clean corridor y=514)
        f'  <path d="M 924 640 L 888 640 L 888 514 L 724 514 L 724 462" fill="none" '
        f'stroke="{t.emerald}" stroke-width="2" marker-end="url(#arrow-emerald-{t.name})"/>',
        "  " + _edge_label(818, 514, "Batch Settlement", t.emerald, t, width=130),
        # Zone 2B PostgreSQL 18 -> Zone 4 Reconciler (clean dashed read at y=488)
        f'  <path d="M 768 460 L 768 488 L 1424 488 L 1424 786 L 1394 786" '
        f'fill="none" stroke="{t.emerald}" stroke-width="1.5" stroke-dasharray="5 4" '
        f'marker-end="url(#arrow-emerald-{t.name})"/>',
        "  " + _edge_label(1002, 488, "Liability Snapshot", t.emerald, t, width=128),
        # Zone 5 Watcher -> Zone 2B PostgreSQL 18
        f'  <path d="M 470 592 L 470 462" fill="none" stroke="{t.indigo}" stroke-width="2" '
        f'marker-end="url(#arrow-indigo-{t.name})"/>',
        "  " + _edge_label(470, 514, "Finality / Reorg", t.indigo, t, width=116),
        # Zone 2B PostgreSQL 18 <-> Zone 5 Relayer
        f'  <path d="M 634 462 L 634 592" fill="none" stroke="{t.indigo}" stroke-width="2" '
        f'marker-start="url(#arrow-indigo-{t.name})" marker-end="url(#arrow-indigo-{t.name})"/>',
        "  " + _edge_label(634, 514, "Outbox / Settled", t.indigo, t, width=116),
        # Zone 5 internal: Watcher & Relayer <-> Dual RPC Pool -> EscrowVault
        f'  <path d="M 344 798 L 312 798" fill="none" stroke="{t.indigo}" stroke-width="1.8" '
        f'marker-start="url(#arrow-indigo-{t.name})" marker-end="url(#arrow-indigo-{t.name})"/>',
        f'  <path d="M 734 844 L 734 853 L 154 853 L 154 846" fill="none" '
        f'stroke="{t.indigo}" stroke-width="1.6" stroke-dasharray="4 3" '
        f'marker-end="url(#arrow-indigo-{t.name})"/>',
        f'  <path d="M 180 752 L 180 734" fill="none" stroke="{t.indigo}" stroke-width="2" '
        f'marker-start="url(#arrow-indigo-{t.name})" marker-end="url(#arrow-indigo-{t.name})"/>',
        # Reconciler -> Dual RPC (publishReserveAttestation) on separate bottom rail y=876
        f'  <path d="M 924 816 L 892 816 L 892 876 L 236 876 L 236 846" fill="none" '
        f'stroke="{t.emerald}" stroke-width="1.6" stroke-dasharray="5 3" '
        f'marker-end="url(#arrow-emerald-{t.name})"/>',
        "  "
        + _edge_label(
            520,
            876,
            "publishReserveAttestation (attester)",
            t.emerald,
            t,
            width=230,
        ),
        "</svg>",
    ]
    return "\n".join(svg) + "\n"


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Generate dark and light SettleCore system architecture SVGs."
    )
    parser.add_argument(
        "--output-dir",
        type=Path,
        default=Path("../../docs/assets/architecture"),
    )
    args = parser.parse_args()
    out_dir = args.output_dir.resolve()
    out_dir.mkdir(parents=True, exist_ok=True)

    for theme_name, theme in THEMES.items():
        svg_text = render_system_topology(theme)
        out_path = out_dir / f"system-topology-{theme_name}.svg"
        out_path.write_text(svg_text, encoding="utf-8")


if __name__ == "__main__":
    main()
