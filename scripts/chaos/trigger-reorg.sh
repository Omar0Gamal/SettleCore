#!/usr/bin/env bash
# ==============================================================================
# Chaos Drill 1: Deterministic 3-Block Chain Reorg via Gateway Chaos API / Anvil
# ==============================================================================
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://localhost:8080}"
REORG_DEPTH="${REORG_DEPTH:-3}"

echo "[Chaos Drill] Triggering ${REORG_DEPTH}-block chain reorganization..."
curl -fsSL -X POST "${GATEWAY_URL}/v1/chaos/reorg" \
  -H "Content-Type: application/json" \
  -d "{\"depth\": ${REORG_DEPTH}}"
echo ""
