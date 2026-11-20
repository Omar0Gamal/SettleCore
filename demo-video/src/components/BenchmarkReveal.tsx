import React from "react";
import { Easing, interpolate, useCurrentFrame } from "remotion";
import { TABULAR_FONT_STYLE } from "../styles/typography";

interface PercentileRow {
  label: string;
  ns: number;
  us: string;
  barPct: number;
  highlight?: boolean;
}

interface DepthRow {
  levels: string;
  insertOps: string;
  cancelOps: string;
  matchOps: string;
  barPct: number;
}

interface WalRow {
  mode: string;
  p50: string;
  p99: string;
  throughput: string;
  tone: string;
  barPct: number;
}

interface WaterfallStage {
  stage: string;
  latency: string;
  pct: number;
  color: string;
}

const PERCENTILE_ROWS: PercentileRow[] = [
  { label: "p50", ns: 215, us: "0.215 µs", barPct: 18, highlight: true },
  { label: "p90", ns: 367, us: "0.367 µs", barPct: 28 },
  { label: "p99", ns: 703, us: "0.703 µs", barPct: 50, highlight: true },
  { label: "p99.9", ns: 2503, us: "2.503 µs", barPct: 72 },
  { label: "p99.99", ns: 24911, us: "24.911 µs", barPct: 100 },
];

const DEPTH_ROWS: DepthRow[] = [
  { levels: "100 levels", insertOps: "3.09M/s", cancelOps: "13.50M/s", matchOps: "7.97M/s", barPct: 100 },
  { levels: "1,000 levels", insertOps: "3.03M/s", cancelOps: "6.69M/s", matchOps: "10.77M/s", barPct: 82 },
  { levels: "10,000 levels", insertOps: "2.80M/s", cancelOps: "3.66M/s", matchOps: "11.30M/s", barPct: 86 },
  { levels: "100,000 levels", insertOps: "1.84M/s", cancelOps: "3.12M/s", matchOps: "9.95M/s", barPct: 76 },
];

const WAL_ROWS: WalRow[] = [
  { mode: "in_memory", p50: "0.215 µs", p99: "0.703 µs", throughput: "2.90M ops/s", tone: "#38BDF8", barPct: 100 },
  { mode: "group_commit_wal", p50: "0.655 µs", p99: "96.0 ms (batch)", throughput: "339 ops/s (56x fsync)", tone: "#0ECB81", barPct: 68 },
  { mode: "buffered_wal", p50: "1.119 µs", p99: "8.783 µs", throughput: "5.70K ops/s", tone: "#F59E0B", barPct: 48 },
  { mode: "fsync_wal", p50: "168.0 ms", p99: "216.1 ms", throughput: "6 ops/s", tone: "#F43F5E", barPct: 12 },
];

const WATERFALL_STAGES: WaterfallStage[] = [
  { stage: "EIP-712 Verify", latency: "12 µs", pct: 18, color: "#38BDF8" },
  { stage: "PG Pre-Trade Reserve", latency: "380 µs", pct: 46, color: "#818CF8" },
  { stage: "C ABI", latency: "0.15 µs", pct: 10, color: "#0ECB81" },
  { stage: "C++17 CLOB Match", latency: "0.215 µs", pct: 12, color: "#38BDF8" },
  { stage: "Group-Commit WAL", latency: "0.655 µs", pct: 14, color: "#0ECB81" },
];

const SILKY_EASE = Easing.bezier(0.22, 1, 0.36, 1);

/**
 * Fixes V8:
 * - Full-height balanced 3 columns with zero empty 40% bottom gap
 * - Includes all 4 WAL durability modes (in_memory, group_commit_wal, buffered_wal, fsync_wal)
 * - Bottom End-to-End Latency Waterfall Bar
 */
export const BenchmarkReveal: React.FC<{ startFrame?: number }> = ({ startFrame = 0 }) => {
  const frame = useCurrentFrame();

  const cardReveal = (delay: number, duration = 34) =>
    interpolate(frame - (startFrame + delay), [0, duration], [0, 1], {
      extrapolateLeft: "clamp",
      extrapolateRight: "clamp",
      easing: SILKY_EASE,
    });

  const heroStats = [
    { label: "p50 Match Latency", value: "0.215 µs", sub: "215 ns • 1M orders (WSL2)", color: "#38BDF8" },
    { label: "p99 Tail Latency", value: "0.703 µs", sub: "703 ns • HdrHistogram_c", color: "#0ECB81" },
    { label: "Balanced MM Throughput", value: "4.90M/s", sub: "4,898,680 ops/sec • 204 ns", color: "#38BDF8" },
    { label: "Depth Scaling (100–100K)", value: "3.09M–13.50M/s", sub: "O(1) Hash + Intrusive List", color: "#0ECB81" },
  ];

  const waterfallAnim = cardReveal(46, 40);

  return (
    <div
      style={{
        width: "100%",
        height: "100%",
        display: "flex",
        flexDirection: "column",
        gap: 16,
        ...TABULAR_FONT_STYLE,
      }}
    >
      {/* Top 4 Headline KPI Strip */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(4, minmax(0, 1fr))",
          gap: 16,
          flexShrink: 0,
        }}
      >
        {heroStats.map((stat, i) => {
          const p = cardReveal(i * 5);
          return (
            <div
              key={stat.label}
              style={{
                opacity: p,
                transform: `translateY(${interpolate(p, [0, 1], [16, 0])}px)`,
                borderRadius: 12,
                border: "1px solid rgba(255, 255, 255, 0.09)",
                background: "#111318",
                padding: "16px 20px",
                boxShadow: "inset 0 1px 0 rgba(255,255,255,0.06)",
              }}
            >
              <div
                style={{
                  fontSize: 12,
                  fontWeight: 600,
                  textTransform: "uppercase",
                  letterSpacing: "0.06em",
                  color: "#A1A1AA",
                  marginBottom: 6,
                }}
              >
                {stat.label}
              </div>
              <div
                style={{
                  ...TABULAR_FONT_STYLE,
                  fontSize: 34,
                  fontWeight: 700,
                  color: stat.color,
                  letterSpacing: "-0.025em",
                  lineHeight: 1.1,
                }}
              >
                {stat.value}
              </div>
              <div
                style={{
                  ...TABULAR_FONT_STYLE,
                  fontSize: 12,
                  color: "#71717A",
                  marginTop: 5,
                }}
              >
                {stat.sub}
              </div>
            </div>
          );
        })}
      </div>

      {/* Main 3-Column Full-Height Benchmark Panels */}
      <div
        style={{
          flex: 1,
          display: "grid",
          gridTemplateColumns: "1.1fr 1.15fr 1.05fr",
          gap: 18,
          minHeight: 0,
        }}
      >
        {/* Panel 1: HdrHistogram_c 1M-Order Latency Percentiles */}
        <div
          style={{
            opacity: cardReveal(14),
            transform: `translateY(${interpolate(cardReveal(14), [0, 1], [18, 0])}px)`,
            borderRadius: 14,
            border: "1px solid rgba(255, 255, 255, 0.09)",
            background: "#111318",
            padding: "22px 24px",
            display: "flex",
            flexDirection: "column",
            justifyContent: "space-between",
          }}
        >
          <div>
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 6 }}>
              <span style={{ fontSize: 18, fontWeight: 700, color: "#FAFAFA" }}>
                C++17 HdrHistogram_c Latency
              </span>
              <span
                style={{
                  ...TABULAR_FONT_STYLE,
                  fontSize: 11,
                  fontWeight: 700,
                  color: "#38BDF8",
                  background: "rgba(56, 189, 248, 0.12)",
                  border: "1px solid rgba(56, 189, 248, 0.28)",
                  borderRadius: 6,
                  padding: "4px 9px",
                }}
              >
                1,000,000 ORDERS
              </span>
            </div>
            <div style={{ fontSize: 13, color: "#A1A1AA" }}>
              Release -O3 -march=native single-thread matching latency
            </div>
          </div>

          <div
            style={{
              flex: 1,
              display: "flex",
              flexDirection: "column",
              justifyContent: "space-evenly",
              padding: "12px 0",
            }}
          >
            {PERCENTILE_ROWS.map((row, idx) => {
              const barAnim = cardReveal(22 + idx * 6, 38);
              const widthPct = row.barPct * barAnim;
              return (
                <div key={row.label} style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                  <div
                    style={{
                      display: "flex",
                      justifyContent: "space-between",
                      alignItems: "baseline",
                      ...TABULAR_FONT_STYLE,
                      fontSize: 16,
                    }}
                  >
                    <span style={{ color: row.highlight ? "#FAFAFA" : "#D4D4D8", fontWeight: 700 }}>
                      {row.label}
                    </span>
                    <span style={{ color: row.highlight ? "#38BDF8" : "#EAECEF", fontWeight: 600 }}>
                      {row.us}{" "}
                      <span style={{ color: "#71717A", fontSize: 13 }}>({row.ns} ns)</span>
                    </span>
                  </div>
                  <div
                    style={{
                      height: 15,
                      borderRadius: 8,
                      background: "#09090B",
                      border: "1px solid rgba(255,255,255,0.07)",
                      overflow: "hidden",
                    }}
                  >
                    <div
                      style={{
                        height: "100%",
                        width: `${widthPct}%`,
                        borderRadius: 8,
                        background:
                          row.label === "p99.99"
                            ? "linear-gradient(90deg, #38BDF8, #F43F5E)"
                            : "linear-gradient(90deg, #0ECB81, #38BDF8)",
                        boxShadow: "0 0 18px rgba(56, 189, 248, 0.35)",
                      }}
                    />
                  </div>
                </div>
              );
            })}
          </div>

          <div
            style={{
              paddingTop: 12,
              borderTop: "1px solid rgba(255,255,255,0.08)",
              ...TABULAR_FONT_STYLE,
              fontSize: 13,
              color: "#A1A1AA",
              display: "flex",
              justifyContent: "space-between",
            }}
          >
            <span>Slab Pool Pre-Allocation</span>
            <span style={{ color: "#0ECB81", fontWeight: 700 }}>Zero heap allocs on hot path</span>
          </div>
        </div>

        {/* Panel 2: Depth Scaling (100 -> 100,000 Price Levels) */}
        <div
          style={{
            opacity: cardReveal(20),
            transform: `translateY(${interpolate(cardReveal(20), [0, 1], [18, 0])}px)`,
            borderRadius: 14,
            border: "1px solid rgba(255, 255, 255, 0.09)",
            background: "#111318",
            padding: "22px 24px",
            display: "flex",
            flexDirection: "column",
            justifyContent: "space-between",
          }}
        >
          <div>
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 6 }}>
              <span style={{ fontSize: 18, fontWeight: 700, color: "#FAFAFA" }}>
                Depth Scaling (100 → 100K)
              </span>
              <span
                style={{
                  ...TABULAR_FONT_STYLE,
                  fontSize: 11,
                  fontWeight: 700,
                  color: "#0ECB81",
                  background: "rgba(14, 203, 129, 0.12)",
                  border: "1px solid rgba(14, 203, 129, 0.28)",
                  borderRadius: 6,
                  padding: "4px 9px",
                }}
              >
                3.09M–13.50M OPS/SEC
              </span>
            </div>
            <div style={{ fontSize: 13, color: "#A1A1AA" }}>
              Flat hash index + intrusive doubly-linked FIFO queues
            </div>
          </div>

          <div
            style={{
              flex: 1,
              display: "flex",
              flexDirection: "column",
              justifyContent: "space-evenly",
              padding: "10px 0",
              gap: 10,
            }}
          >
            {DEPTH_ROWS.map((row, idx) => {
              const barAnim = cardReveal(28 + idx * 7, 38);
              return (
                <div
                  key={row.levels}
                  style={{
                    borderRadius: 10,
                    border: "1px solid rgba(255,255,255,0.08)",
                    background: "#09090B",
                    padding: "14px 16px",
                    display: "flex",
                    flexDirection: "column",
                    gap: 8,
                  }}
                >
                  <div
                    style={{
                      display: "flex",
                      justifyContent: "space-between",
                      alignItems: "center",
                      ...TABULAR_FONT_STYLE,
                      fontSize: 15,
                    }}
                  >
                    <span style={{ color: "#FAFAFA", fontWeight: 700 }}>{row.levels}</span>
                    <span style={{ color: "#0ECB81", fontWeight: 700 }}>Match {row.matchOps}</span>
                  </div>
                  <div
                    style={{
                      height: 11,
                      borderRadius: 6,
                      background: "rgba(255,255,255,0.06)",
                      overflow: "hidden",
                    }}
                  >
                    <div
                      style={{
                        height: "100%",
                        width: `${row.barPct * barAnim}%`,
                        borderRadius: 6,
                        background: "linear-gradient(90deg, #38BDF8, #0ECB81)",
                      }}
                    />
                  </div>
                  <div
                    style={{
                      display: "flex",
                      justifyContent: "space-between",
                      ...TABULAR_FONT_STYLE,
                      fontSize: 12,
                      color: "#A1A1AA",
                    }}
                  >
                    <span>Insert: {row.insertOps}</span>
                    <span>Cancel: {row.cancelOps}</span>
                  </div>
                </div>
              );
            })}
          </div>

          <div
            style={{
              paddingTop: 12,
              borderTop: "1px solid rgba(255,255,255,0.08)",
              ...TABULAR_FONT_STYLE,
              fontSize: 13,
              color: "#A1A1AA",
              display: "flex",
              justifyContent: "space-between",
            }}
          >
            <span>250-Level Sweep</span>
            <span style={{ color: "#38BDF8", fontWeight: 700 }}>16.87 µs (59.3K sweeps/s)</span>
          </div>
        </div>

        {/* Panel 3: All 4 WAL Durability Modes (incl. group_commit_wal) */}
        <div
          style={{
            opacity: cardReveal(26),
            transform: `translateY(${interpolate(cardReveal(26), [0, 1], [18, 0])}px)`,
            borderRadius: 14,
            border: "1px solid rgba(255, 255, 255, 0.09)",
            background: "#111318",
            padding: "22px 24px",
            display: "flex",
            flexDirection: "column",
            justifyContent: "space-between",
          }}
        >
          <div>
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 6 }}>
              <span style={{ fontSize: 18, fontWeight: 700, color: "#FAFAFA" }}>
                WAL Durability (4 Modes)
              </span>
              <span
                style={{
                  ...TABULAR_FONT_STYLE,
                  fontSize: 11,
                  fontWeight: 700,
                  color: "#0ECB81",
                  background: "rgba(14, 203, 129, 0.12)",
                  border: "1px solid rgba(14, 203, 129, 0.28)",
                  borderRadius: 6,
                  padding: "4px 9px",
                }}
              >
                CRC32C + FDATASYNC
              </span>
            </div>
            <div style={{ fontSize: 13, color: "#A1A1AA" }}>
              Group-commit amortizes fdatasync across batch frames
            </div>
          </div>

          <div
            style={{
              flex: 1,
              display: "flex",
              flexDirection: "column",
              justifyContent: "space-evenly",
              padding: "10px 0",
              gap: 10,
            }}
          >
            {WAL_ROWS.map((row, idx) => {
              const barAnim = cardReveal(34 + idx * 7, 38);
              return (
                <div
                  key={row.mode}
                  style={{
                    borderRadius: 10,
                    border:
                      row.mode === "group_commit_wal"
                        ? "1px solid rgba(14, 203, 129, 0.36)"
                        : "1px solid rgba(255,255,255,0.08)",
                    background: "#09090B",
                    padding: "13px 16px",
                    display: "flex",
                    flexDirection: "column",
                    gap: 8,
                  }}
                >
                  <div
                    style={{
                      display: "flex",
                      justifyContent: "space-between",
                      alignItems: "center",
                      ...TABULAR_FONT_STYLE,
                    }}
                  >
                    <span style={{ fontSize: 15, fontWeight: 700, color: "#FAFAFA" }}>
                      {row.mode}
                    </span>
                    <span style={{ fontSize: 14, fontWeight: 700, color: row.tone }}>
                      {row.throughput}
                    </span>
                  </div>
                  <div
                    style={{
                      height: 11,
                      borderRadius: 6,
                      background: "rgba(255,255,255,0.06)",
                      overflow: "hidden",
                    }}
                  >
                    <div
                      style={{
                        height: "100%",
                        width: `${row.barPct * barAnim}%`,
                        borderRadius: 6,
                        background: row.tone,
                      }}
                    />
                  </div>
                  <div
                    style={{
                      display: "flex",
                      justifyContent: "space-between",
                      ...TABULAR_FONT_STYLE,
                      fontSize: 12,
                      color: "#A1A1AA",
                    }}
                  >
                    <span>p50: {row.p50}</span>
                    <span>p99: {row.p99}</span>
                  </div>
                </div>
              );
            })}
          </div>

          <div
            style={{
              paddingTop: 12,
              borderTop: "1px solid rgba(255,255,255,0.08)",
              ...TABULAR_FONT_STYLE,
              fontSize: 13,
              color: "#A1A1AA",
              display: "flex",
              justifyContent: "space-between",
            }}
          >
            <span>Snapshot + WAL Replay</span>
            <span style={{ color: "#0ECB81", fontWeight: 700 }}>Bit-identical state</span>
          </div>
        </div>
      </div>

      {/* Bottom End-to-End Latency Waterfall Bar */}
      <div
        style={{
          opacity: waterfallAnim,
          transform: `translateY(${interpolate(waterfallAnim, [0, 1], [14, 0])}px)`,
          borderRadius: 12,
          border: "1px solid rgba(255, 255, 255, 0.09)",
          background: "#111318",
          padding: "14px 22px",
          flexShrink: 0,
          display: "flex",
          flexDirection: "column",
          gap: 10,
        }}
      >
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <span style={{ fontSize: 13, fontWeight: 700, letterSpacing: "0.04em", color: "#FAFAFA", textTransform: "uppercase" }}>
            End-to-End Order Latency Waterfall
          </span>
          <span style={{ fontSize: 12, fontWeight: 600, color: "#0ECB81" }}>
            EIP-712 Verify 12 µs → PG Pre-Trade Reserve 380 µs → C ABI 0.15 µs → C++17 CLOB Match 0.215 µs → Group-Commit WAL 0.655 µs
          </span>
        </div>

        <div
          style={{
            display: "flex",
            width: "100%",
            height: 22,
            borderRadius: 7,
            overflow: "hidden",
            background: "#09090B",
            border: "1px solid rgba(255,255,255,0.08)",
            gap: 2,
          }}
        >
          {WATERFALL_STAGES.map((seg) => (
            <div
              key={seg.stage}
              style={{
                width: `${seg.pct * waterfallAnim}%`,
                height: "100%",
                background: `${seg.color}26`,
                borderTop: `2px solid ${seg.color}`,
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                fontSize: 11,
                fontWeight: 700,
                color: "#FAFAFA",
                whiteSpace: "nowrap",
                overflow: "hidden",
                padding: "0 8px",
              }}
            >
              {seg.stage}: {seg.latency}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
};
