#!/usr/bin/env bash
# ==============================================================================
# Chaos Drill 4: -15% Flash Crash Burst to Trip C++17 CLOB Circuit Breaker
# ==============================================================================
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://localhost:8080}"
PAIR="${PAIR:-ETH/USDC}"
DROP_BPS="${DROP_BPS:-1500}"

echo "[Chaos Drill] Injecting -15% (${DROP_BPS} bps) sell sweep on ${PAIR} to trip circuit breaker..."
curl -fsSL -X POST "${GATEWAY_URL}/v1/chaos/flash-crash" \
  -H "Content-Type: application/json" \
  -d "{\"pair\": \"${PAIR}\", \"drop_bps\": ${DROP_BPS}}"
echo ""
