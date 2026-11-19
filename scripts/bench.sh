#!/usr/bin/env bash
# ==============================================================================
# SettleCore C++17 CLOB Benchmark Execution Harness
# ==============================================================================
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="${ROOT_DIR}/engine/build/release"
RESULTS_DIR="${ROOT_DIR}/benchmarks/results"
ORDER_COUNT="${ORDER_COUNT:-1000000}"
PIN_CORE="${PIN_CORE:-2}"

mkdir -p "${RESULTS_DIR}"

cmake --preset release -S "${ROOT_DIR}/engine"
cmake --build --preset release

RUNNER=()
if command -v taskset >/dev/null 2>&1; then
  RUNNER=(taskset -c "${PIN_CORE}")
fi

echo "[SettleCore Bench] Running 1M-order throughput & depth sweep benchmark..."
"${RUNNER[@]}" "${BUILD_DIR}/clob_bench" \
  --benchmark_out="${RESULTS_DIR}/clob_bench.json" \
  --benchmark_out_format=json

echo "[SettleCore Bench] Running HdrHistogram p50/p99/p99.9/p99.99 tail-latency benchmark (${ORDER_COUNT} orders)..."
"${RUNNER[@]}" "${BUILD_DIR}/latency_hist_bench" \
  --orders="${ORDER_COUNT}" \
  --hdr-out="${RESULTS_DIR}/latency_percentiles.csv" \
  --wal-out="${RESULTS_DIR}/wal_overhead.csv"

echo "[SettleCore Bench] Raw artifacts written to ${RESULTS_DIR}"
