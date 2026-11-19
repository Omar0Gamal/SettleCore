"""Dual-theme GitHub dark/light styling tokens for SettleCore benchmark SVGs."""

from __future__ import annotations

from dataclasses import dataclass

import matplotlib as mpl


@dataclass(frozen=True)
class ThemePalette:
    """Color tokens matching docs/benchmarking.md for GitHub dark and light themes."""

    name: str
    canvas_bg: str
    panel_bg: str
    grid: str
    text: str
    muted: str
    primary_accent: str
    tail_accent: str
    extreme_accent: str
    secondary_green: str


THEMES: dict[str, ThemePalette] = {
    "dark": ThemePalette(
        name="dark",
        canvas_bg="#0D1117",
        panel_bg="#161B22",
        grid="#21262D",
        text="#F0F6FC",
        muted="#8B949E",
        primary_accent="#38BDF8",
        tail_accent="#818CF8",
        extreme_accent="#F43F5E",
        secondary_green="#34D399",
    ),
    "light": ThemePalette(
        name="light",
        canvas_bg="#FFFFFF",
        panel_bg="#F8FAFC",
        grid="#E5E7EB",
        text="#111827",
        muted="#4B5563",
        primary_accent="#0284C7",
        tail_accent="#4F46E5",
        extreme_accent="#E11D48",
        secondary_green="#059669",
    ),
}


def apply_theme(theme: ThemePalette) -> None:
    """Configure Matplotlib rcParams for crisp vector SVG rendering."""
    mpl.rcParams.update(
        {
            "figure.facecolor": theme.canvas_bg,
            "axes.facecolor": theme.panel_bg,
            "axes.edgecolor": theme.grid,
            "axes.labelcolor": theme.text,
            "axes.titlecolor": theme.text,
            "axes.grid": True,
            "grid.color": theme.grid,
            "grid.linestyle": "-",
            "grid.linewidth": 0.8,
            "xtick.color": theme.muted,
            "ytick.color": theme.muted,
            "text.color": theme.text,
            "font.family": "sans-serif",
            "font.sans-serif": ["DejaVu Sans", "Inter", "Arial", "Helvetica"],
            "font.size": 10.5,
            "axes.titlesize": 12.5,
            "axes.titleweight": "bold",
            "axes.labelsize": 10.5,
            "svg.fonttype": "none",
            "savefig.facecolor": theme.canvas_bg,
            "savefig.edgecolor": theme.canvas_bg,
            "savefig.bbox": "tight",
            "savefig.pad_inches": 0.25,
        }
    )
