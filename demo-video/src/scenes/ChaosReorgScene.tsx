import React from "react";
import { AbsoluteFill } from "remotion";
import waypoints from "../../public/captures/waypoints.json";
import { AnchoredCaption } from "../components/AnchoredCaption";
import { ZoomViewport, type CameraKeyframe, type CursorKeyframe } from "../components/ZoomViewport";
import { TABULAR_FONT_STYLE } from "../styles/typography";

interface MeasuredControl {
  x: number;
  y: number;
  targetBox: { x: number; y: number; w: number; h: number };
}

const rawControls = waypoints.controls as Record<string, MeasuredControl | undefined>;

function ctrl(key: string, fallback: MeasuredControl): MeasuredControl {
  return rawControls[key] ?? fallback;
}

const c = {
  navTabChaos: ctrl("navTabChaos", {
    x: 0.29138,
    y: 0.02176,
    targetBox: { x: 0.24407, y: 0.0088, w: 0.09463, h: 0.02593 },
  }),
  chaosTriggerReorg: ctrl("chaosTriggerReorg", {
    x: 0.20859,
    y: 0.44363,
    targetBox: { x: 0.11458, y: 0.3787, w: 0.18802, h: 0.12986 },
  }),
  depositStatusBadge: ctrl("depositStatusBadge", {
    x: 0.54282,
    y: 0.57847,
    targetBox: { x: 0.47454, y: 0.56875, w: 0.13657, h: 0.01944 },
  }),
  navTabPor: ctrl("navTabPor", {
    x: 0.38253,
    y: 0.02176,
    targetBox: { x: 0.34078, y: 0.0088, w: 0.0835, h: 0.02593 },
  }),
  verifyPorBtn: ctrl("verifyPorBtn", {
    x: 0.1585,
    y: 0.65599,
    targetBox: { x: 0.11458, y: 0.64117, w: 0.08784, h: 0.02963 },
  }),
};

/**
 * Scene 3: ChaosReorgScene (global frames 1350..2174, 825 local frames = 22 beats @ 96 BPM)
 *
 * Fixes V2, V3, V4, V5, V9:
 * - Strictly keeps zoom at 1.00x (focusX: 0.5, focusY: 0.5) on BOTH Chaos & Systems Lab
 *   and Proof of Reserves so 100% of all Chaos cards, Block Fork Timeline, Multi-RPC Router,
 *   Reset Normal State, Custody Watchlist, and Merkle Sum Tree are visible with ZERO cropped edges (V3).
 * - Clicks `Chaos & Systems Lab` at local 38 (global 1388) -> step6-chaos-armed.png.
 * - Clicks `3-Block Reorg` at local 200 (global 1550) -> step7-chaos-reorg.png.
 * - Clicks `Proof of Reserves` at local 450 (global 1800, 30.0s) -> step8-por-unverified.png
 *   so at 00:00:31 (global 1860, local 510) Proof of Reserves is clearly UNVERIFIED before click (V2).
 * - Clicks `Verify Merkle Proof` at local 600 (global 1950, 32.5s) -> step9-por-verified.png.
 * - All AnchoredCaptions fade out completely by local 770 (55 frames before scene end) -> ZERO ghost callouts (V5).
 */
const CAMERA_KEYFRAMES: CameraKeyframe[] = [
  { frame: 0, zoom: 1.0, focusX: 0.5, focusY: 0.5 },
  { frame: 824, zoom: 1.0, focusX: 0.5, focusY: 0.5 },
];

const CURSOR_WAYPOINTS: CursorKeyframe[] = [
  { frame: 6, x: c.navTabChaos.x, y: c.navTabChaos.y },
  // Click 7: Chaos & Systems Lab top nav tab (global 1388 -> swap at local 40)
  {
    frame: 28,
    x: c.navTabChaos.x,
    y: c.navTabChaos.y,
    clickAtFrame: 40,
    tone: "sky",
    targetBox: c.navTabChaos.targetBox,
  },
  // Click 8: Trigger 3-Block Reorg (global 1550 -> swap at local 202)
  {
    frame: 185,
    x: c.chaosTriggerReorg.x,
    y: c.chaosTriggerReorg.y,
    clickAtFrame: 200,
    tone: "rose",
    targetBox: c.chaosTriggerReorg.targetBox,
  },
  // Sweep pointer over orphaned custody deposit badge
  { frame: 330, x: c.depositStatusBadge.x, y: c.depositStatusBadge.y },
  // Click 9: Proof of Reserves top nav tab (global 1800 = 30.0s -> step8-por-unverified at local 452)
  {
    frame: 438,
    x: c.navTabPor.x,
    y: c.navTabPor.y,
    clickAtFrame: 450,
    tone: "sky",
    targetBox: c.navTabPor.targetBox,
  },
  // Click 10: Verify Merkle Proof CTA (global 1950 = 32.5s -> step9-por-verified at local 602)
  {
    frame: 585,
    x: c.verifyPorBtn.x,
    y: c.verifyPorBtn.y,
    clickAtFrame: 600,
    tone: "emerald",
    targetBox: c.verifyPorBtn.targetBox,
  },
];

export const ChaosReorgScene: React.FC = () => {
  return (
    <AbsoluteFill
      style={{
        backgroundColor: "#0B0E11",
        ...TABULAR_FONT_STYLE,
      }}
    >
      <ZoomViewport
        containerWidth={1920}
        containerHeight={1080}
        keyframes={CAMERA_KEYFRAMES}
        cursorWaypoints={CURSOR_WAYPOINTS}
        captures={[
          { src: "captures/step5-lifecycle-open.png", fromFrame: 0 },
          { src: "captures/step6-chaos-armed.png", fromFrame: 42, fadeFrames: 3 },
          { src: "captures/step7-chaos-reorg.png", fromFrame: 202, fadeFrames: 3 },
          { src: "captures/step8-por-unverified.png", fromFrame: 452, fadeFrames: 3 },
          { src: "captures/step9-por-verified.png", fromFrame: 602, fadeFrames: 3 },
        ]}
      />

      {/* Compact Narrative Pill 1: Canonical 3/3 Confirmation (empty header gap left of Reset Normal State) */}
      <AnchoredCaption
        badge="3-CONFIRMATION FINALITY"
        text="Deposit #103 credited after 3 blocks"
        detail="#100..#103"
        tone="sky"
        startFrame={52}
        exitFrame={175}
        centerX
        style={{ top: 60, left: "64%" }}
      />

      {/* Compact Narrative Pill 2: 3-Block EVM Reorg Rollback */}
      <AnchoredCaption
        badge="3-BLOCK REORG ROLLBACK"
        text="Orphaned deposit reversed atomically"
        detail="rollbacks = 1"
        tone="rose"
        startFrame={210}
        exitFrame={415}
        centerX
        style={{ top: 60, left: "64%" }}
      />

      {/* Compact Narrative Pill 3: Unverified PoR Ready to Audit (visible at 00:00:31 in bottom center gap) */}
      <AnchoredCaption
        badge="MERKLE SUM TREE"
        text="Liabilities bound to root prior to on-chain check"
        detail="Ready to audit"
        tone="sky"
        startFrame={460}
        exitFrame={575}
        centerX
        style={{ bottom: 44, left: "50%" }}
      />

      {/* Compact Narrative Pill 4: Verified On-Chain Solvency (fades out >=55 frames before scene end) */}
      <AnchoredCaption
        badge="SOLVENCY VERIFIED"
        text="Vault reserves ≥ total user liabilities"
        detail="isSolvent = true"
        tone="emerald"
        startFrame={610}
        exitFrame={755}
        centerX
        style={{ bottom: 192, left: "50%" }}
      />
    </AbsoluteFill>
  );
};
