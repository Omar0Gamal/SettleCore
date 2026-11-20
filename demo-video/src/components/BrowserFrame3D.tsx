import React from "react";
import { Easing, interpolate, spring, useCurrentFrame, useVideoConfig } from "remotion";
import { TABULAR_FONT_STYLE } from "../styles/typography";

export interface BrowserFrame3DProps {
  children: React.ReactNode;
  url?: string;
  entranceFrame?: number;
  enable3D?: boolean;
  width?: number;
  height?: number;
  statusPill?: string;
  statusTone?: "emerald" | "sky" | "rose";
  style?: React.CSSProperties;
}

export const BrowserFrame3D: React.FC<BrowserFrame3DProps> = ({
  children,
  url = "localhost:3000 // SettleCore Hybrid CLOB & EVM Settlement",
  entranceFrame = 0,
  enable3D = true,
  width = 1680,
  height = 955,
  statusPill = "ANVIL 31337 • 0.183 µs p50",
  statusTone = "emerald",
  style,
}) => {
  const frame = useCurrentFrame();
  const { fps } = useVideoConfig();

  const localFrame = Math.max(0, frame - entranceFrame);
  const rise = spring({
    frame: localFrame,
    fps,
    config: { damping: 24, stiffness: 85, mass: 1.15 },
  });

  const entranceProgress = interpolate(localFrame, [0, 58], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: Easing.bezier(0.22, 1, 0.36, 1),
  });

  // Continuous subtle 3D floating parallax so the frame glides organically in space
  const floatBlend = interpolate(localFrame, [20, 80], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: Easing.inOut(Easing.cubic),
  });
  const driftX = enable3D ? Math.sin(frame * 0.028) * 6 * floatBlend : 0;
  const driftY = enable3D ? Math.cos(frame * 0.023) * 4 * floatBlend : 0;
  const tiltX = enable3D ? Math.sin(frame * 0.021) * 1.4 * floatBlend : 0;
  const tiltY = enable3D ? Math.cos(frame * 0.026) * 2.1 * floatBlend : 0;

  const rotateX = (enable3D ? interpolate(entranceProgress, [0, 1], [18, 0]) : 0) + tiltX;
  const rotateY = tiltY;
  const translateY = (enable3D ? interpolate(entranceProgress, [0, 1], [110, 0]) : 0) + driftY;
  const scale = enable3D ? interpolate(rise, [0, 1], [0.93, 1]) : 1;
  const opacity = enable3D
    ? interpolate(localFrame, [0, 22], [0, 1], {
        extrapolateLeft: "clamp",
        extrapolateRight: "clamp",
        easing: Easing.bezier(0.22, 1, 0.36, 1),
      })
    : 1;

  const pillColor =
    statusTone === "emerald"
      ? "#10B981"
      : statusTone === "rose"
        ? "#F43F5E"
        : "#38BDF8";

  return (
    <div
      style={{
        perspective: 2000,
        width,
        height,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        ...TABULAR_FONT_STYLE,
        ...style,
      }}
    >
      <div
        style={{
          position: "relative",
          width: "100%",
          height: "100%",
          borderRadius: 16,
          overflow: "hidden",
          background: "#111318",
          border: "1px solid rgba(255, 255, 255, 0.11)",
          boxShadow:
            "0 45px 120px rgba(0, 0, 0, 0.85), 0 0 90px rgba(56, 189, 248, 0.12), inset 0 1px 0 rgba(255, 255, 255, 0.18)",
          transform: `translate3d(${driftX}px, ${translateY}px, 0) rotateX(${rotateX}deg) rotateY(${rotateY}deg) scale(${scale})`,
          transformOrigin: "center bottom",
          opacity,
          display: "flex",
          flexDirection: "column",
        }}
      >
        {/* Top Specular Highlight Hairline */}
        <div
          style={{
            position: "absolute",
            top: 0,
            left: "8%",
            right: "8%",
            height: 1,
            background:
              "linear-gradient(90deg, transparent, rgba(56, 189, 248, 0.45), rgba(255, 255, 255, 0.55), rgba(16, 185, 129, 0.45), transparent)",
            pointerEvents: "none",
            zIndex: 20,
          }}
        />

        {/* Browser Window Chrome */}
        <div
          style={{
            height: 44,
            flexShrink: 0,
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            padding: "0 18px",
            background: "#111318",
            borderBottom: "1px solid rgba(255, 255, 255, 0.08)",
          }}
        >
          {/* Traffic Light Dots */}
          <div style={{ display: "flex", alignItems: "center", gap: 8, width: 120 }}>
            <span
              style={{
                width: 11,
                height: 11,
                borderRadius: "50%",
                background: "#F43F5E",
                opacity: 0.9,
              }}
            />
            <span
              style={{
                width: 11,
                height: 11,
                borderRadius: "50%",
                background: "#F59E0B",
                opacity: 0.9,
              }}
            />
            <span
              style={{
                width: 11,
                height: 11,
                borderRadius: "50%",
                background: "#10B981",
                opacity: 0.9,
              }}
            />
          </div>

          {/* URL Pill */}
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 8,
              padding: "5px 18px",
              borderRadius: 8,
              border: "1px solid rgba(255, 255, 255, 0.08)",
              background: "#09090B",
              color: "#D4D4D8",
              ...TABULAR_FONT_STYLE,
              fontSize: 12,
              fontWeight: 500,
              letterSpacing: "0.01em",
            }}
          >
            <span
              style={{
                width: 6,
                height: 6,
                borderRadius: "50%",
                background: "#10B981",
                boxShadow: "0 0 8px #10B981",
              }}
            />
            <span>{url}</span>
          </div>

          {/* Right Status Badge */}
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-end",
              width: 240,
            }}
          >
            <span
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: 6,
                padding: "4px 10px",
                borderRadius: 6,
                border: `1px solid ${pillColor}44`,
                background: `${pillColor}14`,
                color: pillColor,
                ...TABULAR_FONT_STYLE,
                fontSize: 11,
                fontWeight: 600,
              }}
            >
              <span
                style={{
                  width: 5,
                  height: 5,
                  borderRadius: "50%",
                  background: pillColor,
                }}
              />
              {statusPill}
            </span>
          </div>
        </div>

        {/* Viewport Body */}
        <div
          style={{
            position: "relative",
            flex: 1,
            overflow: "hidden",
            background: "#09090B",
          }}
        >
          {children}
        </div>
      </div>
    </div>
  );
};
