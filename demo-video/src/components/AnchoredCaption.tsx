import React from "react";
import { Easing, interpolate, useCurrentFrame } from "remotion";
import { TABULAR_FONT_STYLE } from "../styles/typography";

export type CaptionTone = "sky" | "emerald" | "rose" | "neutral";

export interface AnchoredCaptionProps {
  badge: string;
  text: string;
  detail?: string;
  startFrame: number;
  exitFrame: number;
  tone?: CaptionTone;
  centerX?: boolean;
  style?: React.CSSProperties;
}

const TONE_PALETTE: Record<CaptionTone, { border: string; accent: string; bgGlow: string }> = {
  sky: {
    border: "rgba(56, 189, 248, 0.42)",
    accent: "#38BDF8",
    bgGlow: "rgba(56, 189, 248, 0.14)",
  },
  emerald: {
    border: "rgba(14, 203, 129, 0.44)",
    accent: "#0ECB81",
    bgGlow: "rgba(14, 203, 129, 0.14)",
  },
  rose: {
    border: "rgba(246, 70, 93, 0.46)",
    accent: "#F6465D",
    bgGlow: "rgba(246, 70, 93, 0.16)",
  },
  neutral: {
    border: "rgba(255, 255, 255, 0.16)",
    accent: "#EAECEF",
    bgGlow: "rgba(255, 255, 255, 0.06)",
  },
};

const SILKY_EASE = Easing.bezier(0.22, 1, 0.36, 1);

/**
 * Compact single-line floating narrative pill (max height 40px) docked in
 * guaranteed non-occluding dead space. Strictly disappears after `exitFrame`
 * so zero ghost callouts ever bleed into scene transitions.
 */
export const AnchoredCaption: React.FC<AnchoredCaptionProps> = ({
  badge,
  text,
  detail,
  startFrame,
  exitFrame,
  tone = "sky",
  centerX = false,
  style,
}) => {
  const frame = useCurrentFrame();

  const fadeOutLen = 18;
  if (frame < startFrame || frame >= exitFrame + fadeOutLen) {
    return null;
  }

  const enterProgress = interpolate(frame - startFrame, [0, 20], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: SILKY_EASE,
  });

  const exitProgress = interpolate(frame - exitFrame, [0, fadeOutLen], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: SILKY_EASE,
  });

  const opacity = enterProgress * (1 - exitProgress);
  if (opacity <= 0.005) return null;

  const translateY =
    interpolate(enterProgress, [0, 1], [10, 0]) +
    interpolate(exitProgress, [0, 1], [0, -8]);

  const palette = TONE_PALETTE[tone];

  return (
    <div
      style={{
        position: "absolute",
        display: "inline-flex",
        alignItems: "center",
        gap: 10,
        height: 38,
        maxHeight: 42,
        padding: "0 16px",
        borderRadius: 999,
        border: `1px solid ${palette.border}`,
        background: "rgba(11, 14, 17, 0.9)",
        backdropFilter: "blur(14px)",
        boxShadow: `0 12px 32px rgba(0, 0, 0, 0.6), 0 0 24px ${palette.bgGlow}`,
        opacity,
        transform: `translate(${centerX ? "-50%" : "0px"}, ${translateY}px)`,
        whiteSpace: "nowrap",
        pointerEvents: "none",
        zIndex: 60,
        ...TABULAR_FONT_STYLE,
        ...style,
      }}
    >
      <span
        style={{
          width: 7,
          height: 7,
          borderRadius: "50%",
          background: palette.accent,
          boxShadow: `0 0 10px ${palette.accent}`,
          flexShrink: 0,
        }}
      />
      <span
        style={{
          fontSize: 11,
          fontWeight: 700,
          letterSpacing: "0.06em",
          textTransform: "uppercase",
          color: palette.accent,
        }}
      >
        {badge}
      </span>
      <span style={{ width: 1, height: 14, background: "rgba(255, 255, 255, 0.14)" }} />
      <span
        style={{
          fontSize: 13,
          fontWeight: 500,
          color: "#F4F4F5",
        }}
      >
        {text}
      </span>
      {detail ? (
        <>
          <span style={{ width: 1, height: 14, background: "rgba(255, 255, 255, 0.14)" }} />
          <span
            style={{
              fontSize: 12,
              fontWeight: 600,
              color: "#A1A1AA",
            }}
          >
            {detail}
          </span>
        </>
      ) : null}
    </div>
  );
};
