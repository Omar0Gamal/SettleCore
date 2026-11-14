#include "settlecore/c_api.h"
#include "settlecore/order_book.hpp"
#include "settlecore/wal.hpp"

#include <gtest/gtest.h>

#include <cstdlib>
#include <filesystem>
#include <new>
#include <vector>

#if defined(__SANITIZE_ADDRESS__)
extern "C" int __sanitizer_install_malloc_and_free_hooks(
    void (*malloc_hook)(const volatile void*, std::size_t),
    void (*free_hook)(const volatile void*));
#endif

namespace {
thread_local bool g_track_heap_allocs = false;
thread_local uint64_t g_heap_alloc_count = 0U;

#if defined(__SANITIZE_ADDRESS__)
void sc_asan_malloc_hook(const volatile void*, std::size_t) {
    if (g_track_heap_allocs) {
        ++g_heap_alloc_count;
    }
}

void sc_asan_free_hook(const volatile void*) {}

struct ScAsanHookInstaller {
    ScAsanHookInstaller() {
        (void)__sanitizer_install_malloc_and_free_hooks(&sc_asan_malloc_hook, &sc_asan_free_hook);
    }
};
const ScAsanHookInstaller kScAsanHookInstaller{};
#endif
}  // namespace

#if !defined(__SANITIZE_ADDRESS__)
#if defined(__GNUC__) && !defined(__clang__)
#define SC_NOINLINE __attribute__((noinline))
#pragma GCC diagnostic push
#pragma GCC diagnostic ignored "-Wmismatched-new-delete"
#else
#define SC_NOINLINE
#endif

SC_NOINLINE void* operator new(std::size_t sz) {
    if (g_track_heap_allocs) {
        ++g_heap_alloc_count;
    }
    void* ptr = std::malloc(sz == 0U ? 1U : sz);
    if (ptr == nullptr) {
        throw std::bad_alloc();
    }
    return ptr;
}

SC_NOINLINE void* operator new(std::size_t sz, const std::nothrow_t&) noexcept {
    if (g_track_heap_allocs) {
        ++g_heap_alloc_count;
    }
    return std::malloc(sz == 0U ? 1U : sz);
}

SC_NOINLINE void* operator new[](std::size_t sz) {
    return ::operator new(sz);
}

SC_NOINLINE void* operator new[](std::size_t sz, const std::nothrow_t& nt) noexcept {
    return ::operator new(sz, nt);
}

SC_NOINLINE void* operator new(std::size_t sz, std::align_val_t al) {
    if (g_track_heap_allocs) {
        ++g_heap_alloc_count;
    }
    const std::size_t align = static_cast<std::size_t>(al);
    const std::size_t req = (sz == 0U) ? align : ((sz + align - 1U) / align) * align;
#if defined(_WIN32)
    void* ptr = _aligned_malloc(req, align);
#else
    void* ptr = std::aligned_alloc(align < sizeof(void*) ? sizeof(void*) : align, req);
#endif
    if (ptr == nullptr) {
        throw std::bad_alloc();
    }
    return ptr;
}

SC_NOINLINE void* operator new(std::size_t sz, std::align_val_t al, const std::nothrow_t&) noexcept {
    if (g_track_heap_allocs) {
        ++g_heap_alloc_count;
    }
    const std::size_t align = static_cast<std::size_t>(al);
    const std::size_t req = (sz == 0U) ? align : ((sz + align - 1U) / align) * align;
#if defined(_WIN32)
    return _aligned_malloc(req, align);
#else
    return std::aligned_alloc(align < sizeof(void*) ? sizeof(void*) : align, req);
#endif
}

SC_NOINLINE void* operator new[](std::size_t sz, std::align_val_t al) {
    return ::operator new(sz, al);
}

SC_NOINLINE void* operator new[](std::size_t sz,
                                 std::align_val_t al,
                                 const std::nothrow_t& nt) noexcept {
    return ::operator new(sz, al, nt);
}

SC_NOINLINE void operator delete(void* ptr) noexcept {
    std::free(ptr);
}

SC_NOINLINE void operator delete(void* ptr, std::size_t) noexcept {
    std::free(ptr);
}

SC_NOINLINE void operator delete(void* ptr, const std::nothrow_t&) noexcept {
    std::free(ptr);
}

SC_NOINLINE void operator delete[](void* ptr) noexcept {
    std::free(ptr);
}

SC_NOINLINE void operator delete[](void* ptr, std::size_t) noexcept {
    std::free(ptr);
}

SC_NOINLINE void operator delete[](void* ptr, const std::nothrow_t&) noexcept {
    std::free(ptr);
}

SC_NOINLINE void operator delete(void* ptr, std::align_val_t) noexcept {
#if defined(_WIN32)
    _aligned_free(ptr);
#else
    std::free(ptr);
#endif
}

SC_NOINLINE void operator delete(void* ptr, std::size_t, std::align_val_t al) noexcept {
    ::operator delete(ptr, al);
}

SC_NOINLINE void operator delete(void* ptr,
                                 std::align_val_t al,
                                 const std::nothrow_t&) noexcept {
    ::operator delete(ptr, al);
}

SC_NOINLINE void operator delete[](void* ptr, std::align_val_t al) noexcept {
    ::operator delete(ptr, al);
}

SC_NOINLINE void operator delete[](void* ptr, std::size_t, std::align_val_t al) noexcept {
    ::operator delete(ptr, al);
}

SC_NOINLINE void operator delete[](void* ptr,
                                   std::align_val_t al,
                                   const std::nothrow_t&) noexcept {
    ::operator delete(ptr, al);
}

#if defined(__GNUC__) && !defined(__clang__)
#pragma GCC diagnostic pop
#endif
#endif  // !defined(__SANITIZE_ADDRESS__)

namespace settlecore {
namespace {

OrderSubmitRequest make_req(uint64_t order_id,
                            uint64_t acct,
                            Side side,
                            int64_t price_1e8,
                            int64_t qty_1e8,
                            OrderType type = OrderType::LIMIT,
                            TimeInForce tif = TimeInForce::GTC,
                            StpMode stp = StpMode::CANCEL_NEWEST) {
    OrderSubmitRequest r{};
    r.order_id = order_id;
    r.account_id_low = acct;
    r.account_id_high = 0U;
    r.side = side;
    r.price_1e8 = price_1e8;
    r.qty_1e8 = qty_1e8;
    r.order_type = type;
    r.time_in_force = tif;
    r.stp_mode = stp;
    r.timestamp_ns = order_id * 100U;
    return r;
}

TEST(TypesTest, FixedPoint128BitPrecision) {
    // 10^15 * 10^15 / 10^8 = 10^22 (would overflow int64 without 128-bit, test near max safe)
    // 100,000.00000000 * 1,000.00000000 -> 100,000,000.00000000 (1e16 ticks, well above 32-bit,
    // product = 1e24 > INT64_MAX = 9.22e18)
    const int64_t price = 100'000LL * kScale1e8;
    const int64_t qty = 1'000LL * kScale1e8;
    EXPECT_EQ(compute_quote_notional(price, qty), 100'000'000LL * kScale1e8);

    // Floor rounding check: 1.00000003 * 1.00000003 = 1.0000000600000009 -> 100000006
    EXPECT_EQ(compute_quote_notional(100'000'003LL, 100'000'003LL), 100'000'006LL);

    // Ceiling fee rounding: 10001 notional * 1 bps (1/10000) -> ceil(1.0001) = 2
    EXPECT_EQ(compute_fee_ceil(10'001LL, 1LL), 2LL);
    EXPECT_EQ(compute_fee_ceil(10'000LL, 1LL), 1LL);
    EXPECT_EQ(compute_fee_ceil(0LL, 10LL), 0LL);
}

TEST(OrderBookTest, PriceTimePriorityFifo) {
    OrderBook book(kMarketEthUsdc, 1024U);
    uint64_t tid = 1U;
    Fill fills[16]{};

    // Two sells at 2000, one sell at 1990
    auto r1 = book.submit(make_req(1U, 10U, Side::SELL, 2000LL * kScale1e8, 2LL * kScale1e8),
                          tid, nullptr, fills, 16U);
    auto r2 = book.submit(make_req(2U, 11U, Side::SELL, 2000LL * kScale1e8, 3LL * kScale1e8),
                          tid, nullptr, fills, 16U);
    auto r3 = book.submit(make_req(3U, 12U, Side::SELL, 1990LL * kScale1e8, 1LL * kScale1e8),
                          tid, nullptr, fills, 16U);
    EXPECT_EQ(r1.status, OrderStatus::ACCEPTED);
    EXPECT_EQ(r2.status, OrderStatus::ACCEPTED);
    EXPECT_EQ(r3.status, OrderStatus::ACCEPTED);
    EXPECT_EQ(book.best_ask_price(), 1990LL * kScale1e8);

    // Incoming buy at 2010 for 4 units should fill order 3 (1 @ 1990), then order 1 (2 @ 2000),
    // then 1 unit of order 2 (@ 2000).
    auto buy = book.submit(make_req(4U, 20U, Side::BUY, 2010LL * kScale1e8, 4LL * kScale1e8),
                           tid, nullptr, fills, 16U);
    EXPECT_EQ(buy.error_code, SC_OK);
    EXPECT_EQ(buy.status, OrderStatus::FILLED);
    EXPECT_EQ(buy.filled_qty_1e8, 4LL * kScale1e8);
    EXPECT_EQ(buy.remaining_qty_1e8, 0LL);
    ASSERT_EQ(buy.fill_count, 3U);

    EXPECT_EQ(fills[0].maker_order_id, 3U);
    EXPECT_EQ(fills[0].exec_price_1e8, 1990LL * kScale1e8);
    EXPECT_EQ(fills[0].fill_qty_1e8, 1LL * kScale1e8);

    EXPECT_EQ(fills[1].maker_order_id, 1U);
    EXPECT_EQ(fills[1].exec_price_1e8, 2000LL * kScale1e8);
    EXPECT_EQ(fills[1].fill_qty_1e8, 2LL * kScale1e8);

    EXPECT_EQ(fills[2].maker_order_id, 2U);
    EXPECT_EQ(fills[2].exec_price_1e8, 2000LL * kScale1e8);
    EXPECT_EQ(fills[2].fill_qty_1e8, 1LL * kScale1e8);

    EXPECT_EQ(book.best_ask_price(), 2000LL * kScale1e8);
    EXPECT_EQ(book.best_ask_volume(), 2LL * kScale1e8);
    EXPECT_FALSE(book.contains_order(1U));
    EXPECT_TRUE(book.contains_order(2U));
    EXPECT_FALSE(book.contains_order(3U));
}

TEST(OrderBookTest, IocAndMarketDoNotRest) {
    OrderBook book(kMarketEthUsdc, 1024U);
    uint64_t tid = 1U;
    Fill fills[16]{};

    book.submit(make_req(1U, 10U, Side::SELL, 2000LL * kScale1e8, 2LL * kScale1e8),
                tid, nullptr, fills, 16U);

    // IOC buy for 5 @ 2000 fills 2 and cancels 3.
    auto ioc = book.submit(
        make_req(2U, 20U, Side::BUY, 2000LL * kScale1e8, 5LL * kScale1e8,
                 OrderType::LIMIT, TimeInForce::IOC),
        tid, nullptr, fills, 16U);
    EXPECT_EQ(ioc.status, OrderStatus::CANCELED);
    EXPECT_EQ(ioc.filled_qty_1e8, 2LL * kScale1e8);
    EXPECT_EQ(ioc.remaining_qty_1e8, 0LL);
    EXPECT_EQ(book.active_order_count(), 0U);
    EXPECT_EQ(book.best_bid_price(), 0LL);

    // Market order against empty book cancels immediately.
    auto mkt = book.submit(
        make_req(3U, 20U, Side::BUY, 0LL, 3LL * kScale1e8, OrderType::MARKET, TimeInForce::IOC),
        tid, nullptr, fills, 16U);
    EXPECT_EQ(mkt.status, OrderStatus::CANCELED);
    EXPECT_EQ(mkt.filled_qty_1e8, 0LL);
    EXPECT_EQ(book.active_order_count(), 0U);
}

TEST(OrderBookTest, FokAllOrNothing) {
    OrderBook book(kMarketEthUsdc, 1024U);
    uint64_t tid = 1U;
    Fill fills[16]{};

    book.submit(make_req(1U, 10U, Side::SELL, 2000LL * kScale1e8, 2LL * kScale1e8),
                tid, nullptr, fills, 16U);
    book.submit(make_req(2U, 11U, Side::SELL, 2010LL * kScale1e8, 2LL * kScale1e8),
                tid, nullptr, fills, 16U);

    // FOK buy for 5 @ 2010: only 4 available -> must cancel with zero mutation.
    auto fok_fail = book.submit(
        make_req(3U, 20U, Side::BUY, 2010LL * kScale1e8, 5LL * kScale1e8,
                 OrderType::LIMIT, TimeInForce::FOK),
        tid, nullptr, fills, 16U);
    EXPECT_EQ(fok_fail.error_code, SC_OK);
    EXPECT_EQ(fok_fail.status, OrderStatus::CANCELED);
    EXPECT_EQ(fok_fail.filled_qty_1e8, 0LL);
    EXPECT_EQ(book.active_order_count(), 2U);
    EXPECT_EQ(book.best_ask_volume(), 2LL * kScale1e8);

    // FOK buy for 4 @ 2010: fills completely across both levels.
    auto fok_ok = book.submit(
        make_req(4U, 20U, Side::BUY, 2010LL * kScale1e8, 4LL * kScale1e8,
                 OrderType::LIMIT, TimeInForce::FOK),
        tid, nullptr, fills, 16U);
    EXPECT_EQ(fok_ok.status, OrderStatus::FILLED);
    EXPECT_EQ(fok_ok.filled_qty_1e8, 4LL * kScale1e8);
    EXPECT_EQ(fok_ok.fill_count, 2U);
    EXPECT_EQ(book.active_order_count(), 0U);
}

TEST(OrderBookTest, CancelAndCancelAllAccount) {
    OrderBook book(kMarketEthUsdc, 1024U);
    uint64_t tid = 1U;
    Fill fills[16]{};

    // Three orders at same price for account 10, plus one at another price for account 10,
    // plus one for account 99.
    book.submit(make_req(1U, 10U, Side::BUY, 1900LL * kScale1e8, 1LL * kScale1e8),
                tid, nullptr, fills, 16U);
    book.submit(make_req(2U, 10U, Side::BUY, 1900LL * kScale1e8, 2LL * kScale1e8),
                tid, nullptr, fills, 16U);
    book.submit(make_req(3U, 99U, Side::BUY, 1900LL * kScale1e8, 3LL * kScale1e8),
                tid, nullptr, fills, 16U);
    book.submit(make_req(4U, 10U, Side::BUY, 1910LL * kScale1e8, 4LL * kScale1e8),
                tid, nullptr, fills, 16U);

    EXPECT_EQ(book.best_bid_price(), 1910LL * kScale1e8);

    // Cancel middle order #2
    int64_t canceled_qty = 0;
    EXPECT_EQ(book.cancel(2U, &canceled_qty), SC_OK);
    EXPECT_EQ(canceled_qty, 2LL * kScale1e8);
    EXPECT_EQ(book.cancel(2U), SC_ERR_ORDER_NOT_FOUND);

    // Cancel all remaining orders of account 10 (orders #1 and #4)
    const uint32_t removed = book.cancel_all_account(10U, 0U);
    EXPECT_EQ(removed, 2U);
    EXPECT_EQ(book.active_order_count(), 1U);
    EXPECT_TRUE(book.contains_order(3U));
    EXPECT_EQ(book.best_bid_price(), 1900LL * kScale1e8);
    EXPECT_EQ(book.best_bid_volume(), 3LL * kScale1e8);
}

TEST(OrderBookTest, L2DepthAndValidationBounds) {
    OrderBook book(kMarketEthUsdc, 8U);  // clamped to 16
    uint64_t tid = 1U;

    EXPECT_EQ(book.submit(make_req(0U, 1U, Side::BUY, 100LL, 100LL), tid, nullptr, nullptr, 0U)
                  .error_code,
              SC_ERR_INVALID_ARG);
    EXPECT_EQ(book.submit(make_req(1U, 1U, Side::BUY, -5LL, 100LL), tid, nullptr, nullptr, 0U)
                  .error_code,
              SC_ERR_INVALID_NUMERIC_BOUND);
    EXPECT_EQ(
        book.submit(make_req(1U, 1U, Side::BUY, 100LL, kMaxPriceOrQty1e8 + 1LL),
                    tid, nullptr, nullptr, 0U)
            .error_code,
        SC_ERR_INVALID_NUMERIC_BOUND);

    book.submit(make_req(1U, 1U, Side::BUY, 100LL * kScale1e8, 1LL * kScale1e8),
                tid, nullptr, nullptr, 0U);
    book.submit(make_req(2U, 2U, Side::BUY, 100LL * kScale1e8, 2LL * kScale1e8),
                tid, nullptr, nullptr, 0U);
    book.submit(make_req(3U, 3U, Side::BUY, 105LL * kScale1e8, 5LL * kScale1e8),
                tid, nullptr, nullptr, 0U);
    book.submit(make_req(4U, 4U, Side::SELL, 110LL * kScale1e8, 4LL * kScale1e8),
                tid, nullptr, nullptr, 0U);

    // Duplicate order ID
    EXPECT_EQ(
        book.submit(make_req(1U, 1U, Side::BUY, 100LL * kScale1e8, 1LL * kScale1e8),
                    tid, nullptr, nullptr, 0U)
            .error_code,
        SC_ERR_DUPLICATE_ORDER_ID);

    std::vector<L2Level> bids;
    std::vector<L2Level> asks;
    book.get_l2_depth(10U, bids, asks);
    ASSERT_EQ(bids.size(), 2U);
    EXPECT_EQ(bids[0].price_1e8, 105LL * kScale1e8);
    EXPECT_EQ(bids[0].volume_1e8, 5LL * kScale1e8);
    EXPECT_EQ(bids[1].price_1e8, 100LL * kScale1e8);
    EXPECT_EQ(bids[1].volume_1e8, 3LL * kScale1e8);
    EXPECT_EQ(bids[1].order_count, 2U);

    ASSERT_EQ(asks.size(), 1U);
    EXPECT_EQ(asks[0].price_1e8, 110LL * kScale1e8);
}

TEST(OrderBookTest, PoolExhaustionReturnsErrorCleanly) {
    OrderBook book(kMarketEthUsdc, 16U);
    uint64_t tid = 1U;
    for (uint64_t i = 1U; i <= 16U; ++i) {
        auto r = book.submit(make_req(i, i, Side::BUY, static_cast<int64_t>(100U + i) * kScale1e8,
                                      1LL * kScale1e8),
                             tid, nullptr, nullptr, 0U);
        EXPECT_EQ(r.error_code, SC_OK);
    }
    auto overflow =
        book.submit(make_req(17U, 17U, Side::BUY, 90LL * kScale1e8, 1LL * kScale1e8),
                    tid, nullptr, nullptr, 0U);
    EXPECT_EQ(overflow.error_code, SC_ERR_ORDER_POOL_EXHAUSTED);
    EXPECT_EQ(book.active_order_count(), 16U);
}

TEST(OrderBookTest, ZeroHeapGrowthAcross50kCycles) {
    OrderBook book(kMarketEthUsdc, 4096U);
    uint64_t tid = 1U;
    Fill fills[32]{};
    std::vector<uint64_t> active_ids;
    active_ids.reserve(2048U);

    uint64_t next_oid = 1U;
    // Pre-populate 200 resting orders so price levels & hash tables are warmed up.
    for (int i = 1; i <= 100; ++i) {
        auto rb = book.submit(
            make_req(next_oid, static_cast<uint64_t>(i % 64) + 1U, Side::BUY,
                     static_cast<int64_t>(1900 + (i % 20)) * kScale1e8, 2LL * kScale1e8),
            tid, nullptr, fills, 32U);
        ASSERT_EQ(rb.error_code, SC_OK);
        active_ids.push_back(next_oid++);

        auto ra = book.submit(
            make_req(next_oid, static_cast<uint64_t>(i % 64) + 101U, Side::SELL,
                     static_cast<int64_t>(2010 + (i % 20)) * kScale1e8, 2LL * kScale1e8),
            tid, nullptr, fills, 32U);
        ASSERT_EQ(ra.error_code, SC_OK);
        active_ids.push_back(next_oid++);
    }

    g_heap_alloc_count = 0U;
    g_track_heap_allocs = true;

    uint64_t rng = 0x123456789ABCDEF0ULL;
    for (uint32_t cycle = 0U; cycle < 50'000U; ++cycle) {
        rng = rng * 6364136223846793005ULL + 1442695040888963407ULL;
        if (active_ids.size() < 512U) {
            const bool is_buy = (cycle & 1U) == 0U;
            const int64_t px = is_buy ? static_cast<int64_t>(1900U + (rng % 30U)) * kScale1e8
                                      : static_cast<int64_t>(2000U + (rng % 30U)) * kScale1e8;
            const uint64_t oid = next_oid++;
            const auto res = book.submit(
                make_req(oid, (rng % 64U) + (is_buy ? 1U : 101U),
                         is_buy ? Side::BUY : Side::SELL, px, 2LL * kScale1e8),
                tid, nullptr, fills, 32U);
            ASSERT_EQ(res.error_code, SC_OK);
            if (res.remaining_qty_1e8 > 0) {
                active_ids.push_back(oid);
            }
        } else if ((cycle % 3U) == 0U) {
            // Aggressive crossing order to trigger matches and node reuse.
            const bool is_buy = (rng & 1U) == 0U;
            const int64_t px = is_buy ? (2040LL * kScale1e8) : (1890LL * kScale1e8);
            const auto res = book.submit(
                make_req(next_oid++, 9999U, is_buy ? Side::BUY : Side::SELL, px, 1LL * kScale1e8,
                         OrderType::LIMIT, TimeInForce::IOC),
                tid, nullptr, fills, 32U);
            ASSERT_EQ(res.error_code, SC_OK);
        } else {
            // Cancel an existing resting order (may already have been matched, which is valid).
            const size_t pos = static_cast<size_t>(rng % active_ids.size());
            const uint64_t oid = active_ids[pos];
            active_ids[pos] = active_ids.back();
            active_ids.pop_back();
            (void)book.cancel(oid);
        }
    }

    // Drain remaining resting orders back into the slab free-list.
    while (!active_ids.empty()) {
        (void)book.cancel(active_ids.back());
        active_ids.pop_back();
    }

    g_track_heap_allocs = false;
    EXPECT_EQ(g_heap_alloc_count, 0U);
    EXPECT_EQ(book.active_order_count(), 0U);
}

TEST(OrderBookTest, SubmitBatchCAbiAndGroupCommit) {
    const std::filesystem::path wal_path =
        std::filesystem::temp_directory_path() / "settlecore_test_batch_gc.wal";
    const std::filesystem::path ev_path =
        std::filesystem::temp_directory_path() / "settlecore_test_batch_gc.wal.evlog";
    std::filesystem::remove(wal_path);
    std::filesystem::remove(ev_path);

    sc_engine_config_t cfg{};
    cfg.max_orders_per_book = 1024U;
    cfg.cb_collar_bps = 1000LL;
    cfg.wal_mode = SC_WAL_GROUP_COMMIT;
    const std::string wal_str = wal_path.string();
    cfg.wal_path = wal_str.c_str();

    sc_engine_t* engine = sc_engine_create(&cfg);
    ASSERT_NE(engine, nullptr);

    sc_order_cmd_t cmds[4]{};
    // 1. Resting ask 2 @ 2500
    cmds[0].market_id = kMarketEthUsdc;
    cmds[0].opcode = SC_OP_SUBMIT_ORDER;
    cmds[0].order_id = 101U;
    cmds[0].account_id_low = 1U;
    cmds[0].side = SC_SIDE_SELL;
    cmds[0].price_1e8 = 2500LL * kScale1e8;
    cmds[0].qty_1e8 = 2LL * kScale1e8;

    // 2. Resting ask 2 @ 2510
    cmds[1].market_id = kMarketEthUsdc;
    cmds[1].opcode = SC_OP_SUBMIT_ORDER;
    cmds[1].order_id = 102U;
    cmds[1].account_id_low = 2U;
    cmds[1].side = SC_SIDE_SELL;
    cmds[1].price_1e8 = 2510LL * kScale1e8;
    cmds[1].qty_1e8 = 2LL * kScale1e8;

    // 3. Aggressive buy 3 @ 2510 (fills 2 @ 2500 + 1 @ 2510)
    cmds[2].market_id = kMarketEthUsdc;
    cmds[2].opcode = SC_OP_SUBMIT_ORDER;
    cmds[2].order_id = 103U;
    cmds[2].account_id_low = 3U;
    cmds[2].side = SC_SIDE_BUY;
    cmds[2].price_1e8 = 2510LL * kScale1e8;
    cmds[2].qty_1e8 = 3LL * kScale1e8;

    // 4. Cancel remaining ask #102
    cmds[3].market_id = kMarketEthUsdc;
    cmds[3].opcode = SC_OP_CANCEL_ORDER;
    cmds[3].order_id = 102U;

    sc_exec_report_t reports[4]{};
    sc_fill_t fills[8]{};
    uint32_t total_fills = 0U;
    ASSERT_EQ(sc_engine_submit_batch(engine, cmds, 4U, reports, fills, 8U, &total_fills), SC_C_OK);

    EXPECT_EQ(total_fills, 2U);
    EXPECT_EQ(reports[0].status, SC_STATUS_ACCEPTED);
    EXPECT_EQ(reports[1].status, SC_STATUS_ACCEPTED);
    EXPECT_EQ(reports[2].status, SC_STATUS_FILLED);
    EXPECT_EQ(reports[2].fill_count, 2U);
    EXPECT_EQ(reports[3].status, SC_STATUS_CANCELED);
    EXPECT_EQ(fills[0].maker_order_id, 101U);
    EXPECT_EQ(fills[1].maker_order_id, 102U);

    // Because sc_engine_submit_batch triggers GROUP_COMMIT sync on the batch,
    // both WAL and .evlog records are already durable on disk before destroy.
    std::vector<WalRecord> wal_recs;
    ASSERT_EQ(WalReader::read_all(wal_str, wal_recs, false, nullptr), SC_OK);
    EXPECT_EQ(wal_recs.size(), 4U);

    std::vector<WalEventRecord> ev_recs;
    ASSERT_EQ(read_event_log(ev_path.string(), ev_recs, false, nullptr), SC_OK);
    EXPECT_EQ(ev_recs.size(), 5U);  // 2 accepts + 2 fills + 1 cancel

    sc_engine_destroy(engine);
    std::filesystem::remove(wal_path);
    std::filesystem::remove(ev_path);
}

}  // namespace
}  // namespace settlecore
