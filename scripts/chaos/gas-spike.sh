#!/usr/bin/env bash
# ==============================================================================
# Chaos Drill 3: 10x Base-Fee Spike to Trigger Relayer EIP-1559 Fee Bumping
# ==============================================================================
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://localhost:8080}"
MULTIPLIER="${MULTIPLIER:-10}"

echo "[Chaos Drill] Injecting ${MULTIPLIER}x EIP-1559 baseFeePerGas spike..."
curl -fsSL -X POST "${GATEWAY_URL}/v1/chaos/gas-spike" \
  -H "Content-Type: application/json" \
  -d "{\"base_fee_multiplier\": ${MULTIPLIER}}"
echo ""
