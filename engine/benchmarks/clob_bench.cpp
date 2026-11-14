#include <benchmark/benchmark.h>

#include <cstdint>
#include <vector>

#include "settlecore/matching_engine.hpp"

namespace settlecore {
namespace {

constexpr uint64_t kBenchSeed = 0x534554544C45434FULL;
constexpr int64_t kMidPriceTicks = 200'000'000'000LL;  // 2,000.00000000
constexpr int64_t kTickStep = 100LL;

class SplitMix64 {
  public:
    explicit SplitMix64(uint64_t seed) noexcept : state_(seed) {}
    uint64_t next() noexcept {
        uint64_t z = (state_ += 0x9e3779b97f4a7c15ULL);
        z = (z ^ (z >> 30U)) * 0xbf58476d1ce4e5b9ULL;
        z = (z ^ (z >> 27U)) * 0x94d049bb133111ebULL;
        return z ^ (z >> 31U);
    }

  private:
    uint64_t state_;
};

void populate_depth(OrderBook& book,
                    uint32_t levels_per_side,
                    SplitMix64& rng,
                    uint64_t& next_oid,
                    uint64_t& next_tid,
                    std::vector<uint64_t>& resting_ids) {
    resting_ids.clear();
    resting_ids.reserve(levels_per_side * 2U);

    for (uint32_t i = 1U; i <= levels_per_side; ++i) {
        const int64_t offset = static_cast<int64_t>(i) * kTickStep;

        OrderSubmitRequest bid{};
        bid.order_id = next_oid++;
        bid.account_id_low = (rng.next() % 1024U) + 1U;
        bid.side = Side::BUY;
        bid.price_1e8 = kMidPriceTicks - offset;
        bid.qty_1e8 = 10LL * kScale1e8;
        bid.order_type = OrderType::LIMIT;
        bid.time_in_force = TimeInForce::GTC;
        book.submit(bid, next_tid, nullptr, nullptr, 0U);
        resting_ids.push_back(bid.order_id);

        OrderSubmitRequest ask{};
        ask.order_id = next_oid++;
        ask.account_id_low = (rng.next() % 1024U) + 1025U;
        ask.side = Side::SELL;
        ask.price_1e8 = kMidPriceTicks + offset;
        ask.qty_1e8 = 10LL * kScale1e8;
        ask.order_type = OrderType::LIMIT;
        ask.time_in_force = TimeInForce::GTC;
        book.submit(ask, next_tid, nullptr, nullptr, 0U);
        resting_ids.push_back(ask.order_id);
    }
}

void BM_W1_BalancedMarketMaker(benchmark::State& state) {
    OrderBook book(kMarketEthUsdc, kDefaultMaxOrders);
    SplitMix64 rng(kBenchSeed);
    uint64_t next_oid = 1U;
    uint64_t next_tid = 1U;
    Fill fills[32]{};
    std::vector<uint64_t> active_ids;
    active_ids.reserve(100'000U);

    // Seed resting depth +-50 ticks
    for (int i = 1; i <= 50; ++i) {
        for (int j = 0; j < 20; ++j) {
            OrderSubmitRequest bid{};
            bid.order_id = next_oid++;
            bid.account_id_low = (rng.next() % 512U) + 1U;
            bid.side = Side::BUY;
            bid.price_1e8 = kMidPriceTicks - static_cast<int64_t>(i) * kTickStep;
            bid.qty_1e8 = kScale1e8;
            book.submit(bid, next_tid, nullptr, nullptr, 0U);
            active_ids.push_back(bid.order_id);

            OrderSubmitRequest ask{};
            ask.order_id = next_oid++;
            ask.account_id_low = (rng.next() % 512U) + 513U;
            ask.side = Side::SELL;
            ask.price_1e8 = kMidPriceTicks + static_cast<int64_t>(i) * kTickStep;
            ask.qty_1e8 = kScale1e8;
            book.submit(ask, next_tid, nullptr, nullptr, 0U);
            active_ids.push_back(ask.order_id);
        }
    }

    int64_t total_orders = 0;
    for (auto _ : state) {
        const uint64_t roll = rng.next() % 100U;
        if (roll < 55U || active_ids.empty()) {
            const bool is_buy = (rng.next() & 1U) == 0U;
            const int64_t spread = static_cast<int64_t>((rng.next() % 50U) + 1U) * kTickStep;
            OrderSubmitRequest req{};
            req.order_id = next_oid++;
            req.account_id_low = (rng.next() % 1024U) + 1U;
            req.side = is_buy ? Side::BUY : Side::SELL;
            req.price_1e8 = is_buy ? (kMidPriceTicks - spread) : (kMidPriceTicks + spread);
            req.qty_1e8 = kScale1e8;
            const auto res = book.submit(req, next_tid, nullptr, fills, 32U);
            if (res.remaining_qty_1e8 > 0) {
                active_ids.push_back(req.order_id);
            }
        } else if (roll < 90U) {
            const size_t pos = static_cast<size_t>(rng.next() % active_ids.size());
            const uint64_t oid = active_ids[pos];
            active_ids[pos] = active_ids.back();
            active_ids.pop_back();
            book.cancel(oid);
        } else {
            const bool is_buy = (rng.next() & 1U) == 0U;
            OrderSubmitRequest req{};
            req.order_id = next_oid++;
            req.account_id_low = (rng.next() % 1024U) + 2048U;
            req.side = is_buy ? Side::BUY : Side::SELL;
            req.price_1e8 = is_buy ? (kMidPriceTicks + 50LL * kTickStep)
                                   : (kMidPriceTicks - 50LL * kTickStep);
            req.qty_1e8 = kScale1e8;
            req.time_in_force = TimeInForce::IOC;
            auto res = book.submit(req, next_tid, nullptr, fills, 32U);
            benchmark::DoNotOptimize(res);
        }
        ++total_orders;

        if (active_ids.size() > 250'000U) {
            for (int c = 0; c < 1000 && !active_ids.empty(); ++c) {
                book.cancel(active_ids.back());
                active_ids.pop_back();
            }
        }
    }
    state.SetItemsProcessed(total_orders);
}

void BM_W2_DepthScaling_LimitInsert(benchmark::State& state) {
    const uint32_t depth = static_cast<uint32_t>(state.range(0));
    OrderBook book(kMarketEthUsdc, kDefaultMaxOrders);
    SplitMix64 rng(kBenchSeed + depth);
    uint64_t next_oid = 1U;
    uint64_t next_tid = 1U;
    std::vector<uint64_t> resting_ids;
    populate_depth(book, depth, rng, next_oid, next_tid, resting_ids);

    int64_t processed = 0;
    for (auto _ : state) {
        state.PauseTiming();
        if (book.active_order_count() >= 900'000U) {
            book.clear();
            populate_depth(book, depth, rng, next_oid, next_tid, resting_ids);
        }
        const bool is_buy = (rng.next() & 1U) == 0U;
        const int64_t level_off =
            static_cast<int64_t>((rng.next() % depth) + 1U) * kTickStep;
        OrderSubmitRequest req{};
        req.order_id = next_oid++;
        req.account_id_low = (rng.next() % 1024U) + 1U;
        req.side = is_buy ? Side::BUY : Side::SELL;
        req.price_1e8 = is_buy ? (kMidPriceTicks - level_off) : (kMidPriceTicks + level_off);
        req.qty_1e8 = kScale1e8;
        state.ResumeTiming();

        auto res = book.submit(req, next_tid, nullptr, nullptr, 0U);
        benchmark::DoNotOptimize(res);
        ++processed;
    }
    state.SetItemsProcessed(processed);
}

void BM_W2_DepthScaling_Cancel(benchmark::State& state) {
    const uint32_t depth = static_cast<uint32_t>(state.range(0));
    OrderBook book(kMarketEthUsdc, kDefaultMaxOrders);
    SplitMix64 rng(kBenchSeed + depth + 1U);
    uint64_t next_oid = 1U;
    uint64_t next_tid = 1U;
    std::vector<uint64_t> resting_ids;
    populate_depth(book, depth, rng, next_oid, next_tid, resting_ids);

    int64_t processed = 0;
    for (auto _ : state) {
        if (resting_ids.empty()) {
            state.PauseTiming();
            populate_depth(book, depth, rng, next_oid, next_tid, resting_ids);
            state.ResumeTiming();
        }
        const uint64_t oid = resting_ids.back();
        resting_ids.pop_back();
        int32_t rc = book.cancel(oid);
        benchmark::DoNotOptimize(rc);
        ++processed;
    }
    state.SetItemsProcessed(processed);
}

void BM_W2_DepthScaling_AggressiveMatch(benchmark::State& state) {
    const uint32_t depth = static_cast<uint32_t>(state.range(0));
    OrderBook book(kMarketEthUsdc, kDefaultMaxOrders);
    SplitMix64 rng(kBenchSeed + depth + 2U);
    uint64_t next_oid = 1U;
    uint64_t next_tid = 1U;
    std::vector<uint64_t> resting_ids;
    populate_depth(book, depth, rng, next_oid, next_tid, resting_ids);
    Fill fills[8]{};

    int64_t processed = 0;
    for (auto _ : state) {
        if (book.best_ask_price() == 0 || book.best_bid_price() == 0) {
            state.PauseTiming();
            book.clear();
            populate_depth(book, depth, rng, next_oid, next_tid, resting_ids);
            state.ResumeTiming();
        }
        const bool is_buy = (rng.next() & 1U) == 0U;
        OrderSubmitRequest req{};
        req.order_id = next_oid++;
        req.account_id_low = 999'999U;
        req.side = is_buy ? Side::BUY : Side::SELL;
        req.order_type = OrderType::MARKET;
        req.time_in_force = TimeInForce::IOC;
        req.qty_1e8 = kScale1e8;
        auto res = book.submit(req, next_tid, nullptr, fills, 8U);
        benchmark::DoNotOptimize(res);
        ++processed;
    }
    state.SetItemsProcessed(processed);
}

void BM_W3_DeepBookSweep(benchmark::State& state) {
    const uint32_t sweep_orders = static_cast<uint32_t>(state.range(0));
    OrderBook book(kMarketEthUsdc, 4096U);
    uint64_t next_oid = 1U;
    uint64_t next_tid = 1U;
    std::vector<Fill> fills(sweep_orders + 4U);

    int64_t processed = 0;
    for (auto _ : state) {
        state.PauseTiming();
        for (uint32_t i = 0U; i < sweep_orders; ++i) {
            OrderSubmitRequest ask{};
            ask.order_id = next_oid++;
            ask.account_id_low = 1U;
            ask.side = Side::SELL;
            ask.price_1e8 = kMidPriceTicks + static_cast<int64_t>(i) * kTickStep;
            ask.qty_1e8 = kScale1e8;
            book.submit(ask, next_tid, nullptr, nullptr, 0U);
        }
        OrderSubmitRequest sweep{};
        sweep.order_id = next_oid++;
        sweep.account_id_low = 2U;
        sweep.side = Side::BUY;
        sweep.order_type = OrderType::MARKET;
        sweep.time_in_force = TimeInForce::IOC;
        sweep.qty_1e8 = static_cast<int64_t>(sweep_orders) * kScale1e8;
        state.ResumeTiming();

        auto res = book.submit(sweep, next_tid, nullptr, fills.data(),
                               static_cast<uint32_t>(fills.size()));
        benchmark::DoNotOptimize(res);
        ++processed;
    }
    state.SetItemsProcessed(processed);
}

BENCHMARK(BM_W1_BalancedMarketMaker);
BENCHMARK(BM_W2_DepthScaling_LimitInsert)->Arg(100)->Arg(1000)->Arg(10000)->Arg(100000);
BENCHMARK(BM_W2_DepthScaling_Cancel)->Arg(100)->Arg(1000)->Arg(10000)->Arg(100000);
BENCHMARK(BM_W2_DepthScaling_AggressiveMatch)->Arg(100)->Arg(1000)->Arg(10000)->Arg(100000);
BENCHMARK(BM_W3_DeepBookSweep)->Arg(1)->Arg(10)->Arg(50)->Arg(250);

}  // namespace
}  // namespace settlecore

BENCHMARK_MAIN();
