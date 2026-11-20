import React, { useState } from "react";
import { Easing, Img, interpolate, staticFile, useCurrentFrame } from "remotion";
import { TABULAR_FONT_STYLE } from "../styles/typography";

export type CursorTone = "emerald" | "rose" | "sky";

export interface TargetBox {
  /** Normalized [0..1] bounds relative to 1920x1080 capture */
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface CameraKeyframe {
  frame: number;
  zoom: number;
  /** Normalized horizontal focus [0..1] */
  focusX: number;
  /** Normalized vertical focus [0..1] */
  focusY: number;
}

export interface CaptureLayer {
  /** Path relative to public/, e.g. "captures/step1-desk.png" */
  src: string;
  /** Scene-local frame when this screenshot swaps in */
  fromFrame: number;
  fadeFrames?: number;
}

export interface CursorKeyframe {
  frame: number;
  /** Normalized X [0..1] on 1920x1080 canvas */
  x: number;
  /** Normalized Y [0..1] on 1920x1080 canvas */
  y: number;
  clickAtFrame?: number;
  tone?: CursorTone;
  targetBox?: TargetBox;
}

export interface ZoomViewportProps {
  keyframes: CameraKeyframe[];
  captures: CaptureLayer[];
  cursorWaypoints?: CursorKeyframe[];
  containerWidth?: number;
  containerHeight?: number;
  showLiveHud?: boolean;
  overlayChildren?: React.ReactNode;
}

const CAMERA_EASE = Easing.bezier(0.25, 0.1, 0.25, 1.0);
const CURSOR_EASE = Easing.bezier(0.22, 1, 0.36, 1);
const SHOCKWAVE_EASE = Easing.bezier(0.16, 1, 0.3, 1);

const TONE_PALETTE: Record<CursorTone, { hex: string; soft: string; glow: string }> = {
  emerald: {
    hex: "#0ECB81",
    soft: "rgba(14, 203, 129, 0.22)",
    glow: "rgba(14, 203, 129, 0.58)",
  },
  rose: {
    hex: "#F6465D",
    soft: "rgba(246, 70, 93, 0.24)",
    glow: "rgba(246, 70, 93, 0.62)",
  },
  sky: {
    hex: "#38BDF8",
    soft: "rgba(56, 189, 248, 0.22)",
    glow: "rgba(56, 189, 248, 0.58)",
  },
};

function interpolateCamera(
  keyframes: CameraKeyframe[],
  frame: number
): { zoom: number; focusX: number; focusY: number } {
  if (keyframes.length === 0) {
    return { zoom: 1, focusX: 0.5, focusY: 0.5 };
  }
  const first = keyframes[0]!;
  if (frame <= first.frame || keyframes.length === 1) {
    return { zoom: first.zoom, focusX: first.focusX, focusY: first.focusY };
  }

  const last = keyframes[keyframes.length - 1]!;
  if (frame >= last.frame) {
    return { zoom: last.zoom, focusX: last.focusX, focusY: last.focusY };
  }

  for (let i = 1; i < keyframes.length; i++) {
    const prev = keyframes[i - 1]!;
    const curr = keyframes[i]!;
    if (frame <= curr.frame) {
      const span = Math.max(1, curr.frame - prev.frame);
      const rawT = Math.min(1, Math.max(0, (frame - prev.frame) / span));
      const eased = CAMERA_EASE(rawT);
      return {
        zoom: prev.zoom + (curr.zoom - prev.zoom) * eased,
        focusX: prev.focusX + (curr.focusX - prev.focusX) * eased,
        focusY: prev.focusY + (curr.focusY - prev.focusY) * eased,
      };
    }
  }

  return { zoom: last.zoom, focusX: last.focusX, focusY: last.focusY };
}

interface ActiveClickState {
  clickAtFrame: number;
  localFrame: number;
  tone: CursorTone;
  targetBox?: TargetBox;
  xNorm: number;
  yNorm: number;
}

function interpolateCursor(
  waypoints: CursorKeyframe[],
  frame: number
): {
  xNorm: number;
  yNorm: number;
  visible: boolean;
  activeClicks: ActiveClickState[];
  pointerScale: number;
  pointerRotateDeg: number;
} {
  if (waypoints.length === 0) {
    return {
      xNorm: 0.5,
      yNorm: 0.5,
      visible: false,
      activeClicks: [],
      pointerScale: 1,
      pointerRotateDeg: 0,
    };
  }

  const first = waypoints[0]!;
  let xNorm = first.x;
  let yNorm = first.y;

  if (frame > first.frame && waypoints.length > 1) {
    const last = waypoints[waypoints.length - 1]!;
    if (frame >= last.frame) {
      xNorm = last.x;
      yNorm = last.y;
    } else {
      for (let i = 1; i < waypoints.length; i++) {
        const prev = waypoints[i - 1]!;
        const curr = waypoints[i]!;
        if (frame <= curr.frame) {
          const postClickFrame = prev.clickAtFrame !== undefined ? prev.clickAtFrame + 14 : prev.frame;
          const naturalStart = Math.max(prev.frame, postClickFrame);
          const windowLen = Math.min(54, Math.max(24, curr.frame - naturalStart));
          const moveStart = Math.max(prev.frame, curr.frame - windowLen);

          if (frame <= moveStart) {
            xNorm = prev.x;
            yNorm = prev.y;
          } else {
            const rawT = Math.min(1, Math.max(0, (frame - moveStart) / Math.max(1, curr.frame - moveStart)));
            const eased = CURSOR_EASE(rawT);

            const dx = curr.x - prev.x;
            const dy = curr.y - prev.y;
            const dist = Math.hypot(dx, dy);

            let arcX = 0;
            let arcY = 0;
            if (dist > 0.01) {
              const nx = -dy / dist;
              const ny = dx / dist;
              const direction = i % 2 === 0 ? 1 : -1;
              const arcAmp = Math.min(0.016, dist * 0.11) * direction;
              const arcEnvelope = Math.sin(eased * Math.PI);
              arcX = nx * arcEnvelope * arcAmp;
              arcY = ny * arcEnvelope * arcAmp;
            }

            xNorm = prev.x + dx * eased + arcX;
            yNorm = prev.y + dy * eased + arcY;
          }
          break;
        }
      }
    }
  }

  const activeClicks: ActiveClickState[] = [];
  let pointerScale = 1;
  let pointerRotateDeg = 0;

  for (const wp of waypoints) {
    if (wp.clickAtFrame === undefined) continue;
    const local = frame - wp.clickAtFrame;
    if (local >= -10 && local <= 28) {
      activeClicks.push({
        clickAtFrame: wp.clickAtFrame,
        localFrame: local,
        tone: wp.tone ?? "sky",
        targetBox: wp.targetBox,
        xNorm: wp.x,
        yNorm: wp.y,
      });
    }
    if (local >= -4 && local <= 14) {
      if (local <= 0) {
        pointerScale = interpolate(local, [-4, 0], [1, 0.84], {
          extrapolateLeft: "clamp",
          extrapolateRight: "clamp",
          easing: CURSOR_EASE,
        });
        pointerRotateDeg = interpolate(local, [-4, 0], [0, -4], {
          extrapolateLeft: "clamp",
          extrapolateRight: "clamp",
        });
      } else if (local <= 6) {
        pointerScale = interpolate(local, [0, 6], [0.84, 1.06], {
          extrapolateLeft: "clamp",
          extrapolateRight: "clamp",
          easing: CURSOR_EASE,
        });
        pointerRotateDeg = interpolate(local, [0, 6], [-4, 1], {
          extrapolateLeft: "clamp",
          extrapolateRight: "clamp",
        });
      } else {
        pointerScale = interpolate(local, [6, 14], [1.06, 1.0], {
          extrapolateLeft: "clamp",
          extrapolateRight: "clamp",
          easing: CURSOR_EASE,
        });
        pointerRotateDeg = interpolate(local, [6, 14], [1, 0], {
          extrapolateLeft: "clamp",
          extrapolateRight: "clamp",
        });
      }
    }
  }

  return {
    xNorm,
    yNorm,
    visible: frame >= first.frame,
    activeClicks,
    pointerScale,
    pointerRotateDeg,
  };
}

const SafeCaptureImg: React.FC<{
  src: string;
  opacity: number;
  width: number;
  height: number;
  left: number;
  top: number;
}> = ({ src, opacity, width, height, left, top }) => {
  const [failed, setFailed] = useState(false);
  if (failed) return null;

  return (
    <Img
      src={staticFile(src)}
      onError={() => setFailed(true)}
      style={{
        position: "absolute",
        left,
        top,
        width,
        height,
        objectFit: "fill",
        opacity,
      }}
    />
  );
};

/**
 * Subtle 60fps Live Micro-Motion Layer (Fixes V1):
 * Keeps the terminal visibly breathing at 60fps even between discrete clicks
 * via a soft order-book depth breath, top-header hairline shimmer, and
 * a crisp live 60fps frame-tick pulse line so no frame is ever frozen.
 */
const LiveMicroMotionHud: React.FC<{
  frame: number;
}> = ({ frame }) => {
  const pulseAlpha = 0.03 + 0.025 * (0.5 + 0.5 * Math.sin(frame * 0.12));
  const scanX = ((frame * 3.2) % 140) - 20; // gentle 60fps shimmer sweep
  const bookBreathY = 32 + 10 * Math.sin(frame * 0.08);

  return (
    <>
      {/* Subtle 60fps top-of-book & market stream breathing highlight */}
      <div
        style={{
          position: "absolute",
          inset: 0,
          background: `radial-gradient(52% 32% at 64% ${bookBreathY.toFixed(1)}%, rgba(14, 203, 129, ${pulseAlpha.toFixed(4)}), transparent 72%)`,
          pointerEvents: "none",
          zIndex: 20,
        }}
      />

      {/* Subtle 60fps top-hairline shimmer across the exchange header */}
      <div
        style={{
          position: "absolute",
          top: 47,
          left: 0,
          right: 0,
          height: 1,
          background: `linear-gradient(90deg, transparent ${Math.max(0, scanX - 15)}%, rgba(56, 189, 248, 0.42) ${scanX}%, transparent ${Math.min(100, scanX + 15)}%)`,
          pointerEvents: "none",
          zIndex: 22,
        }}
      />

      {/* Subtle 60fps bottom-hairline data-stream pulse above TelemetryStrip */}
      <div
        style={{
          position: "absolute",
          bottom: 28,
          left: 0,
          right: 0,
          height: 1,
          background: `linear-gradient(90deg, transparent ${Math.max(0, 100 - scanX - 15)}%, rgba(14, 203, 129, 0.32) ${100 - scanX}%, transparent ${Math.min(100, 100 - scanX + 15)}%)`,
          pointerEvents: "none",
          zIndex: 22,
        }}
      />
    </>
  );
};

export const ZoomViewport: React.FC<ZoomViewportProps> = ({
  keyframes,
  captures,
  cursorWaypoints = [],
  containerWidth = 1920,
  containerHeight = 1080,
  showLiveHud = true,
  overlayChildren,
}) => {
  const frame = useCurrentFrame();

  const { zoom, focusX, focusY } = interpolateCamera(keyframes, frame);

  // Direct 4K -> target pixel sizing (zero CSS transform: scale() wrapper blur)
  const scaledW = Math.round(containerWidth * zoom);
  const scaledH = Math.round(containerHeight * zoom);

  const rawTx = Math.round(containerWidth / 2 - focusX * scaledW);
  const rawTy = Math.round(containerHeight / 2 - focusY * scaledH);

  // Strictly clamp viewport pan inside [container - scaled, 0] so no black borders ever appear
  const tx = Math.max(containerWidth - scaledW, Math.min(0, rawTx));
  const ty = Math.max(containerHeight - scaledH, Math.min(0, rawTy));

  const cursor = interpolateCursor(cursorWaypoints, frame);

  // Determine the latest fully opaque capture so we never stack redundant 4K layers
  let latestOpaqueIdx = 0;
  for (let i = 0; i < captures.length; i++) {
    const cap = captures[i]!;
    const fade = cap.fadeFrames ?? 3;
    if (frame >= cap.fromFrame + fade) {
      latestOpaqueIdx = i;
    }
  }

  return (
    <div
      style={{
        position: "relative",
        width: containerWidth,
        height: containerHeight,
        overflow: "hidden",
        backgroundColor: "#0B0E11",
        ...TABULAR_FONT_STYLE,
      }}
    >
      {/* Direct 4K <Img> layers sized directly to scaledW x scaledH */}
      {captures.map((cap, idx) => {
        if (idx < latestOpaqueIdx || frame < cap.fromFrame) return null;
        const fade = Math.max(1, cap.fadeFrames ?? 3);
        const opacity =
          idx === latestOpaqueIdx
            ? 1
            : interpolate(frame, [cap.fromFrame, cap.fromFrame + fade], [0, 1], {
                extrapolateLeft: "clamp",
                extrapolateRight: "clamp",
                easing: CAMERA_EASE,
              });
        if (opacity <= 0) return null;
        return (
          <SafeCaptureImg
            key={`${cap.src}-${cap.fromFrame}`}
            src={cap.src}
            opacity={opacity}
            width={scaledW}
            height={scaledH}
            left={tx}
            top={ty}
          />
        );
      })}

      {/* Synchronized UI-space overlay for 60fps micro-motion, target halos, shockwaves, and pointer */}
      <div
        style={{
          position: "absolute",
          left: tx,
          top: ty,
          width: scaledW,
          height: scaledH,
          pointerEvents: "none",
        }}
      >
        {showLiveHud ? <LiveMicroMotionHud frame={frame} /> : null}

        {overlayChildren}

        {/* 1. Target Button Magnetic Focus Halo (-10..+22 frames around clickAtFrame) */}
        {cursor.activeClicks.map((click) => {
          if (!click.targetBox || click.localFrame < -10 || click.localFrame > 22) {
            return null;
          }
          const palette = TONE_PALETTE[click.tone];
          const haloOpacity =
            click.localFrame <= 0
              ? interpolate(click.localFrame, [-10, -2, 0], [0, 0.85, 1], {
                  extrapolateLeft: "clamp",
                  extrapolateRight: "clamp",
                  easing: CURSOR_EASE,
                })
              : interpolate(click.localFrame, [0, 5, 22], [1, 0.9, 0], {
                  extrapolateLeft: "clamp",
                  extrapolateRight: "clamp",
                  easing: CURSOR_EASE,
                });
          const pad = 4;
          const left = click.targetBox.x * scaledW - pad;
          const top = click.targetBox.y * scaledH - pad;
          const width = click.targetBox.w * scaledW + pad * 2;
          const height = click.targetBox.h * scaledH + pad * 2;

          return (
            <div
              key={`halo-${click.clickAtFrame}`}
              style={{
                position: "absolute",
                left,
                top,
                width,
                height,
                borderRadius: 10,
                border: `2px solid ${palette.hex}`,
                background: `radial-gradient(circle at center, ${palette.soft}, transparent 75%)`,
                boxShadow: `0 0 24px ${palette.glow}, inset 0 0 14px ${palette.soft}`,
                opacity: haloOpacity,
                zIndex: 42,
              }}
            />
          );
        })}

        {/* 2. 3-Layer Concentric Click Shockwave (0..28 frames after clickAtFrame) */}
        {cursor.activeClicks.map((click) => {
          if (click.localFrame < 0 || click.localFrame > 28) return null;
          const palette = TONE_PALETTE[click.tone];
          const cx = click.xNorm * scaledW;
          const cy = click.yNorm * scaledH;

          const coreT = Math.min(1, click.localFrame / 10);
          const coreSize = interpolate(coreT, [0, 1], [8, 34], { easing: SHOCKWAVE_EASE });
          const coreOpacity =
            click.localFrame <= 10
              ? interpolate(click.localFrame, [0, 2, 10], [0.95, 1, 0], {
                  extrapolateLeft: "clamp",
                  extrapolateRight: "clamp",
                })
              : 0;

          const ring1T = Math.min(1, click.localFrame / 22);
          const ring1Size = interpolate(ring1T, [0, 1], [14, 78], { easing: SHOCKWAVE_EASE });
          const ring1Opacity =
            click.localFrame <= 22
              ? interpolate(click.localFrame, [0, 2, 22], [0.95, 0.9, 0], {
                  extrapolateLeft: "clamp",
                  extrapolateRight: "clamp",
                  easing: SHOCKWAVE_EASE,
                })
              : 0;

          const echoLocal = click.localFrame - 4;
          const ring2T = Math.max(0, Math.min(1, echoLocal / 24));
          const ring2Size = interpolate(ring2T, [0, 1], [20, 114], { easing: SHOCKWAVE_EASE });
          const ring2Opacity =
            echoLocal >= 0 && echoLocal <= 24
              ? interpolate(echoLocal, [0, 3, 24], [0.68, 0.58, 0], {
                  extrapolateLeft: "clamp",
                  extrapolateRight: "clamp",
                  easing: SHOCKWAVE_EASE,
                })
              : 0;

          return (
            <React.Fragment key={`shock-${click.clickAtFrame}`}>
              {coreOpacity > 0 ? (
                <div
                  style={{
                    position: "absolute",
                    left: cx - coreSize / 2,
                    top: cy - coreSize / 2,
                    width: coreSize,
                    height: coreSize,
                    borderRadius: "50%",
                    background: `radial-gradient(circle, #FFFFFF 0%, ${palette.hex} 55%, transparent 100%)`,
                    opacity: coreOpacity,
                    zIndex: 45,
                  }}
                />
              ) : null}

              {ring1Opacity > 0 ? (
                <div
                  style={{
                    position: "absolute",
                    left: cx - ring1Size / 2,
                    top: cy - ring1Size / 2,
                    width: ring1Size,
                    height: ring1Size,
                    borderRadius: "50%",
                    border: `2px solid ${palette.hex}`,
                    boxShadow: `0 0 18px ${palette.glow}`,
                    opacity: ring1Opacity,
                    zIndex: 46,
                  }}
                />
              ) : null}

              {ring2Opacity > 0 ? (
                <div
                  style={{
                    position: "absolute",
                    left: cx - ring2Size / 2,
                    top: cy - ring2Size / 2,
                    width: ring2Size,
                    height: ring2Size,
                    borderRadius: "50%",
                    border: `1.2px solid ${palette.hex}`,
                    opacity: ring2Opacity,
                    zIndex: 47,
                  }}
                />
              ) : null}
            </React.Fragment>
          );
        })}

        {/* 3. Tactile macOS Vector Pointer with Press Squish & Rebound */}
        {cursor.visible ? (
          <div
            style={{
              position: "absolute",
              left: cursor.xNorm * scaledW - 5,
              top: cursor.yNorm * scaledH - 4,
              zIndex: 50,
            }}
          >
            <svg
              width={32}
              height={32}
              viewBox="0 0 24 24"
              fill="none"
              style={{
                filter: "drop-shadow(0 6px 14px rgba(0, 0, 0, 0.85))",
                transformOrigin: "5px 4px",
                transform: `scale(${cursor.pointerScale}) rotate(${cursor.pointerRotateDeg}deg)`,
              }}
            >
              <path
                d="M4.5 3.5L19.5 11.2L12.6 13.4L9.8 20.5L4.5 3.5Z"
                fill="#FAFAFA"
                stroke="#09090B"
                strokeWidth={1.7}
                strokeLinejoin="round"
              />
            </svg>
          </div>
        ) : null}
      </div>
    </div>
  );
};
