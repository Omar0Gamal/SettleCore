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
  orderPriceInput: ctrl("orderPriceInput", {
    x: 0.86361,
    y: 0.24907,
    targetBox: { x: 0.77382, y: 0.23426, w: 0.17958, h: 0.02963 },
  }),
  orderQtyInput: ctrl("orderQtyInput", {
    x: 0.86448,
    y: 0.31065,
    targetBox: { x: 0.75715, y: 0.29583, w: 0.21465, h: 0.02963 },
  }),
  submitOrderBtn: ctrl("submitOrderBtn", {
    x: 0.87519,
    y: 0.52778,
    targetBox: { x: 0.75663, y: 0.51111, w: 0.23711, h: 0.03333 },
  }),
  orderTypeMarket: ctrl("orderTypeMarket", {
    x: 0.78771,
    y: 0.18333,
    targetBox: { x: 0.77725, y: 0.17407, w: 0.02092, h: 0.01852 },
  }),
  submitOrderBtnMarket: ctrl("submitOrderBtnMarket", {
    x: 0.87519,
    y: 0.52546,
    targetBox: { x: 0.75663, y: 0.5088, w: 0.23711, h: 0.03333 },
  }),
  navTabChaos: ctrl("navTabChaos", {
    x: 0.29138,
    y: 0.02176,
    targetBox: { x: 0.24407, y: 0.0088, w: 0.09463, h: 0.02593 },
  }),
};

const inspectLifecycleControl: MeasuredControl = ctrl("inspectLifecycleBtn", {
  x: 0.87,
  y: 0.62,
  targetBox: { x: 0.77, y: 0.602, w: 0.2, h: 0.032 },
});

/**
 * Scene 2: OrderExecutionScene (global frames 375..1349, 975 local frames = 26 beats @ 96 BPM)
 *
 * Fixes V1, V3, V4, V5, V9:
 * - Zoom capped at 1.08x max centered cleanly around Order Ticket & Book so the
 *   top-left ETH/USDC price ticker and chart are NEVER cropped out (V3).
 * - Returns to exact 1.00x (focusX: 0.5, focusY: 0.5) BEFORE opening the Order Lifecycle
 *   Drawer so 100% of the 5-stage microsecond pipeline drawer is visible.
 * - Click-synchronized 4K swaps (step1-desk -> step2-price-focused -> step3-limit-filled
 *   -> step4-market-filled -> step5-lifecycle-open) at clickAtFrame + 2.
 * - Single-line AnchoredCaption pills docked at top: 56px (never covering blotter rows),
 *   fading out completely by frame 915 (60 frames before scene end -> zero ghost callouts).
 */
const CAMERA_KEYFRAMES: CameraKeyframe[] = [
  { frame: 0, zoom: 1.0, focusX: 0.5, focusY: 0.5 },
  { frame: 974, zoom: 1.0, focusX: 0.5, focusY: 0.5 },
];

const CURSOR_WAYPOINTS: CursorKeyframe[] = [
  { frame: 30, x: 0.72, y: 0.28 },
  // Click 1: Focus Price Input & type 3125.50 (global 505 -> local 130)
  {
    frame: 120,
    x: c.orderPriceInput.x,
    y: c.orderPriceInput.y,
    clickAtFrame: 130,
    tone: "sky",
    targetBox: c.orderPriceInput.targetBox,
  },
  // Click 2: Focus Qty Input & type 2.50 (global 570 -> local 195 -> step2-price-focused at 197)
  {
    frame: 185,
    x: c.orderQtyInput.x,
    y: c.orderQtyInput.y,
    clickAtFrame: 195,
    tone: "sky",
    targetBox: c.orderQtyInput.targetBox,
  },
  // Click 3: Submit LIMIT BUY 2.50 ETH @ 3,125.50 USDC (global 675 -> local 300 -> step3-limit-filled at 302)
  {
    frame: 288,
    x: c.submitOrderBtn.x,
    y: c.submitOrderBtn.y,
    clickAtFrame: 300,
    tone: "emerald",
    targetBox: c.submitOrderBtn.targetBox,
  },
  // Click 4: Switch to Market tab (global 780 -> local 405)
  {
    frame: 392,
    x: c.orderTypeMarket.x,
    y: c.orderTypeMarket.y,
    clickAtFrame: 405,
    tone: "sky",
    targetBox: c.orderTypeMarket.targetBox,
  },
  // Click 5: Submit MARKET BUY 0.75 ETH (global 855 -> local 480 -> step4-market-filled at 482)
  {
    frame: 468,
    x: c.submitOrderBtnMarket.x,
    y: c.submitOrderBtnMarket.y,
    clickAtFrame: 480,
    tone: "emerald",
    targetBox: c.submitOrderBtnMarket.targetBox,
  },
  // Pan smoothly across Candlestick chart & Order Book
  { frame: 600, x: 0.45, y: 0.44 },
  // Click 6: Open 2-Phase Order Lifecycle Inspector Drawer (global 1110 -> local 735 -> step5-lifecycle-open at 737)
  {
    frame: 722,
    x: inspectLifecycleControl.x,
    y: inspectLifecycleControl.y,
    clickAtFrame: 735,
    tone: "sky",
    targetBox: inspectLifecycleControl.targetBox,
  },
  // Glide cleanly toward Chaos & Systems Lab nav tab for Scene 3 handoff
  { frame: 920, x: c.navTabChaos.x, y: c.navTabChaos.y },
];

export const OrderExecutionScene: React.FC = () => {
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
          { src: "captures/step1-desk.png", fromFrame: 0 },
          { src: "captures/step2-price-focused.png", fromFrame: 197, fadeFrames: 3 },
          { src: "captures/step3-limit-filled.png", fromFrame: 302, fadeFrames: 3 },
          { src: "captures/step4-market-filled.png", fromFrame: 482, fadeFrames: 3 },
          { src: "captures/step5-lifecycle-open.png", fromFrame: 737, fadeFrames: 3 },
        ]}
      />

      {/* Compact Narrative Pill 1: Fixed-point Limit & Market execution (in empty upper-left chart space) */}
      <AnchoredCaption
        badge="PRE-TRADE RESERVE → CLOB"
        text="Fixed-point int64 (1e8) order entry & instant fill"
        detail="0.183 µs p50"
        tone="emerald"
        startFrame={190}
        exitFrame={530}
        style={{ top: 172, left: 32 }}
      />

      {/* Compact Narrative Pill 2: 2-Phase Order Lifecycle Inspector (left of open drawer) */}
      <AnchoredCaption
        badge="2-PHASE LIFECYCLE"
        text="PG reserve → CRC32C WAL → C++17 match → ledger"
        detail="Zero float64"
        tone="sky"
        startFrame={735}
        exitFrame={905}
        style={{ bottom: 48, left: 32 }}
      />
    </AbsoluteFill>
  );
};
