import React from "react";
import { AbsoluteFill, Easing, interpolate, useCurrentFrame } from "remotion";
import { KineticTitle } from "../components/KineticTitle";
import { TABULAR_FONT_STYLE } from "../styles/typography";

interface ArchCard {
  layer: string;
  tech: string;
  detail: string;
  accent: string;
}

const ARCH_CARDS: ArchCard[] = [
  {
    layer: "Matching Core",
    tech: "C++17 CLOB + C ABI",
    detail: "Fixed-point int64 (1e8) • Flat hash + intrusive FIFO • CRC32C WAL • 0.183 µs p50",
    accent: "#38BDF8",
  },
  {
    layer: "Ledger & EVM Relayer",
    tech: "Go 1.27 Double-Entry",
    detail: "Two-phase order reservation • 3-block reorg rollback • RPC failover & gas bumper",
    accent: "#10B981",
  },
  {
    layer: "On-Chain Custody",
    tech: "Solidity 0.8.38 EscrowVault",
    detail: "EIP-712 tiered Hot/Cold signers • Rolling token-bucket caps • Merkle Sum Tree PoR",
    accent: "#38BDF8",
  },
];

const SILKY_EASE = Easing.bezier(0.22, 1, 0.36, 1);

/**
 * Scene 5: OutroScene (frames 2696..2999, 304 local frames)
 * Minimalist architecture topology cards + docker compose up --build + repo slug.
 */
export const OutroScene: React.FC = () => {
  const frame = useCurrentFrame();

  const cmdProgress = interpolate(frame - 78, [0, 36], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
    easing: SILKY_EASE,
  });

  return (
    <AbsoluteFill
      style={{
        backgroundColor: "#09090B",
        backgroundImage:
          "radial-gradient(ellipse 75% 55% at 50% 15%, rgba(56, 189, 248, 0.08), transparent 70%)",
        padding: "72px 110px",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        gap: 44,
        ...TABULAR_FONT_STYLE,
      }}
    >
      <KineticTitle
        eyebrow="DETERMINISTIC EXECUTION • CRYPTOGRAPHIC SOLVENCY"
        lines={["SettleCore"]}
        subtitle="Hybrid Institutional CLOB & Reorg-Safe EVM Settlement Engine"
        startFrame={8}
        staggerFrames={4}
        fontSize={74}
      />

      {/* Architecture Topology Cards */}
      <div
        style={{
          width: "100%",
          maxWidth: 1460,
          display: "grid",
          gridTemplateColumns: "repeat(3, minmax(0, 1fr))",
          gap: 24,
        }}
      >
        {ARCH_CARDS.map((card, i) => {
          const p = interpolate(frame - (28 + i * 10), [0, 36], [0, 1], {
            extrapolateLeft: "clamp",
            extrapolateRight: "clamp",
            easing: SILKY_EASE,
          });
          return (
            <div
              key={card.layer}
              style={{
                opacity: p,
                transform: `translateY(${interpolate(p, [0, 1], [22, 0])}px)`,
                borderRadius: 14,
                border: "1px solid rgba(255, 255, 255, 0.1)",
                background: "#111318",
                padding: "24px 26px",
                boxShadow: "0 20px 50px rgba(0,0,0,0.45), inset 0 1px 0 rgba(255,255,255,0.08)",
              }}
            >
              <div
                style={{
                  ...TABULAR_FONT_STYLE,
                  fontSize: 12,
                  fontWeight: 600,
                  textTransform: "uppercase",
                  letterSpacing: "0.08em",
                  color: card.accent,
                  marginBottom: 8,
                }}
              >
                {card.layer}
              </div>
              <div
                style={{
                  fontSize: 22,
                  fontWeight: 600,
                  color: "#FAFAFA",
                  letterSpacing: "-0.02em",
                  marginBottom: 10,
                }}
              >
                {card.tech}
              </div>
              <div style={{ fontSize: 14, lineHeight: 1.55, color: "#A1A1AA" }}>{card.detail}</div>
            </div>
          );
        })}
      </div>

      {/* Terminal Launch Command & Repository Slug */}
      <div
        style={{
          opacity: cmdProgress,
          transform: `translateY(${interpolate(cmdProgress, [0, 1], [16, 0])}px)`,
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          gap: 16,
        }}
      >
        <div
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: 14,
            padding: "16px 30px",
            borderRadius: 12,
            border: "1px solid rgba(56, 189, 248, 0.35)",
            background: "#111318",
            boxShadow: "0 0 40px rgba(56, 189, 248, 0.14)",
            ...TABULAR_FONT_STYLE,
            fontSize: 22,
            fontWeight: 600,
            color: "#FAFAFA",
          }}
        >
          <span style={{ color: "#10B981" }}>$</span>
          <span>docker compose up --build</span>
        </div>

        <div
          style={{
            ...TABULAR_FONT_STYLE,
            fontSize: 17,
            fontWeight: 500,
            color: "#A1A1AA",
            letterSpacing: "0.02em",
          }}
        >
          github.com/Omar0Gamal/SettleCore
        </div>
      </div>
    </AbsoluteFill>
  );
};
