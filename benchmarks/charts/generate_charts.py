"""Automated dual-theme SVG chart generator for SettleCore C++17 CLOB benchmarks."""

from __future__ import annotations

import argparse
import csv
import json
from pathlib import Path

import matplotlib.pyplot as plt
import matplotlib.ticker as mticker
import numpy as np

from styles import THEMES, ThemePalette, apply_theme


def _load_latency_percentiles(csv_path: Path) -> list[tuple[str, float, float, int]]:
    rows: list[tuple[str, float, float, int]] = []
    with csv_path.open("r", encoding="utf-8", newline="") as handle:
        reader = csv.DictReader(handle)
        for row in reader:
            pct = float(row["percentile"])
            label = f"p{pct:g}"
            latency_us = float(row["latency_us"])
            co_latency_us = float(row.get("co_latency_us", latency_us))
            sample_count = int(row["sample_count"])
            rows.append((label, latency_us, co_latency_us, sample_count))
    return rows


def _load_wal_overhead(csv_path: Path) -> dict[str, dict[str, float]]:
    modes: dict[str, dict[str, float]] = {}
    with csv_path.open("r", encoding="utf-8", newline="") as handle:
        reader = csv.DictReader(handle)
        for row in reader:
            modes[row["mode"]] = {
                "p50_us": float(row["p50_us"]),
                "p90_us": float(row["p90_us"]),
                "p99_us": float(row["p99_us"]),
                "p999_us": float(row["p999_us"]),
                "throughput_ops_sec": float(row["throughput_ops_sec"]),
            }
    return modes


def _load_depth_scaling(
    json_path: Path,
) -> tuple[dict[str, list[tuple[int, float]]], float | None]:
    data = json.loads(json_path.read_text(encoding="utf-8"))
    series: dict[str, list[tuple[int, float]]] = {
        "Limit Insert": [],
        "O(1) Cancel": [],
        "Aggressive Match": [],
    }
    balanced_mops: float | None = None
    for entry in data.get("benchmarks", []):
        name: str = entry.get("name", "")
        ops_sec = float(entry.get("items_per_second", 0.0))
        mops = ops_sec / 1_000_000.0
        if name == "BM_W1_BalancedMarketMaker":
            balanced_mops = mops
            continue
        if "/" not in name:
            continue
        base, arg_str = name.split("/", 1)
        try:
            depth = int(arg_str.split("_")[0])
        except ValueError:
            continue
        if "LimitInsert" in base:
            series["Limit Insert"].append((depth, mops))
        elif "Cancel" in base:
            series["O(1) Cancel"].append((depth, mops))
        elif "AggressiveMatch" in base:
            series["Aggressive Match"].append((depth, mops))

    for points in series.values():
        points.sort(key=lambda item: item[0])
    return series, balanced_mops


def _format_latency(val_us: float) -> str:
    """Format latency in compact µs/ms units so bar labels stay narrow and readable."""
    if val_us >= 1000.0:
        ms = val_us / 1000.0
        return f"{ms:.2f} ms" if ms < 10.0 else f"{ms:.1f} ms"
    if val_us >= 10.0:
        return f"{val_us:.1f} µs"
    return f"{val_us:.2f} µs"


def _format_log_ticks(val: float, _pos: int) -> str:
    if val >= 1000.0:
        ms = val / 1000.0
        return f"{ms:g} ms"
    return f"{val:g} µs"


def _style_axes(ax: plt.Axes, theme: ThemePalette) -> None:
    ax.spines["top"].set_visible(False)
    ax.spines["right"].set_visible(False)
    ax.spines["left"].set_color(theme.grid)
    ax.spines["bottom"].set_color(theme.grid)
    ax.set_axisbelow(True)


def _place_top_legend(ax: plt.Axes, theme: ThemePalette, ncol: int = 3) -> None:
    ax.legend(
        frameon=True,
        facecolor=theme.canvas_bg,
        edgecolor=theme.grid,
        labelcolor=theme.text,
        loc="lower center",
        bbox_to_anchor=(0.5, 1.02),
        ncol=ncol,
        fontsize=9.0,
        handlelength=2.0,
        columnspacing=1.6,
    )


def _annotate_vertical_bars(
    ax: plt.Axes,
    bars: plt.container.BarContainer,
    vals: list[float],
    theme: ThemePalette,
    multiplier: float = 1.15,
) -> None:
    """Place rotated 90-degree labels above log-scale bars to guarantee zero horizontal overlap."""
    for bar, val in zip(bars, vals, strict=True):
        if val <= 0.0:
            continue
        ax.text(
            bar.get_x() + bar.get_width() / 2.0,
            val * multiplier,
            _format_latency(val),
            ha="center",
            va="bottom",
            rotation=90,
            fontsize=8.2,
            fontweight="bold",
            color=theme.text,
            bbox={
                "boxstyle": "round,pad=0.10",
                "facecolor": theme.panel_bg,
                "edgecolor": "none",
                "alpha": 0.88,
            },
            zorder=6,
        )


def _save_outputs(
    fig: plt.Figure,
    out_path: Path,
    description: str,
    preview_png: bool,
) -> None:
    fig.savefig(
        out_path,
        format="svg",
        metadata={"Description": description},
    )
    if preview_png:
        fig.savefig(
            out_path.with_suffix(".png"),
            format="png",
            dpi=300,
        )
    plt.close(fig)


def render_latency_percentiles(
    rows: list[tuple[str, float, float, int]],
    wal_modes: dict[str, dict[str, float]],
    theme: ThemePalette,
    out_path: Path,
    *,
    preview_png: bool = False,
) -> None:
    """Render grouped log-scale latency percentile chart across all 3 measurement profiles."""
    apply_theme(theme)
    fig, ax = plt.subplots(figsize=(10.6, 5.6))

    labels = [r[0] for r in rows]
    raw_vals = [r[1] for r in rows]
    co_vals = [r[2] for r in rows]

    gc_row = wal_modes.get("group_commit_wal", {})
    gc_map = {
        "p50": gc_row.get("p50_us"),
        "p90": gc_row.get("p90_us"),
        "p99": gc_row.get("p99_us"),
        "p99.9": gc_row.get("p999_us"),
    }
    gc_vals = [gc_map.get(lbl) for lbl in labels]

    x = np.arange(len(labels))
    width = 0.25

    bars_raw = ax.bar(
        x - width,
        raw_vals,
        width=width,
        label="In-Memory Raw",
        color=theme.primary_accent,
        zorder=3,
    )
    _annotate_vertical_bars(ax, bars_raw, raw_vals, theme)

    gc_x = [pos for pos, val in zip(x, gc_vals, strict=True) if val is not None]
    gc_present = [float(val) for val in gc_vals if val is not None]
    bars_gc = ax.bar(
        gc_x,
        gc_present,
        width=width,
        label="Group-Commit WAL (64-batch)",
        color=theme.secondary_green,
        zorder=3,
    )
    _annotate_vertical_bars(ax, bars_gc, gc_present, theme)

    bars_co = ax.bar(
        x + width,
        co_vals,
        width=width,
        label="Coordinated-Omission Adjusted",
        color=theme.tail_accent,
        zorder=3,
    )
    _annotate_vertical_bars(ax, bars_co, co_vals, theme)

    # Mark unmeasured p99.99 slot for Group-Commit WAL cleanly at baseline
    for pos, val in zip(x, gc_vals, strict=True):
        if val is None:
            ax.text(
                pos,
                0.11,
                "n/r",
                ha="center",
                va="bottom",
                fontsize=7.6,
                color=theme.muted,
                zorder=4,
            )

    sla_us = 10.0
    ax.axhline(
        sla_us,
        color=theme.extreme_accent,
        linestyle="--",
        linewidth=1.5,
        zorder=2,
    )
    ax.text(
        0.015,
        sla_us * 1.18,
        "10 µs Engine SLA Target",
        transform=ax.get_yaxis_transform(),
        color=theme.extreme_accent,
        fontsize=8.6,
        fontweight="bold",
        ha="left",
        va="bottom",
        bbox={
            "boxstyle": "round,pad=0.22",
            "facecolor": theme.panel_bg,
            "edgecolor": "none",
            "alpha": 0.9,
        },
        zorder=5,
    )

    max_val = max([*raw_vals, *co_vals, *gc_present])
    ax.set_yscale("log")
    ax.set_ylim(0.08, max_val * 28.0)
    ax.yaxis.set_major_formatter(mticker.FuncFormatter(_format_log_ticks))
    ax.set_xticks(x)
    ax.set_xticklabels(labels, fontweight="bold", fontsize=10.5, color=theme.text)
    ax.set_xlabel("Latency Percentile", labelpad=8)
    ax.set_ylabel("Order Execution Latency (Log Scale)")

    _style_axes(ax, theme)
    ax.grid(axis="x", visible=False)
    _place_top_legend(ax, theme, ncol=3)

    ax.set_title(
        "CLOB Matching Latency Distribution",
        fontsize=13.5,
        fontweight="bold",
        color=theme.text,
        pad=38,
    )

    fig.tight_layout()
    _save_outputs(
        fig,
        out_path,
        "SettleCore CLOB latency percentiles (In-Memory Raw, Group-Commit WAL, CO-Adjusted)",
        preview_png,
    )


def render_throughput_vs_depth(
    series: dict[str, list[tuple[int, float]]],
    balanced_mops: float | None,
    theme: ThemePalette,
    out_path: Path,
    *,
    preview_png: bool = False,
) -> None:
    """Render log-depth vs sustained throughput SVG with collision-free data labels."""
    apply_theme(theme)
    fig, ax = plt.subplots(figsize=(8.6, 5.2))

    palette = {
        "Limit Insert": theme.primary_accent,
        "O(1) Cancel": theme.secondary_green,
        "Aggressive Match": theme.tail_accent,
    }
    markers = {
        "Limit Insert": "o",
        "O(1) Cancel": "s",
        "Aggressive Match": "^",
    }

    max_y = 1.0
    for name, points in series.items():
        if not points:
            continue
        xs = [p[0] for p in points]
        ys = [p[1] for p in points]
        max_y = max(max_y, max(ys))
        ax.plot(
            xs,
            ys,
            label=name,
            color=palette[name],
            marker=markers[name],
            markersize=7.2,
            linewidth=2.4,
            zorder=4,
        )

        for x_v, y_v in points:
            # Position labels per-series/point to avoid line & marker crossings
            if name == "Limit Insert":
                xytext = (0, -14)
                va = "top"
                ha = "center"
            elif name == "O(1) Cancel" and x_v == 1000:
                xytext = (10, 8)
                va = "bottom"
                ha = "left"
            else:
                xytext = (0, 10)
                va = "bottom"
                ha = "center"

            ax.annotate(
                f"{y_v:.2f}M",
                xy=(x_v, y_v),
                xytext=xytext,
                textcoords="offset points",
                ha=ha,
                va=va,
                fontsize=8.6,
                fontweight="bold",
                color=theme.text,
                zorder=6,
            )

    if balanced_mops is not None:
        ax.axhline(
            balanced_mops,
            color=theme.muted,
            linestyle="--",
            linewidth=1.3,
            alpha=0.85,
            zorder=2,
        )
        ax.text(
            100_000,
            balanced_mops + 0.28,
            f"Balanced MM Steady-State: {balanced_mops:.2f}M ops/s",
            ha="right",
            va="bottom",
            fontsize=8.2,
            fontweight="bold",
            color=theme.muted,
            bbox={
                "boxstyle": "round,pad=0.18",
                "facecolor": theme.panel_bg,
                "edgecolor": "none",
                "alpha": 0.9,
            },
            zorder=5,
        )

    ax.set_xscale("log")
    ax.set_xlim(65, 155_000)
    ax.set_xticks([100, 1000, 10000, 100000])
    ax.set_xticklabels(["10²", "10³", "10⁴", "10⁵"], fontweight="bold")
    ax.set_ylim(0.0, max_y * 1.18)
    ax.set_xlabel("Active Resting Price Levels (Log Scale)", labelpad=8)
    ax.set_ylabel("Sustained Throughput (Million Orders / sec)")

    _style_axes(ax, theme)
    _place_top_legend(ax, theme, ncol=3)
    ax.set_title(
        "O(1) Book Depth Scaling (10² → 10⁵ Price Levels)",
        fontsize=13.0,
        fontweight="bold",
        color=theme.text,
        pad=36,
    )

    fig.tight_layout()
    _save_outputs(
        fig,
        out_path,
        "SettleCore depth scaling (group_commit_wal benchmark suite)",
        preview_png,
    )


def render_wal_overhead(
    modes: dict[str, dict[str, float]],
    theme: ThemePalette,
    out_path: Path,
    *,
    preview_png: bool = False,
) -> None:
    """Render grouped latency comparison across all 4 WAL durability modes."""
    apply_theme(theme)
    fig, ax = plt.subplots(figsize=(10.6, 5.6))

    ordered_keys = ["in_memory", "buffered_wal", "group_commit_wal", "fsync_wal"]
    display_names = {
        "in_memory": "In-Memory",
        "buffered_wal": "Buffered WAL",
        "group_commit_wal": "★ Group-Commit WAL",
        "fsync_wal": "Per-Order Fsync WAL",
    }
    metrics = [("p50_us", "p50"), ("p99_us", "p99"), ("p999_us", "p99.9")]
    metric_colors = [theme.primary_accent, theme.tail_accent, theme.extreme_accent]

    x = np.arange(len(ordered_keys))
    width = 0.25

    gc_idx = ordered_keys.index("group_commit_wal")
    ax.axvspan(
        gc_idx - 0.44,
        gc_idx + 0.44,
        color=theme.secondary_green,
        alpha=0.10,
        zorder=1,
    )
    ax.text(
        gc_idx,
        0.975,
        "DURABLE BATCH (64-cmd)",
        transform=ax.get_xaxis_transform(),
        ha="center",
        va="top",
        fontsize=8.2,
        fontweight="bold",
        color=theme.secondary_green,
        bbox={
            "boxstyle": "round,pad=0.22",
            "facecolor": theme.canvas_bg,
            "edgecolor": theme.secondary_green,
            "linewidth": 0.9,
            "alpha": 0.95,
        },
        zorder=5,
    )

    max_val = 1.0
    for idx, (m_key, m_label) in enumerate(metrics):
        vals = [max(modes.get(k, {}).get(m_key, 0.0), 0.05) for k in ordered_keys]
        max_val = max(max_val, *vals)
        offset = (idx - 1) * width
        is_gc_series = [k == "group_commit_wal" for k in ordered_keys]
        bars = ax.bar(
            x + offset,
            vals,
            width=width,
            label=m_label,
            color=metric_colors[idx],
            edgecolor=[theme.secondary_green if g else "none" for g in is_gc_series],
            linewidth=[1.6 if g else 0.0 for g in is_gc_series],
            zorder=3,
        )
        _annotate_vertical_bars(ax, bars, vals, theme, multiplier=1.15)

    ax.set_yscale("log")
    ax.set_ylim(0.05, max_val * 65.0)
    ax.yaxis.set_major_formatter(mticker.FuncFormatter(_format_log_ticks))
    ax.set_xticks(x)

    x_labels = []
    for k in ordered_keys:
        ops = modes.get(k, {}).get("throughput_ops_sec", 0.0)
        if ops >= 1e6:
            ops_str = f"{ops / 1e6:.2f}M ops/s"
        elif ops >= 1e3:
            ops_str = f"{ops / 1e3:.1f}k ops/s"
        else:
            ops_str = f"{ops:.0f} ops/s"
        x_labels.append(f"{display_names[k]}\n({ops_str})")

    ax.set_xticklabels(x_labels, color=theme.text, fontweight="bold", fontsize=9.4)
    ax.set_ylabel("Execution Latency (Log Scale)")
    _style_axes(ax, theme)
    ax.grid(axis="x", visible=False)
    _place_top_legend(ax, theme, ncol=3)
    ax.set_title(
        "64-Byte CRC32C WAL Durability Overhead (4 Modes)",
        fontsize=13.5,
        fontweight="bold",
        color=theme.text,
        pad=38,
    )

    fig.tight_layout()
    _save_outputs(
        fig,
        out_path,
        "WAL modes: in_memory, buffered_wal, group_commit_wal, fsync_wal",
        preview_png,
    )


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Render dark and light benchmark SVGs for SettleCore."
    )
    parser.add_argument("--input-dir", type=Path, default=Path("../results"))
    parser.add_argument("--output-dir", type=Path, default=Path("../../docs/assets/benchmarks"))
    parser.add_argument(
        "--preview-png",
        action="store_true",
        help="Also emit 300-DPI PNG previews alongside SVGs for visual inspection.",
    )
    args = parser.parse_args()

    input_dir: Path = args.input_dir
    output_dir: Path = args.output_dir
    output_dir.mkdir(parents=True, exist_ok=True)

    latency_rows = _load_latency_percentiles(input_dir / "latency_percentiles.csv")
    wal_modes = _load_wal_overhead(input_dir / "wal_overhead.csv")
    depth_series, balanced_mops = _load_depth_scaling(input_dir / "clob_bench.json")

    for theme_name, theme in THEMES.items():
        render_latency_percentiles(
            latency_rows,
            wal_modes,
            theme,
            output_dir / f"latency-percentiles-{theme_name}.svg",
            preview_png=args.preview_png,
        )
        render_throughput_vs_depth(
            depth_series,
            balanced_mops,
            theme,
            output_dir / f"throughput-vs-depth-{theme_name}.svg",
            preview_png=args.preview_png,
        )
        render_wal_overhead(
            wal_modes,
            theme,
            output_dir / f"wal-overhead-{theme_name}.svg",
            preview_png=args.preview_png,
        )

    print(f"Generated 6 dark/light SVG benchmark charts in {output_dir.resolve()}")


if __name__ == "__main__":
    main()
