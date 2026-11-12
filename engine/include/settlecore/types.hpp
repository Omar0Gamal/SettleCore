#pragma once

#include <cstdint>
#include <limits>
#include <type_traits>

#include "settlecore/c_api.h"

namespace settlecore {

inline constexpr int64_t kScale1e8 = 100'000'000LL;
inline constexpr int64_t kMaxPriceOrQty1e8 = 1'000'000'000'000'000LL;  // 10^15
inline constexpr int64_t kBpsDenom = 10'000LL;
inline constexpr uint32_t kDefaultMaxOrders = 1'048'576U;
inline constexpr int64_t kDefaultCollarBps = 1'000LL;
inline constexpr uint32_t kNullIdx = std::numeric_limits<uint32_t>::max();
inline constexpr uint16_t kMarketEthUsdc = 1U;
inline constexpr uint16_t kMarketBtcUsdc = 2U;

enum class Side : uint8_t {
    BUY = 0,
    SELL = 1,
};

enum class OrderType : uint8_t {
    LIMIT = 0,
    MARKET = 1,
};

enum class TimeInForce : uint8_t {
    GTC = 0,
    IOC = 1,
    FOK = 2,
};

enum class StpMode : uint8_t {
    CANCEL_NEWEST = 0,
    CANCEL_OLDEST = 1,
    CANCEL_BOTH = 2,
};

enum class OrderStatus : uint8_t {
    ACCEPTED = 0,
    PARTIALLY_FILLED = 1,
    FILLED = 2,
    CANCELED = 3,
    STP_CANCELED = 4,
    CB_REJECTED = 5,
    REJECTED = 6,
};

enum class WalOpcode : uint8_t {
    SUBMIT_ORDER = 0x01,
    CANCEL_ORDER = 0x02,
    CANCEL_ALL_ACCOUNT = 0x03,
    UPDATE_MARK_PRICE = 0x04,
    SNAPSHOT_BARRIER = 0x05,
};

enum class WalMode : uint8_t {
    MEMORY = 0,
    BUFFERED = 1,
    FSYNC = 2,
    GROUP_COMMIT = 3,
};

enum ErrorCode : int32_t {
    SC_OK = 0,
    SC_ERR_INVALID_ARG = -1,
    SC_ERR_INVALID_NUMERIC_BOUND = -2,
    SC_ERR_UNKNOWN_MARKET = -3,
    SC_ERR_ORDER_POOL_EXHAUSTED = -4,
    SC_ERR_DUPLICATE_ORDER_ID = -5,
    SC_ERR_ORDER_NOT_FOUND = -6,
    SC_ERR_CIRCUIT_BREAKER_TRIPPED = -7,
    SC_ERR_FOK_UNFILLABLE = -8,
    SC_ERR_IO = -9,
    SC_ERR_CORRUPT_WAL = -10,
    SC_ERR_CORRUPT_SNAPSHOT = -11,
    SC_ERR_OUT_OF_MEMORY = -12,
};

namespace detail {

#if defined(__SIZEOF_INT128__)
__extension__ using Int128 = __int128;
__extension__ using UInt128 = unsigned __int128;
#endif

struct Uint128 {
    uint64_t hi;
    uint64_t lo;
};

inline Uint128 mul_u64_to_u128(uint64_t a, uint64_t b) noexcept {
#if defined(__SIZEOF_INT128__)
    const UInt128 prod = static_cast<UInt128>(a) * static_cast<UInt128>(b);
    return Uint128{static_cast<uint64_t>(prod >> 64U), static_cast<uint64_t>(prod)};
#else
    const uint64_t a_lo = a & 0xFFFFFFFFULL;
    const uint64_t a_hi = a >> 32U;
    const uint64_t b_lo = b & 0xFFFFFFFFULL;
    const uint64_t b_hi = b >> 32U;

    const uint64_t p0 = a_lo * b_lo;
    const uint64_t p1 = a_lo * b_hi;
    const uint64_t p2 = a_hi * b_lo;
    const uint64_t p3 = a_hi * b_hi;

    const uint64_t mid = (p0 >> 32U) + (p1 & 0xFFFFFFFFULL) + (p2 & 0xFFFFFFFFULL);
    const uint64_t lo = (p0 & 0xFFFFFFFFULL) | (mid << 32U);
    const uint64_t hi = p3 + (p1 >> 32U) + (p2 >> 32U) + (mid >> 32U);
    return Uint128{hi, lo};
#endif
}

inline uint64_t div_u128_by_u64(Uint128 num, uint64_t den) noexcept {
    if (num.hi == 0U) {
        return num.lo / den;
    }
#if defined(__SIZEOF_INT128__)
    const UInt128 val = (static_cast<UInt128>(num.hi) << 64U) | static_cast<UInt128>(num.lo);
    return static_cast<uint64_t>(val / den);
#else
    uint64_t rem = num.hi % den;
    uint64_t quot = 0U;
    for (int bit = 63; bit >= 0; --bit) {
        const uint64_t top_bit = (rem >> 63U) & 1ULL;
        rem = (rem << 1U) | ((num.lo >> static_cast<unsigned>(bit)) & 1ULL);
        if (top_bit != 0U || rem >= den) {
            rem -= den;
            quot |= (1ULL << static_cast<unsigned>(bit));
        }
    }
    return quot;
#endif
}

inline Uint128 add_u128_u64(Uint128 v, uint64_t addend) noexcept {
    const uint64_t lo = v.lo + addend;
    const uint64_t carry = (lo < v.lo) ? 1ULL : 0ULL;
    return Uint128{v.hi + carry, lo};
}

}  // namespace detail

// Exact quote notional in 1e8 scale (floor rounding favors buyer reserve safety).
inline int64_t compute_quote_notional(int64_t price_1e8, int64_t qty_1e8) noexcept {
    if (price_1e8 <= 0 || qty_1e8 <= 0) {
        return 0;
    }
#if defined(__SIZEOF_INT128__)
    const detail::Int128 prod =
        static_cast<detail::Int128>(price_1e8) * static_cast<detail::Int128>(qty_1e8);
    return static_cast<int64_t>(prod / kScale1e8);
#else
    const detail::Uint128 prod =
        detail::mul_u64_to_u128(static_cast<uint64_t>(price_1e8), static_cast<uint64_t>(qty_1e8));
    return static_cast<int64_t>(detail::div_u128_by_u64(prod, static_cast<uint64_t>(kScale1e8)));
#endif
}

// Fee in 1e8 scale given fee_bps (1 bps = 0.01% = 1 / 10,000), ceiling-rounded.
inline int64_t compute_fee_ceil(int64_t notional_1e8, int64_t fee_bps) noexcept {
    if (notional_1e8 <= 0 || fee_bps <= 0) {
        return 0;
    }
#if defined(__SIZEOF_INT128__)
    const detail::Int128 num =
        static_cast<detail::Int128>(notional_1e8) * static_cast<detail::Int128>(fee_bps);
    return static_cast<int64_t>((num + 9'999LL) / kBpsDenom);
#else
    const detail::Uint128 prod = detail::mul_u64_to_u128(static_cast<uint64_t>(notional_1e8),
                                                         static_cast<uint64_t>(fee_bps));
    const detail::Uint128 num = detail::add_u128_u64(prod, 9'999ULL);
    return static_cast<int64_t>(detail::div_u128_by_u64(num, static_cast<uint64_t>(kBpsDenom)));
#endif
}

// Multiply price * factor / denom using 128-bit intermediate arithmetic.
inline int64_t mul_div_128(int64_t value, int64_t mul, int64_t denom) noexcept {
    if (value <= 0 || mul <= 0 || denom <= 0) {
        return 0;
    }
#if defined(__SIZEOF_INT128__)
    const detail::Int128 num =
        static_cast<detail::Int128>(value) * static_cast<detail::Int128>(mul);
    return static_cast<int64_t>(num / denom);
#else
    const detail::Uint128 prod =
        detail::mul_u64_to_u128(static_cast<uint64_t>(value), static_cast<uint64_t>(mul));
    return static_cast<int64_t>(detail::div_u128_by_u64(prod, static_cast<uint64_t>(denom)));
#endif
}

inline bool is_valid_price_or_qty(int64_t v) noexcept {
    return v > 0 && v <= kMaxPriceOrQty1e8;
}

struct alignas(64) OrderNode {
    uint64_t order_id;         // [0..7]   Monotonic engine order ID
    uint64_t account_id_low;   // [8..15]  Lower 64 bits of account UUID
    uint64_t account_id_high;  // [16..23] Upper 64 bits of account UUID (STP check)
    int64_t price_1e8;         // [24..31] Limit price in 1e8 scale
    int64_t remaining_1e8;     // [32..39] Unfilled base quantity in 1e8 scale
    uint64_t timestamp_ns;     // [40..47] Ingress monotonic timestamp
    uint32_t next_idx;         // [48..51] Intrusive doubly-linked FIFO next index
    uint32_t prev_idx;         // [52..55] Intrusive doubly-linked FIFO prev index
    uint16_t market_id;        // [56..57] 1 = ETH/USDC, 2 = BTC/USDC
    uint8_t side;              // [58]     0 = BUY, 1 = SELL
    uint8_t order_type;        // [59]     0 = LIMIT, 1 = MARKET
    uint8_t time_in_force;     // [60]     0 = GTC, 1 = IOC, 2 = FOK
    uint8_t stp_mode;          // [61]     0 = CANCEL_NEWEST, 1 = CANCEL_OLDEST, 2 = CANCEL_BOTH
    uint16_t reserved_pad;     // [62..63] Explicit alignment padding
};
static_assert(sizeof(OrderNode) == 64, "OrderNode must fit in a single 64-byte cache line");
static_assert(std::is_trivially_copyable_v<OrderNode>, "OrderNode must be trivially copyable");

struct AccountId {
    uint64_t low = 0;
    uint64_t high = 0;

    bool operator==(const AccountId& other) const noexcept {
        return low == other.low && high == other.high;
    }
    bool operator!=(const AccountId& other) const noexcept { return !(*this == other); }
};

using Fill = sc_fill_t;

struct L2Level {
    int64_t price_1e8 = 0;
    int64_t volume_1e8 = 0;
    uint32_t order_count = 0;
};

struct ExecReport {
    uint64_t wal_seq = 0;
    uint64_t order_id = 0;
    int64_t filled_qty_1e8 = 0;
    int64_t remaining_qty_1e8 = 0;
    uint32_t fill_count = 0;
    OrderStatus status = OrderStatus::ACCEPTED;
    int32_t error_code = SC_OK;
};

}  // namespace settlecore
