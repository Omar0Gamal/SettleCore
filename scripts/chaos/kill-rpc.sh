#!/usr/bin/env bash
# ==============================================================================
# Chaos Drill 2: Kill Primary EVM RPC Proxy to Verify Sub-Second Multi-RPC Failover
# ==============================================================================
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://localhost:8080}"
ACTION="${1:-kill}"

echo "[Chaos Drill] Executing primary RPC proxy fault injection (action=${ACTION})..."
curl -fsSL -X POST "${GATEWAY_URL}/v1/chaos/rpc-failover" \
  -H "Content-Type: application/json" \
  -d "{\"action\": \"${ACTION}\"}"
echo ""
