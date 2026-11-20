import React from "react";
import { AbsoluteFill, Easing, Img, interpolate, staticFile, useCurrentFrame } from "remotion";
import { KineticTitle } from "../components/KineticTitle";
import { TABULAR_FONT_STYLE } from "../styles/typography";

const SILKY_EASE = Easing.bezier(0.22, 1, 0.36, 1);

/**
 * Scene 1: IntroScene (global frames 0..374, 375 frames = 10 beats @ 96 BPM)
 *
 * Fixes V7 (Cold Open Over Live Blurred Exchange, Zero Double Header):
 * - From frame 0, renders the 4K `step1-desk.png` live exchange full-bleed behind
 *   a cinematic blur(14px) brightness(0.42) glass veil + subtle 60fps ambient drift,
 *   so frame 2s (frame 120) is rich and atmospheric, never a dead black void.
 * - Counts up the hero latency metric (`0.183 µs median match`).
 * - At frames 115..185, the headline lifts and fades while the blur smoothly resolves
 *   from 14px -> 0px and brightness(0.42 -> 1.0) directly into the 1920x1080 live terminal
 *   with ZERO redundant outer BrowserFrame3D chrome.
 * - Holds sharp 1.00x live terminal from 185..374 for a seamless handoff at frame 375.
 */
export const IntroScene: React.FC = () => {
  const frame = useCurrentFrame();

  const blurPx = interpolate(frame, [115, 185], [14, 0], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: SILKY_EASE,
  });

  const brightness = interpolate(frame, [115, 185], [0.42, 1.0], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: SILKY_EASE,
  });

  const bgScale = interpolate(frame, [0, 115, 185], [1.035, 1.02, 1.0], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: SILKY_EASE,
  });

  const titleLift = interpolate(frame, [115, 178], [0, -110], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: SILKY_EASE,
  });

  const titleScale = interpolate(frame, [115, 178], [1, 0.92], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: SILKY_EASE,
  });

  const titleOpacity = interpolate(frame, [115, 172], [1, 0], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: SILKY_EASE,
  });

  const countProgress = interpolate(frame, [18, 76], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: SILKY_EASE,
  });
  const medianUs = (0.183 * countProgress).toFixed(3);

  const badgeOpacity =
    interpolate(frame, [14, 38], [0, 1], {
      extrapolateLeft: "clamp",
      extrapolateRight: "clamp",
      easing: SILKY_EASE,
    }) * titleOpacity;

  return (
    <AbsoluteFill
      style={{
        backgroundColor: "#0B0E11",
        alignItems: "center",
        justifyContent: "center",
        overflow: "hidden",
        ...TABULAR_FONT_STYLE,
      }}
    >
      {/* Full-bleed 4K live exchange resolving from blurred glass backdrop into 1.0x UI */}
      <Img
        src={staticFile("captures/step1-desk.png")}
        style={{
          position: "absolute",
          inset: 0,
          width: 1920,
          height: 1080,
          objectFit: "fill",
          transform: `scale(${bgScale})`,
          transformOrigin: "center center",
          filter: blurPx > 0.05 ? `blur(${blurPx.toFixed(2)}px) brightness(${brightness.toFixed(3)})` : `brightness(${brightness.toFixed(3)})`,
        }}
      />

      {/* Subtle atmospheric vignette while headline is visible */}
      <div
        style={{
          position: "absolute",
          inset: 0,
          background:
            "radial-gradient(ellipse 72% 58% at 50% 48%, rgba(56, 189, 248, 0.12), rgba(9, 11, 15, 0.55) 75%)",
          opacity: titleOpacity,
          pointerEvents: "none",
        }}
      />

      {/* Kinetic Headline Hero + Live Count-Up Latency Pill */}
      {titleOpacity > 0.005 ? (
        <div
          style={{
            position: "relative",
            zIndex: 20,
            display: "flex",
            flexDirection: "column",
            alignItems: "center",
            gap: 28,
            transform: `translateY(${titleLift}px) scale(${titleScale})`,
            opacity: titleOpacity,
            padding: "0 80px",
          }}
        >
          <KineticTitle
            eyebrow="SETTLECORE • INSTITUTIONAL HYBRID EXCHANGE"
            lines={["Sub-microsecond matching.", "Reorg-safe EVM settlement."]}
            subtitle="Deterministic C++17 CLOB paired with Go double-entry custody & Solidity EscrowVault."
            startFrame={4}
            staggerFrames={4}
            fontSize={70}
          />

          <div
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 14,
              padding: "10px 22px",
              borderRadius: 999,
              border: "1px solid rgba(56, 189, 248, 0.35)",
              background: "rgba(11, 14, 17, 0.86)",
              backdropFilter: "blur(14px)",
              boxShadow: "0 16px 40px rgba(0, 0, 0, 0.55)",
              opacity: badgeOpacity,
              ...TABULAR_FONT_STYLE,
            }}
          >
            <span
              style={{
                width: 8,
                height: 8,
                borderRadius: "50%",
                background: "#0ECB81",
                boxShadow: "0 0 12px #0ECB81",
              }}
            />
            <span style={{ fontSize: 18, fontWeight: 700, color: "#38BDF8" }}>
              {medianUs} µs median match
            </span>
            <span style={{ width: 1, height: 16, background: "rgba(255,255,255,0.14)" }} />
            <span style={{ fontSize: 14, fontWeight: 500, color: "#D4D4D8" }}>
              1,000,000 orders • 4.90M ops/sec • Zero float64
            </span>
          </div>
        </div>
      ) : null}
    </AbsoluteFill>
  );
};
