import React from "react";
import { Easing, interpolate, useCurrentFrame } from "remotion";
import { TABULAR_FONT_STYLE } from "../styles/typography";

export type CalloutTone = "sky" | "emerald" | "rose" | "neutral";

export interface CalloutMetricItem {
  label: string;
  value: string;
  tone?: CalloutTone;
  numericTarget?: number;
  decimals?: number;
  prefix?: string;
  suffix?: string;
}

export interface MetricCalloutProps {
  title: string;
  subtitle?: string;
  items: CalloutMetricItem[];
  startFrame?: number;
  exitFrame?: number;
  tone?: CalloutTone;
  style?: React.CSSProperties;
}

const TONE_COLORS: Record<CalloutTone, { border: string; accent: string; glow: string }> = {
  sky: {
    border: "rgba(56, 189, 248, 0.38)",
    accent: "#38BDF8",
    glow: "rgba(56, 189, 248, 0.16)",
  },
  emerald: {
    border: "rgba(16, 185, 129, 0.42)",
    accent: "#10B981",
    glow: "rgba(16, 185, 129, 0.18)",
  },
  rose: {
    border: "rgba(244, 63, 94, 0.42)",
    accent: "#F43F5E",
    glow: "rgba(244, 63, 94, 0.18)",
  },
  neutral: {
    border: "rgba(255, 255, 255, 0.14)",
    accent: "#E4E4E7",
    glow: "rgba(255, 255, 255, 0.06)",
  },
};

const SILKY_EASE = Easing.bezier(0.22, 1, 0.36, 1);

export const MetricCallout: React.FC<MetricCalloutProps> = ({
  title,
  subtitle,
  items,
  startFrame = 0,
  exitFrame,
  tone = "sky",
  style,
}) => {
  const frame = useCurrentFrame();

  const enterProgress = interpolate(frame - startFrame, [0, 32], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: SILKY_EASE,
  });

  const exitProgress =
    exitFrame !== undefined
      ? interpolate(frame - exitFrame, [0, 26], [0, 1], {
          extrapolateLeft: "clamp",
          extrapolateRight: "clamp",
          easing: SILKY_EASE,
        })
      : 0;

  const opacity = enterProgress * (1 - exitProgress);
  const translateY =
    interpolate(enterProgress, [0, 1], [20, 0]) +
    interpolate(exitProgress, [0, 1], [0, -14]);
  const scale = interpolate(enterProgress, [0, 1], [0.96, 1]);

  const countProgress = interpolate(frame - startFrame, [4, 34], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: SILKY_EASE,
  });

  const palette = TONE_COLORS[tone];

  return (
    <div
      style={{
        opacity,
        transform: `translateY(${translateY}px) scale(${scale})`,
        borderRadius: 14,
        border: `1px solid ${palette.border}`,
        background: "rgba(13, 15, 21, 0.92)",
        backdropFilter: "blur(18px)",
        boxShadow: `0 22px 60px rgba(0, 0, 0, 0.65), 0 0 36px ${palette.glow}`,
        padding: "16px 20px",
        minWidth: 360,
        ...TABULAR_FONT_STYLE,
        ...style,
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 12,
          marginBottom: 12,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span
            style={{
              width: 8,
              height: 8,
              borderRadius: "50%",
              background: palette.accent,
              boxShadow: `0 0 12px ${palette.accent}`,
            }}
          />
          <span
            style={{
              fontSize: 13,
              fontWeight: 600,
              letterSpacing: "0.04em",
              textTransform: "uppercase",
              color: "#F4F4F5",
            }}
          >
            {title}
          </span>
        </div>
        {subtitle ? (
          <span
            style={{
              ...TABULAR_FONT_STYLE,
              fontSize: 11,
              fontWeight: 500,
              color: "#A1A1AA",
            }}
          >
            {subtitle}
          </span>
        ) : null}
      </div>

      <div
        style={{
          display: "grid",
          gridTemplateColumns: `repeat(${Math.min(items.length, 3)}, minmax(0, 1fr))`,
          gap: 12,
        }}
      >
        {items.map((item) => {
          const itemTone = TONE_COLORS[item.tone ?? tone];
          let renderedValue = item.value;
          if (item.numericTarget !== undefined) {
            const val = item.numericTarget * countProgress;
            const formatted =
              item.decimals !== undefined ? val.toFixed(item.decimals) : Math.round(val).toString();
            renderedValue = `${item.prefix ?? ""}${formatted}${item.suffix ?? ""}`;
          }

          return (
            <div
              key={item.label}
              style={{
                borderRadius: 9,
                border: "1px solid rgba(255, 255, 255, 0.08)",
                background: "rgba(9, 9, 11, 0.88)",
                padding: "10px 12px",
              }}
            >
              <div
                style={{
                  ...TABULAR_FONT_STYLE,
                  fontSize: 11,
                  fontWeight: 500,
                  color: "#A1A1AA",
                  marginBottom: 4,
                  whiteSpace: "nowrap",
                }}
              >
                {item.label}
              </div>
              <div
                style={{
                  ...TABULAR_FONT_STYLE,
                  fontSize: 17,
                  fontWeight: 600,
                  color: itemTone.accent,
                  whiteSpace: "nowrap",
                }}
              >
                {renderedValue}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
};
