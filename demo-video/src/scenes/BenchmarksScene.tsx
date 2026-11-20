import React from "react";
import { AbsoluteFill } from "remotion";
import { BenchmarkReveal } from "../components/BenchmarkReveal";
import { KineticTitle } from "../components/KineticTitle";
import { TABULAR_FONT_STYLE } from "../styles/typography";

/**
 * Scene 4: BenchmarksScene (global frames 2175..2699, 525 local frames = 14 beats @ 96 BPM)
 * Full-height 3-column benchmark overview + 4 WAL modes + End-to-End Latency Waterfall Bar.
 */
export const BenchmarksScene: React.FC = () => {
  return (
    <AbsoluteFill
      style={{
        backgroundColor: "#09090B",
        backgroundImage:
          "radial-gradient(ellipse 80% 55% at 50% 0%, rgba(14, 203, 129, 0.08), transparent 65%)",
        padding: "44px 64px 36px",
        display: "flex",
        flexDirection: "column",
        gap: 20,
        ...TABULAR_FONT_STYLE,
      }}
    >
      <div style={{ display: "flex", alignItems: "flex-end", justifyContent: "space-between", flexShrink: 0 }}>
        <KineticTitle
          eyebrow="C++17 RELEASE -O3 • OPEN-LOOP BENCHMARKS"
          lines={["1,000,000 Orders. Sub-Microsecond Tail."]}
          align="left"
          startFrame={6}
          staggerFrames={3}
          fontSize={44}
        />

        <div
          style={{
            ...TABULAR_FONT_STYLE,
            fontSize: 13,
            fontWeight: 600,
            color: "#D4D4D8",
            borderRadius: 10,
            border: "1px solid rgba(255,255,255,0.1)",
            background: "#111318",
            padding: "10px 16px",
            whiteSpace: "nowrap",
          }}
        >
          HdrHistogram_c • Group-Commit WAL • ASan/UBSan Clean
        </div>
      </div>

      <div style={{ flex: 1, minHeight: 0 }}>
        <BenchmarkReveal startFrame={16} />
      </div>
    </AbsoluteFill>
  );
};
