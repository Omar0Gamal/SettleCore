import React from "react";
import { Easing, interpolate, useCurrentFrame } from "remotion";
import { TABULAR_FONT_STYLE } from "../styles/typography";

export interface KineticTitleProps {
  eyebrow?: string;
  lines: string[];
  subtitle?: string;
  startFrame?: number;
  staggerFrames?: number;
  align?: "center" | "left";
  accentWords?: Record<string, string>;
  fontSize?: number;
}

const DEFAULT_ACCENT_WORDS: Record<string, string> = {
  "Sub-microsecond": "#38BDF8",
  "Reorg-safe": "#10B981",
  "0.183": "#38BDF8",
  "Merkle": "#10B981",
};

const SILKY_EASE = Easing.bezier(0.22, 1, 0.36, 1);

export const KineticTitle: React.FC<KineticTitleProps> = ({
  eyebrow,
  lines,
  subtitle,
  startFrame = 0,
  staggerFrames = 4,
  align = "center",
  accentWords = DEFAULT_ACCENT_WORDS,
  fontSize = 68,
}) => {
  const frame = useCurrentFrame();

  const eyebrowProgress = interpolate(frame - startFrame, [0, 32], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: SILKY_EASE,
  });

  let wordCounter = 0;

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        alignItems: align === "center" ? "center" : "flex-start",
        textAlign: align,
        gap: 18,
        ...TABULAR_FONT_STYLE,
      }}
    >
      {eyebrow ? (
        <div
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: 10,
            padding: "7px 16px",
            borderRadius: 999,
            border: "1px solid rgba(56, 189, 248, 0.28)",
            background: "rgba(17, 19, 24, 0.85)",
            color: "#38BDF8",
            ...TABULAR_FONT_STYLE,
            fontSize: 14,
            fontWeight: 600,
            letterSpacing: "0.08em",
            textTransform: "uppercase",
            opacity: eyebrowProgress,
            transform: `translateY(${interpolate(eyebrowProgress, [0, 1], [14, 0])}px)`,
            filter: `blur(${interpolate(eyebrowProgress, [0, 1], [5, 0])}px)`,
          }}
        >
          <span
            style={{
              width: 7,
              height: 7,
              borderRadius: "50%",
              background: "#10B981",
              boxShadow: "0 0 12px rgba(16, 185, 129, 0.85)",
            }}
          />
          {eyebrow}
        </div>
      ) : null}

      <div
        style={{
          display: "flex",
          flexDirection: "column",
          alignItems: align === "center" ? "center" : "flex-start",
          gap: 6,
        }}
      >
        {lines.map((line, lineIdx) => {
          const words = line.split(" ");
          return (
            <div
              key={`${lineIdx}-${line}`}
              style={{
                display: "flex",
                flexWrap: "wrap",
                justifyContent: align === "center" ? "center" : "flex-start",
                gap: "0 18px",
                fontSize,
                fontWeight: 700,
                lineHeight: 1.08,
                letterSpacing: "-0.035em",
                color: "#FAFAFA",
              }}
            >
              {words.map((word, wIdx) => {
                const idx = wordCounter++;
                const delay = startFrame + 6 + idx * staggerFrames;
                const progress = interpolate(frame - delay, [0, 34], [0, 1], {
                  extrapolateLeft: "clamp",
                  extrapolateRight: "clamp",
                  easing: SILKY_EASE,
                });

                const cleanWord = word.replace(/[.,!?]/g, "");
                const accent = accentWords[cleanWord];
                const opacity = progress;
                const y = interpolate(progress, [0, 1], [24, 0]);
                const blur = interpolate(progress, [0, 1], [8, 0]);

                return (
                  <span
                    key={`${lineIdx}-${wIdx}-${word}`}
                    style={{
                      display: "inline-block",
                      opacity,
                      transform: `translateY(${y}px)`,
                      filter: `blur(${blur}px)`,
                      color: accent ?? "#FAFAFA",
                      textShadow: accent ? `0 0 36px ${accent}33` : "none",
                    }}
                  >
                    {word}
                  </span>
                );
              })}
            </div>
          );
        })}
      </div>

      {subtitle ? (
        <div
          style={{
            maxWidth: 900,
            fontSize: 22,
            lineHeight: 1.5,
            fontWeight: 400,
            color: "#A1A1AA",
            letterSpacing: "-0.01em",
            ...TABULAR_FONT_STYLE,
            opacity: interpolate(
              frame - (startFrame + 6 + wordCounter * staggerFrames),
              [0, 34],
              [0, 1],
              {
                extrapolateLeft: "clamp",
                extrapolateRight: "clamp",
                easing: SILKY_EASE,
              }
            ),
            transform: `translateY(${interpolate(
              frame - (startFrame + 6 + wordCounter * staggerFrames),
              [0, 34],
              [14, 0],
              {
                extrapolateLeft: "clamp",
                extrapolateRight: "clamp",
                easing: SILKY_EASE,
              }
            )}px)`,
          }}
        >
          {subtitle}
        </div>
      ) : null}
    </div>
  );
};
