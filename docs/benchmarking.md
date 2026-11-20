# SettleCore Benchmarking & Automated SVG Chart Pipeline Specification

This document specifies the methodology, hardware isolation rules, workload distributions, raw artifact schemas, and `uv`-managed SVG chart generation pipeline (`engine/benchmarks/` + `benchmarks/charts/`) for the SettleCore C++17 Central Limit Order Book.

---

## 1. Reproducible Hardware & OS Isolation Methodology

Microsecond-level tail-latency benchmarks (`p99`, `p99.9`, `p99.99`) are highly sensitive to OS scheduler migrations, CPU frequency scaling, and thermal throttling. All official benchmark runs via `scripts/bench.sh` follow four rules:

1. **Compiler Optimization Preset (`cmake --preset release`):**
   - Compiled with `-O3 -DNDEBUG` and C++17 standard conformance.
   - Pre-allocates the full `1,048,576`-slot `OrderNode` slab (64 MB) before timer start so zero `mmap`/`brk` page faults occur during benchmark measurement.
2. **Core Pinning (`taskset -c 2`):**
   - On Linux hosts, `scripts/bench.sh` pins the single-threaded benchmark executable to physical core `2` (`PIN_CORE=2`) to avoid core-to-core L1/L2 cache invalidation.
   - When root access is available on dedicated bare-metal runners, set CPU frequency governor to `performance` (`cpupower frequency-set -g performance`) and disable turbo boost jitter.
3. **Cache Warm-Up Phase:**
   - Every benchmark executes `100,000` warm-up order insertions, matches, and cancellations prior to resetting the `HdrHistogram_c` counters so instruction caches, branch predictors, and pre-allocated slab cache lines are warm.
4. **Open-Loop Arrival-Rate & Coordinated-Omission Correction (`HdrHistogram_c` `v0.12.1`):**
   - `latency_hist_bench.cpp` records both raw execution service time (`hdr_record_value`) and open-loop arrival-rate corrected latency (`hdr_record_corrected_value` with target inter-arrival interval `expected_interval_ns`) into logarithmic `hdr_histogram` buffers configured with:
     - `lowest_discernible_value = 10` (10 nanoseconds)
     - `highest_trackable_value = 100,000,000` (100 milliseconds)
     - `significant_figures = 3`
   - Note on environment: benchmarks measured under WSL2 / virtualized storage reflect virtual disk `fdatasync` latency (`fsync_wal` and `group_commit_wal` sync cost) rather than bare-metal NVMe; hardware specs and environment caveats are disclosed alongside all results.

---

## 2. Workload Profiles

The C++17 benchmark binaries (`engine/benchmarks/clob_bench.cpp` and `engine/benchmarks/latency_hist_bench.cpp`) execute four deterministic PRNG-seeded (`seed = 0x534554544C45434F5245ULL`) workloads:

| Profile ID | Name | Order Mix | Price Level Distribution | Purpose |
| :--- | :--- | :--- | :--- | :--- |
| **W1** | `BalancedMarketMaker` | 55% Limit Insert, 35% Cancel, 10% Aggressive Crossing Limit/Market | Binomial concentration within $\pm 50$ ticks of mid-price | Measures steady-state latency percentile distribution (`p50` to `p99.99`) across 1M orders. |
| **W2** | `DepthScalingSweep` | Pre-populate $D \in \{10^2, 10^3, 10^4, 10^5\}$ active resting price levels, then stream 1M mixed orders | Uniform + geometric tail across $D$ levels | Proves $O(1)$ cancellation and $O(1)$ best-bid/ask lookup invariance as book depth scales $1000\times$. |
| **W3** | `DeepBookSweep` | Single aggressive Market/Limit order sweeping $K \in \{1, 10, 50, 250\}$ resting orders in one call | Dense 1-tick ladder | Measures per-fill iteration cost (`__int128_t` notional math + intrusive FIFO pop). |
| **W4** | `WalDurabilityModes` | Orders under `W1` across four WAL settings: `in_memory`, `buffered_wal` (`write` + batched `fdatasync`), `group_commit_wal` (batch `fdatasync` before ack), `fsync_wal` (per-order `fdatasync`) | Same as `W1` | Quantifies exact latency and throughput trade-offs of 64-byte CRC32C WAL + `.evlog` persistence vs. pure in-memory matching. |

---

## 3. Raw Artifact Schemas (`benchmarks/results/`)

1. **`benchmarks/results/clob_bench.json`** (Google Benchmark JSON output):
   Contains aggregate `items_per_second` (orders/sec) and `cpu_time` across `W2` depth levels ($100$, $1,000$, $10,000$, $100,000$ resting price levels) and `W3` sweep sizes.
2. **`benchmarks/results/latency_percentiles.csv`** (`HdrHistogram_c` raw service time + open-loop coordinated-omission corrected percentiles):
   ```csv
   percentile,latency_ns,latency_us,co_latency_ns,co_latency_us,sample_count,co_sample_count
   50.0000,215,0.215,215,0.215,1000000,1009448
   90.0000,367,0.367,375,0.375,1000000,1009448
   99.0000,703,0.703,2703,2.703,1000000,1009448
   99.9000,2503,2.503,2172927,2172.927,1000000,1009448
   99.9900,24911,24.911,3956735,3956.735,1000000,1009448
   ```
3. **`benchmarks/results/wal_overhead.csv`** (4 WAL durability modes):
   ```csv
   mode,p50_us,p90_us,p99_us,p999_us,co_p99_us,co_p999_us,throughput_ops_sec
   in_memory,0.215,0.367,0.703,2.503,2.703,2172.927,2895287
   buffered_wal,1.119,2.367,8.783,86966.271,133758.975,190709.759,5699
   group_commit_wal,0.655,1.471,96010.239,132775.935,124518.399,152305.663,339
   fsync_wal,168034.303,177209.343,216137.727,216662.015,216137.727,216662.015,6
   ```

---

## 4. Automated Dual-Theme SVG Chart Specification (`benchmarks/charts/`)

Managed by **`uv`** (`benchmarks/charts/pyproject.toml` with `matplotlib` and `numpy`), `generate_charts.py` and `styles.py` render six publication-grade vector SVGs into `docs/assets/benchmarks/` for automatic GitHub `<picture>` dark/light theme switching:

| Output SVG File Pair | Visual Specification |
| :--- | :--- |
| `latency-percentiles-dark.svg`<br>`latency-percentiles-light.svg` | Horizontal bar + CDF tail curve displaying `p50`, `p90`, `p99`, `p99.9`, and `p99.99` latency in microseconds ($\mu\text{s}$) over 1,000,000 orders, with direct data-value callout labels on every bar and a dashed $10\,\mu\text{s}$ SLA threshold line. |
| `throughput-vs-depth-dark.svg`<br>`throughput-vs-depth-light.svg` | Log-scale X-axis ($10^2 \rightarrow 10^5$ active resting price levels) vs. sustained throughput (million orders/sec) comparing `Limit Insert`, `O(1) Cancel`, and `Aggressive Match`. |
| `wal-overhead-dark.svg`<br>`wal-overhead-light.svg` | Grouped comparison of `Pure In-Memory`, `Buffered CRC32C WAL`, `Group-Commit WAL`, and `Synchronous fsync WAL` across `p50`, `p99`, and `p99.9` latency ($\mu\text{s}$). |

### Theme Color Tokens (`benchmarks/charts/styles.py`)

| Token | Dark Mode (`*-dark.svg`) | Light Mode (`*-light.svg`) |
| :--- | :--- | :--- |
| Canvas Background | `#0D1117` (GitHub Dark Canvas) | `#FFFFFF` (GitHub Light Canvas) |
| Panel / Grid Hairline | `#21262D` | `#E5E7EB` |
| Primary Text / Axis Labels | `#F0F6FC` | `#111827` |
| Muted Subtitle / Units | `#8B949E` | `#4B5563` |
| Primary Metric Accent (`p50`/`p90`) | `#38BDF8` (Cyan 400) | `#0284C7` (Cyan 600) |
| Tail Latency Accent (`p99`/`p99.9`) | `#818CF8` (Indigo 400) | `#4F46E5` (Indigo 600) |
| Extreme Tail / SLA Line (`p99.99`) | `#F43F5E` (Rose 500) | `#E11D48` (Rose 600) |

### Execution Commands

```bash
# 1. Run C++17 Release benchmarks (outputs JSON/CSV to benchmarks/results/)
make bench

# 2. Render all 6 dark/light SVG charts via uv
make bench-charts
```
