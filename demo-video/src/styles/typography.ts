import type React from "react";

export const UI_FONT_FAMILY =
  "'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif";

export const TABULAR_FONT_STYLE: React.CSSProperties = {
  fontFamily: UI_FONT_FAMILY,
  fontVariantNumeric: "tabular-nums",
  fontFeatureSettings: '"tnum" 1, "cv01" 1, "cv11" 1, "zero" 0',
};
