# ADR-007: WAL Durability Contract and Group-Commit Amortization

- **Status:** Accepted
- **Date:** 2026-11-21
- **Scope:** `engine/include/settlecore/wal.hpp`, `engine/src/wal.cpp`, `engine/benchmarks/`

## 1. Context

A matching-engine Write-Ahead Log faces a fundamental trade-off between non-blocking speed and crash durability:
- Pure `MEMORY` executes in sub-microsecond CPU time with zero crash durability.
- `BUFFERED` (`write(2)` + periodic flush every `1,024` frames) stays fast in the hot path, **but acknowledges commands before `fdatasync(2)`**, leaving a potential data-loss window of up to `1,024` acknowledged commands on sudden power loss.
- `FSYNC` (`fdatasync(2)` per command) guarantees durability per acknowledged order, **but collapses throughput to single-digit orders/sec** on virtualized disk storage.

A production exchange core needs a mode that provides **durable acknowledgements** (zero acknowledged data loss on `kill -9` or power loss) without paying a separate `fdatasync(2)` syscall latency penalty for every single order.

## 2. Decision

Provide **`GROUP_COMMIT`** (`WalMode::GROUP_COMMIT`) as the default durable WAL mode and codify the exact durability contract across all four modes:

1. **`MEMORY`:** In-memory vector/ring only. Zero disk I/O, zero crash durability. Intended solely for pure data-structure microbenchmarking and ephemeral test fixtures.
2. **`BUFFERED`:** Issues `write(2)` to the OS page cache per command and calls `fdatasync(2)` only every batch threshold (e.g., `1,024` frames). Acknowledgements return immediately after `write(2)`—fast, **not crash-safe for the un-synced window** (`<= 1,024` commands) on host power failure.
3. **`GROUP_COMMIT` (Default Durable Production Mode):** Accumulates up to $N$ commands or a $T\,\mu\text{s}$ flush window, writes the contiguous batch, executes **one `fdatasync(2)` covering both `.wal` and `.evlog`**, and releases execution acknowledgements to callers **only after `fdatasync(2)` returns `0`**.
4. **`FSYNC`:** Issues `write(2)` + `fdatasync(2)` synchronously on every single command before returning `sc_exec_report_t`. Maximum per-order isolation at maximum storage latency cost.

## 3. Exact Specification

### 3.1 Mode Comparison Matrix

| WAL Mode | `write(2)` Cadence | `fdatasync(2)` Cadence | Ack Released | Crash Loss Window (`kill -9` / Power Loss) |
| :--- | :---: | :---: | :---: | :--- |
| **`MEMORY`** | None | None | Immediate | Full in-memory state |
| **`BUFFERED`** | Per command | Every `1,024` frames | **Before `fdatasync`** | Up to `1,024` acknowledged commands |
| **`GROUP_COMMIT`** | Batched | Once per batch ($N$ cmds or $T\,\mu\text{s}$) | **After `fdatasync`** | **0 acknowledged commands** |
| **`FSYNC`** | Per command | Per command | **After `fdatasync`** | **0 acknowledged commands** |

### 3.2 Group-Commit Amortized Cost

If a storage device requires $T_{\text{sync}}$ to complete `fdatasync(2)` and a group-commit batch collects $B$ commands during the flush window:

$$\text{Throughput}_{\text{GROUP\_COMMIT}} \approx \frac{B}{T_{\text{sync}}}$$

Even on virtualized storage where $T_{\text{sync}} \approx 10\text{--}90\,\text{ms}$, batching $B = 256\text{--}1,024$ commands amortizes a single `fdatasync` across hundreds of orders while upholding the invariant that **no client receives `HTTP 201` / WS `ExecutionReport` until its `wal_seq` is durably persisted on disk**.

### 3.3 Crash Recovery & Torn-Tail Handling

On restart after `SIGKILL` (`kill -9`) in `GROUP_COMMIT` or `FSYNC`:
1. `WalReader::read_all` scans `.wal` sequentially, verifying `magic == 0x5343574C`, `version == 1`, and `crc32c` over payload bytes `[16..63]`.
2. Any partial (`< 64`-byte) or CRC-invalid trailing record at EOF is truncated back to the last valid frame boundary (`out_truncated_bytes`).
3. Every command that was acknowledged prior to the crash was part of a completed `fdatasync` batch and is guaranteed to survive recovery.
4. Any pre-trade PostgreSQL `RESERVED` order whose `wal_seq` is absent from the recovered WAL is rolled back (`locked -> available`, `status = 'ABORTED_UNSUBMITTED'`).

## 4. Alternatives Considered

1. **Async Background Thread Flushing Without Waiting for Ack (`BUFFERED` Only):** Rejected for production durability claims because acknowledging before `fdatasync` violates strict zero-data-loss guarantees if the host loses power.
2. **Battery-Backed NVDIMM / Direct-NVMe Bypass Only:** Rejected as a hard requirement so SettleCore remains portable and verifiable on standard Linux NVMe, cloud EBS, and Docker volumes.

## 5. Trade-offs & Failure Modes

- **Added Baseline Latency for Solo Low-Traffic Orders:** When arrival rate is low ($1$ order per $T\,\mu\text{s}$ window), `GROUP_COMMIT` waits up to the timer deadline $T$ before syncing, adding $T\,\mu\text{s}$ to solo-order latency relative to `BUFFERED` (though still vastly faster than `FSYNC` under burst load).
- **Mid-File Bit Rot:** A corrupted CRC32C frame in the middle of a WAL file (not at EOF) is never silently truncated; recovery aborts with `SC_ERR_CORRUPT_WAL` so operators can restore from snapshot + replica rather than silently dropping historical trades.
