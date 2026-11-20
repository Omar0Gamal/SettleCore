import React from "react";
import { AbsoluteFill, Audio, Easing, Sequence, interpolate, staticFile, useCurrentFrame } from "remotion";
import { BenchmarksScene } from "./scenes/BenchmarksScene";
import { ChaosReorgScene } from "./scenes/ChaosReorgScene";
import { IntroScene } from "./scenes/IntroScene";
import { OrderExecutionScene } from "./scenes/OrderExecutionScene";
import { OutroScene } from "./scenes/OutroScene";
import { TABULAR_FONT_STYLE } from "./styles/typography";

const TRANSITION_EASE = Easing.bezier(0.22, 1, 0.36, 1);

interface SceneTransitionProps {
  durationInFrames: number;
  enterFrames?: number;
  exitFrames?: number;
  skipEnter?: boolean;
  skipExit?: boolean;
  children: React.ReactNode;
}

const SceneTransition: React.FC<SceneTransitionProps> = ({
  durationInFrames,
  enterFrames = 8,
  exitFrames = 8,
  skipEnter = false,
  skipExit = false,
  children,
}) => {
  const frame = useCurrentFrame();

  const enterProgress = skipEnter
    ? 1
    : interpolate(frame, [0, enterFrames], [0, 1], {
        extrapolateLeft: "clamp",
        extrapolateRight: "clamp",
        easing: TRANSITION_EASE,
      });

  const exitProgress = skipExit
    ? 0
    : interpolate(frame, [durationInFrames - exitFrames, durationInFrames], [0, 1], {
        extrapolateLeft: "clamp",
        extrapolateRight: "clamp",
        easing: TRANSITION_EASE,
      });

  const opacity = enterProgress * (1 - exitProgress);

  return <AbsoluteFill style={{ opacity }}>{children}</AbsoluteFill>;
};

interface ChapterItem {
  id: string;
  label: string;
  start: number;
  end: number;
  accent: string;
}

const CHAPTERS: ChapterItem[] = [
  { id: "01", label: "01 EXECUTION", start: 375, end: 1350, accent: "#0ECB81" },
  { id: "02", label: "02 3-BLOCK REORG", start: 1350, end: 1800, accent: "#F6465D" },
  { id: "03", label: "03 MERKLE POR", start: 1800, end: 2175, accent: "#0ECB81" },
  { id: "04", label: "04 BENCHMARKS", start: 2175, end: 2700, accent: "#38BDF8" },
];

/**
 * Fixes V6 (Zero Footer-Covering Chapter Pill):
 * - 2px Top-Edge Accent Progress Line along the top edge (y=0)
 * - Compact Top-Center Chapter Badge docked inside the 48px app header gap (top: 9px),
 *   leaving the bottom TelemetryStrip and all table rows 100% uncovered!
 */
const TopProgressChrome: React.FC = () => {
  const frame = useCurrentFrame();

  const activeChapter =
    CHAPTERS.find((ch) => frame >= ch.start && frame < ch.end) ?? CHAPTERS[CHAPTERS.length - 1]!;

  const progressPct = Math.min(100, Math.max(0, (frame / 3000) * 100));

  // Deterministic 60fps sub-microsecond latency jitter (0.175 .. 0.194 µs)
  const wave = Math.sin(frame * 0.21) * 0.6 + Math.cos(frame * 0.37) * 0.4;
  const liveLatencyUs = (0.183 + wave * 0.008).toFixed(3);

  // Show header-docked badge only during UI product chapters (01..03) where 48px header gap exists
  const badgeOpacity =
    frame < 365 || frame > 2175
      ? 0
      : interpolate(frame, [365, 390, 2145, 2175], [0, 1, 1, 0], {
          extrapolateLeft: "clamp",
          extrapolateRight: "clamp",
        });

  return (
    <>
      {/* 2px Top-Edge Accent Progress Line */}
      <div
        style={{
          position: "absolute",
          top: 0,
          left: 0,
          right: 0,
          height: 2,
          background: "rgba(255, 255, 255, 0.06)",
          zIndex: 110,
          pointerEvents: "none",
        }}
      >
        <div
          style={{
            height: "100%",
            width: `${progressPct}%`,
            background: `linear-gradient(90deg, #38BDF8, ${activeChapter.accent})`,
            boxShadow: `0 0 10px ${activeChapter.accent}`,
          }}
        />
      </div>

      {/* Compact Top-Center Chapter Badge docked in 48px header empty center gap (x=40.5%..52.5%) */}
      {badgeOpacity > 0.01 ? (
        <div
          style={{
            position: "absolute",
            top: 10,
            left: "46.6%",
            transform: "translateX(-50%)",
            height: 28,
            display: "inline-flex",
            alignItems: "center",
            gap: 7,
            padding: "0 11px",
            borderRadius: 999,
            border: `1px solid ${activeChapter.accent}55`,
            background: "rgba(11, 14, 17, 0.92)",
            backdropFilter: "blur(10px)",
            boxShadow: "0 6px 20px rgba(0, 0, 0, 0.45)",
            opacity: badgeOpacity,
            zIndex: 110,
            pointerEvents: "none",
            whiteSpace: "nowrap",
            ...TABULAR_FONT_STYLE,
          }}
        >
          <span
            style={{
              width: 6,
              height: 6,
              borderRadius: "50%",
              background: activeChapter.accent,
              boxShadow: `0 0 8px ${activeChapter.accent}`,
            }}
          />
          <span
            style={{
              fontSize: 10,
              fontWeight: 700,
              letterSpacing: "0.05em",
              color: "#FAFAFA",
            }}
          >
            {activeChapter.label}
          </span>
          <span style={{ width: 1, height: 11, background: "rgba(255,255,255,0.14)" }} />
          <span
            style={{
              fontSize: 10,
              fontWeight: 600,
              color: "#38BDF8",
            }}
          >
            {liveLatencyUs} µs
          </span>
        </div>
      ) : null}
    </>
  );
};

/**
 * SettleCoreLaunch Master Timeline (3,000 frames @ 60fps = 50.0 seconds = 80 beats @ 96 BPM)
 *
 * Beat-grid aligned (37.5 frames/beat) with clean opacity handoffs and ZERO scale-through
 * double exposure between workspaces:
 * - IntroScene          : 0..374    (10 beats: blurred cold open -> crisp 1.0x live terminal)
 * - OrderExecutionScene : 375..1349 (26 beats: limit, market & 2-phase lifecycle drawer)
 * - ChaosReorgScene     : 1350..2174 (22 beats: 8-frame clean cut at zoom 1.0 -> reorg -> unverified PoR at 31s -> verified PoR)
 * - BenchmarksScene     : 2175..2699 (14 beats: full-height 4-mode WAL + E2E latency waterfall)
 * - OutroScene          : 2700..2999 (8 beats: architecture topology + docker compose up --build)
 */
export const SettleCoreLaunch: React.FC = () => {
  return (
    <AbsoluteFill style={{ backgroundColor: "#0B0E11", ...TABULAR_FONT_STYLE }}>
      <style>{`
        @import url('https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800&display=swap');
        * {
          font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
          font-variant-numeric: tabular-nums;
          font-feature-settings: "tnum" 1, "cv01" 1, "cv11" 1, "zero" 0;
        }
      `}</style>

      {/* Frame-synced 48 kHz stereo WAV soundtrack */}
      <Audio src={staticFile("audio/settlecore-soundtrack.wav")} />

      <Sequence from={0} durationInFrames={375} name="IntroScene">
        <SceneTransition durationInFrames={375} skipEnter skipExit>
          <IntroScene />
        </SceneTransition>
      </Sequence>

      <Sequence from={375} durationInFrames={975} name="OrderExecutionScene">
        <SceneTransition durationInFrames={975} skipEnter skipExit>
          <OrderExecutionScene />
        </SceneTransition>
      </Sequence>

      <Sequence from={1350} durationInFrames={825} name="ChaosReorgScene">
        <SceneTransition durationInFrames={825} skipEnter exitFrames={10}>
          <ChaosReorgScene />
        </SceneTransition>
      </Sequence>

      <Sequence from={2175} durationInFrames={525} name="BenchmarksScene">
        <SceneTransition durationInFrames={525} enterFrames={10} exitFrames={10}>
          <BenchmarksScene />
        </SceneTransition>
      </Sequence>

      <Sequence from={2700} durationInFrames={300} name="OutroScene">
        <SceneTransition durationInFrames={300} enterFrames={10} skipExit>
          <OutroScene />
        </SceneTransition>
      </Sequence>

      <TopProgressChrome />
    </AbsoluteFill>
  );
};
