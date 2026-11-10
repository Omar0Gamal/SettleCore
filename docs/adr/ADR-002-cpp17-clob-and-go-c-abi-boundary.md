# ADR-002: Single-Threaded Zero-Lock Hot Path C++17 CLOB and Flat C ABI Boundary

- **Status:** Accepted
- **Date:** 2026-11-20
- **Scope:** `engine/`, `services/internal/engine/`

## 1. Context

Order matching is inherently sequential per trading pair: price-time (FIFO) priority requires a total ordering over every order insertion, modification, and cancellation. Multi-threaded order books that protect price levels and order queues with mutexes, spinlocks, or atomic compare-and-swap (CAS) retries suffer from cache-line bouncing, lock convoying, and unpredictable tail latency (`p99.9` > $500\,\mu\text{s}$).

Because each market's Central Limit Order Book executes on a dedicated OS-pinned thread (`runtime.LockOSThread()`), SettleCore requires **zero mutexes, zero spinlocks, and zero atomic CAS contention** on the book—a **single-threaded, zero-lock hot path** rather than a multi-threaded lock-free concurrent data structure.

Furthermore, exposing the matching engine as a separate network microservice over HTTP/JSON or gRPC adds socket syscalls, kernel context switches, and protobuf/JSON serialization overhead ($50\text{--}300\,\mu\text{s}$ per order), dwarfing the sub-microsecond execution of the in-memory data structures.

## 2. Decision

1. **Single-Threaded Deterministic Event Loop per Market:** Each trading pair's Central Limit Order Book (`OrderBook`) executes on a dedicated, single-threaded C++17 state machine with zero internal locks.
2. **Pre-Allocated Arena Pool (Zero Hot-Path Heap Allocations):** At startup, `OrderBook` pre-allocates a contiguous slab of `OrderNode` slots (`ENGINE_MAX_RESTING_ORDERS = 1,048,576`, exactly 64 MB at 64 bytes/node) managed by an intrusive free-list index.
3. **Flat C ABI Bridge (`include/settlecore/c_api.h`):** The C++17 engine compiles into a static library (`libsettlecore_engine.a`) linked directly into the Go 1.27 `gateway` and `replay-cli` binaries via `cgo`. Go passes fixed-size POD structs (`sc_order_cmd_t`, `sc_exec_report_t`) across the C ABI boundary without heap allocation or JSON/protobuf serialization.

## 3. Exact Specification

### 3.1 Cache-Aligned 64-Byte `OrderNode` Layout

Each resting order occupies exactly one 64-byte CPU cache line (`alignas(64)`):

```cpp
struct alignas(64) OrderNode {
    uint64_t order_id;         // [0..7]   Monotonic engine order ID
    uint64_t account_id_low;   // [8..15]  Lower 64 bits of account UUID
    uint64_t account_id_high;  // [16..23] Upper 64 bits of account UUID (used for STP check)
    int64_t  price_1e8;        // [24..31] Limit price in 1e8 scale
    int64_t  remaining_1e8;    // [32..39] Unfilled base quantity in 1e8 scale
    uint64_t timestamp_ns;     // [40..47] Ingress monotonic timestamp
    uint32_t next_idx;         // [48..51] Intrusive doubly-linked FIFO next index (UINT32_MAX = null)
    uint32_t prev_idx;         // [52..55] Intrusive doubly-linked FIFO prev index (UINT32_MAX = null)
    uint16_t market_id;        // [56..57] 1 = ETH/USDC, 2 = BTC/USDC
    uint8_t  side;             // [58]     0 = BUY, 1 = SELL
    uint8_t  order_type;       // [59]     0 = LIMIT, 1 = MARKET
    uint8_t  time_in_force;    // [60]     0 = GTC, 1 = IOC, 2 = FOK
    uint8_t  stp_mode;         // [61]     0 = CANCEL_NEWEST, 1 = CANCEL_OLDEST, 2 = CANCEL_BOTH
    uint16_t reserved_pad;     // [62..63] Explicit alignment padding
};
static_assert(sizeof(OrderNode) == 64, "OrderNode must fit in a single 64-byte cache line");
```

### 3.2 Data Structure Complexity

| Operation | Data Structure | Asymptotic Complexity | Heap Allocations |
| :--- | :--- | :---: | :---: |
| Lookup Order by `order_id` | Open-addressed flat hash table (`order_id -> pool_idx`) | $O(1)$ | 0 |
| Best Bid / Best Ask Lookup | Cached top-of-book pointers + sorted price-level index | $O(1)$ | 0 |
| Append Order to Resting Price Level | Intrusive doubly-linked FIFO queue (`head_idx`, `tail_idx`, `total_volume_1e8`) | $O(1)$ | 0 |
| Cancel Resting Order by `order_id` | Hash lookup $\rightarrow$ intrusive splice (`prev_idx`/`next_idx`) $\rightarrow$ return slot to free-list | $O(1)$ | 0 |
| Match Incoming Aggressive Order | Walk top price levels in priority order; pop resting nodes in FIFO order | $O(K)$ for $K$ fills | 0 |

### 3.3 Flat C ABI Contract (`include/settlecore/c_api.h`)

```c
#ifdef __cplusplus
extern "C" {
#endif

typedef struct sc_engine_handle sc_engine_t;

typedef struct {
    uint64_t wal_seq;
    uint64_t order_id;
    uint64_t account_id_low;
    uint64_t account_id_high;
    int64_t  price_1e8;
    int64_t  qty_1e8;
    uint64_t timestamp_ns;
    uint16_t market_id;
    uint8_t  opcode;         /* 1=SUBMIT, 2=CANCEL, 3=CANCEL_ALL_ACCOUNT */
    uint8_t  side;           /* 0=BUY, 1=SELL */
    uint8_t  order_type;     /* 0=LIMIT, 1=MARKET */
    uint8_t  time_in_force;  /* 0=GTC, 1=IOC, 2=FOK */
    uint8_t  stp_mode;       /* 0=CANCEL_NEWEST, 1=CANCEL_OLDEST, 2=CANCEL_BOTH */
    uint8_t  _pad;
} sc_order_cmd_t;

typedef struct {
    uint64_t trade_id;
    uint64_t maker_order_id;
    uint64_t taker_order_id;
    uint64_t maker_account_low;
    uint64_t maker_account_high;
    int64_t  exec_price_1e8;
    int64_t  fill_qty_1e8;
    int64_t  quote_notional_1e8;
} sc_fill_t;

typedef struct {
    uint64_t wal_seq;
    uint64_t order_id;
    int64_t  filled_qty_1e8;
    int64_t  remaining_qty_1e8;
    uint32_t fill_count;
    uint8_t  status;         /* 0=ACCEPTED, 1=PARTIALLY_FILLED, 2=FILLED, 3=CANCELED, 4=STP_CANCELED, 5=CB_REJECTED */
    int32_t  error_code;
} sc_exec_report_t;

int32_t sc_engine_submit(
    sc_engine_t* engine,
    const sc_order_cmd_t* cmd,
    sc_exec_report_t* out_report,
    sc_fill_t* out_fills_buf,
    uint32_t max_fills
);

#ifdef __cplusplus
}
#endif
```

Go `internal/engine/bridge.go` serializes calls to `sc_engine_submit` through a single pinned goroutine (`runtime.LockOSThread()`) reading from a bounded ring channel, passing a pre-allocated stack/slice buffer of `sc_fill_t` structs.

## 4. Alternatives Considered

1. **Standalone C++ gRPC Microservice:** Rejected. Even over Unix domain sockets, gRPC framing + protobuf serialization + Go/C++ scheduler wakeups add $40\text{--}120\,\mu\text{s}$ per order, which is $40\times$ slower than an in-process C ABI call ($\sim 150\,\text{ns}$ cgo transition in Go 1.27).
2. **Pure Go CLOB:** Rejected. Go 1.27's Green Tea GC reduces garbage-collection overhead, but pointer-rich trees/maps of 1M+ resting orders still incur GC scan pauses or require manual slice-index workarounds without C++17's compile-time `alignas(64)` layout control and deterministic `HdrHistogram_c` integration.
3. **Multi-Threaded Lock-Free SkipList Order Book:** Rejected. CAS retries on the best bid/ask head pointer degrade throughput under concentrated top-of-book contention and complicate deterministic WAL sequence assignment.

## 5. Trade-offs & Failure Modes

- **Cgo Crash Isolation:** A memory corruption bug in C++ would terminate the `gateway` process. We mitigate this by running the C++17 engine test suite under AddressSanitizer and UndefinedBehaviorSanitizer (`cmake --preset asan-ubsan`) in CI and enforcing `-Wall -Wextra -Wpedantic -Werror` plus `.clang-tidy`.
- **Pool Exhaustion (`ENGINE_MAX_RESTING_ORDERS`):** If resting orders reach $1,048,576$, `sc_engine_submit` deterministically returns `SC_ERR_ORDER_POOL_EXHAUSTED` without crashing or allocating on the heap, allowing the gateway to release the pre-trade ledger lock and return HTTP `503 Service Unavailable`.
