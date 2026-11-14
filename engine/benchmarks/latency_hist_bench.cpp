#include <hdr/hdr_histogram.h>

#include <chrono>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <iomanip>
#include <iostream>
#include <string>
#include <vector>

#include "settlecore/matching_engine.hpp"

namespace settlecore {
namespace {

constexpr uint64_t kBenchSeed = 0x534554544C45434FULL;
constexpr int64_t kMidPriceTicks = 200'000'000'000LL;
constexpr int64_t kTickStep = 100LL;
constexpr uint64_t kWarmupOrders = 100'000U;

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

struct ModeMetrics {
    std::string mode_name;
    double p50_us = 0.0;
    double p90_us = 0.0;
    double p99_us = 0.0;
    double p999_us = 0.0;
    double p9999_us = 0.0;
    double co_p50_us = 0.0;
    double co_p90_us = 0.0;
    double co_p99_us = 0.0;
    double co_p999_us = 0.0;
    double co_p9999_us = 0.0;
    double throughput_ops_sec = 0.0;
    uint64_t sample_count = 0U;
    uint64_t co_sample_count = 0U;
};

ModeMetrics run_workload(const std::string& mode_name,
                         WalMode wal_mode,
                         const std::string& wal_path,
                         uint64_t total_orders,
                         int64_t expected_interval_ns,
                         double max_wall_seconds,
                         hdr_histogram* out_raw_hist_copy,
                         hdr_histogram* out_co_hist_copy) {
    if (!wal_path.empty()) {
        std::filesystem::remove(wal_path);
        std::filesystem::remove(wal_path + ".evlog");
    }

    EngineConfig cfg{};
    cfg.max_orders_per_book = kDefaultMaxOrders;
    cfg.cb_collar_bps = kDefaultCollarBps;
    cfg.wal_mode = wal_mode;
    cfg.wal_path = wal_path;
    cfg.group_commit_max_batch = kDefaultGroupCommitMaxBatch;

    MatchingEngine engine(cfg);
    if (!engine.is_ok()) {
        std::cerr << "Failed to initialize MatchingEngine for mode " << mode_name << "\n";
        std::exit(1);
    }

    hdr_histogram* raw_hist = nullptr;
    hdr_histogram* co_hist = nullptr;
    if (hdr_init(10, 10'000'000'000LL, 3, &raw_hist) != 0 ||
        hdr_init(10, 10'000'000'000LL, 3, &co_hist) != 0) {
        std::cerr << "Failed to initialize HdrHistogram\n";
        std::exit(1);
    }

    SplitMix64 rng(kBenchSeed);
    uint64_t next_oid = 1U;
    std::vector<uint64_t> active_ids;
    active_ids.reserve(256'000U);
    sc_exec_report_t report{};
    sc_fill_t fills[32]{};

    // Pre-seed resting orders (+-50 ticks). Keep FSYNC pre-seed compact so physical disk syncs
    // finish promptly while still exercising realistic book resting/matching/canceling.
    const int seed_per_tick = (wal_mode == WalMode::FSYNC) ? 1 : 20;
    for (int i = 1; i <= 50; ++i) {
        for (int j = 0; j < seed_per_tick; ++j) {
            sc_order_cmd_t bid{};
            bid.market_id = kMarketEthUsdc;
            bid.opcode = SC_OP_SUBMIT_ORDER;
            bid.order_id = next_oid++;
            bid.account_id_low = (rng.next() % 512U) + 1U;
            bid.side = SC_SIDE_BUY;
            bid.price_1e8 = kMidPriceTicks - static_cast<int64_t>(i) * kTickStep;
            bid.qty_1e8 = kScale1e8;
            engine.submit(bid, report, nullptr, 0U);
            active_ids.push_back(bid.order_id);

            sc_order_cmd_t ask{};
            ask.market_id = kMarketEthUsdc;
            ask.opcode = SC_OP_SUBMIT_ORDER;
            ask.order_id = next_oid++;
            ask.account_id_low = (rng.next() % 512U) + 513U;
            ask.side = SC_SIDE_SELL;
            ask.price_1e8 = kMidPriceTicks + static_cast<int64_t>(i) * kTickStep;
            ask.qty_1e8 = kScale1e8;
            engine.submit(ask, report, nullptr, 0U);
            active_ids.push_back(ask.order_id);
        }
    }

    uint64_t warmup = kWarmupOrders;
    if (wal_mode == WalMode::FSYNC) {
        warmup = 4U;
    } else if (wal_mode == WalMode::GROUP_COMMIT) {
        warmup = 640U;
    } else if (wal_mode == WalMode::BUFFERED) {
        warmup = 4096U;
    }
    const uint64_t total_iters = warmup + total_orders;
    std::chrono::steady_clock::time_point wall_start{};
    uint64_t measured_orders = 0U;

    for (uint64_t iter = 0U; iter < total_iters; ++iter) {
        if (iter == warmup) {
            hdr_reset(raw_hist);
            hdr_reset(co_hist);
            if (wal_mode == WalMode::GROUP_COMMIT || wal_mode == WalMode::BUFFERED) {
                engine.sync_wal();
            }
            wall_start = std::chrono::steady_clock::now();
        }

        sc_order_cmd_t cmd{};
        cmd.market_id = kMarketEthUsdc;
        const uint64_t roll = rng.next() % 100U;
        if (roll < 55U || active_ids.empty()) {
            const bool is_buy = (rng.next() & 1U) == 0U;
            const int64_t spread = static_cast<int64_t>((rng.next() % 50U) + 1U) * kTickStep;
            cmd.opcode = SC_OP_SUBMIT_ORDER;
            cmd.order_id = next_oid++;
            cmd.account_id_low = (rng.next() % 1024U) + 1U;
            cmd.side = is_buy ? SC_SIDE_BUY : SC_SIDE_SELL;
            cmd.price_1e8 = is_buy ? (kMidPriceTicks - spread) : (kMidPriceTicks + spread);
            cmd.qty_1e8 = kScale1e8;
        } else if (roll < 90U) {
            const size_t pos = static_cast<size_t>(rng.next() % active_ids.size());
            cmd.opcode = SC_OP_CANCEL_ORDER;
            cmd.order_id = active_ids[pos];
            active_ids[pos] = active_ids.back();
            active_ids.pop_back();
        } else {
            const bool is_buy = (rng.next() & 1U) == 0U;
            cmd.opcode = SC_OP_SUBMIT_ORDER;
            cmd.order_id = next_oid++;
            cmd.account_id_low = (rng.next() % 1024U) + 2048U;
            cmd.side = is_buy ? SC_SIDE_BUY : SC_SIDE_SELL;
            cmd.price_1e8 = is_buy ? (kMidPriceTicks + 50LL * kTickStep)
                                   : (kMidPriceTicks - 50LL * kTickStep);
            cmd.qty_1e8 = kScale1e8;
            cmd.time_in_force = SC_TIF_IOC;
        }

        const auto t0 = std::chrono::steady_clock::now();
        engine.submit(cmd, report, fills, 32U);
        const auto t1 = std::chrono::steady_clock::now();

        if (cmd.opcode == SC_OP_SUBMIT_ORDER && report.remaining_qty_1e8 > 0) {
            active_ids.push_back(cmd.order_id);
        }

        if (iter >= warmup) {
            const int64_t service_ns =
                std::chrono::duration_cast<std::chrono::nanoseconds>(t1 - t0).count();
            const int64_t clamped_service =
                service_ns < 10 ? 10 : (service_ns > 10'000'000'000LL ? 10'000'000'000LL : service_ns);
            hdr_record_value(raw_hist, clamped_service);
            hdr_record_corrected_value(co_hist, clamped_service, expected_interval_ns);

            ++measured_orders;
            if (max_wall_seconds > 0.0 && measured_orders >= 64U &&
                std::chrono::duration<double>(t1 - wall_start).count() >= max_wall_seconds) {
                if (wal_mode != WalMode::GROUP_COMMIT ||
                    (measured_orders % kDefaultGroupCommitMaxBatch) == 0U) {
                    break;
                }
            }
        }

        // Prevent unbounded book growth during long 1M+ runs without biasing timing
        if (active_ids.size() > 250'000U) {
            for (int c = 0; c < 1000 && !active_ids.empty(); ++c) {
                sc_order_cmd_t trim{};
                trim.market_id = kMarketEthUsdc;
                trim.opcode = SC_OP_CANCEL_ORDER;
                trim.order_id = active_ids.back();
                active_ids.pop_back();
                engine.submit(trim, report, nullptr, 0U);
            }
        }
    }

    if (wal_mode == WalMode::BUFFERED || wal_mode == WalMode::GROUP_COMMIT) {
        engine.sync_wal();
    }
    const auto wall_end = std::chrono::steady_clock::now();
    const double elapsed_sec =
        std::chrono::duration<double>(wall_end - wall_start).count();

    ModeMetrics m{};
    m.mode_name = mode_name;
    m.p50_us = static_cast<double>(hdr_value_at_percentile(raw_hist, 50.0)) / 1000.0;
    m.p90_us = static_cast<double>(hdr_value_at_percentile(raw_hist, 90.0)) / 1000.0;
    m.p99_us = static_cast<double>(hdr_value_at_percentile(raw_hist, 99.0)) / 1000.0;
    m.p999_us = static_cast<double>(hdr_value_at_percentile(raw_hist, 99.9)) / 1000.0;
    m.p9999_us = static_cast<double>(hdr_value_at_percentile(raw_hist, 99.99)) / 1000.0;
    m.co_p50_us = static_cast<double>(hdr_value_at_percentile(co_hist, 50.0)) / 1000.0;
    m.co_p90_us = static_cast<double>(hdr_value_at_percentile(co_hist, 90.0)) / 1000.0;
    m.co_p99_us = static_cast<double>(hdr_value_at_percentile(co_hist, 99.0)) / 1000.0;
    m.co_p999_us = static_cast<double>(hdr_value_at_percentile(co_hist, 99.9)) / 1000.0;
    m.co_p9999_us = static_cast<double>(hdr_value_at_percentile(co_hist, 99.99)) / 1000.0;
    m.throughput_ops_sec =
        static_cast<double>(measured_orders > 0U ? measured_orders : total_orders) / elapsed_sec;
    m.sample_count = static_cast<uint64_t>(raw_hist->total_count);
    m.co_sample_count = static_cast<uint64_t>(co_hist->total_count);

    if (out_raw_hist_copy != nullptr) {
        hdr_add(out_raw_hist_copy, raw_hist);
    }
    if (out_co_hist_copy != nullptr) {
        hdr_add(out_co_hist_copy, co_hist);
    }

    hdr_close(raw_hist);
    hdr_close(co_hist);
    if (!wal_path.empty()) {
        std::filesystem::remove(wal_path);
        std::filesystem::remove(wal_path + ".evlog");
    }
    return m;
}

}  // namespace
}  // namespace settlecore

int main(int argc, char** argv) {
    uint64_t orders = 1'000'000U;
    std::string hdr_out = "benchmarks/results/latency_percentiles.csv";
    std::string wal_out = "benchmarks/results/wal_overhead.csv";

    for (int i = 1; i < argc; ++i) {
        const std::string arg = argv[i];
        if (arg.rfind("--orders=", 0) == 0) {
            orders = std::strtoull(arg.substr(9).c_str(), nullptr, 10);
        } else if (arg.rfind("--hdr-out=", 0) == 0) {
            hdr_out = arg.substr(10);
        } else if (arg.rfind("--wal-out=", 0) == 0) {
            wal_out = arg.substr(10);
        }
    }

    std::filesystem::create_directories(std::filesystem::path(hdr_out).parent_path());
    std::filesystem::create_directories(std::filesystem::path(wal_out).parent_path());

    hdr_histogram* mem_raw_hist = nullptr;
    hdr_histogram* mem_co_hist = nullptr;
    if (hdr_init(10, 10'000'000'000LL, 3, &mem_raw_hist) != 0 ||
        hdr_init(10, 10'000'000'000LL, 3, &mem_co_hist) != 0) {
        return 1;
    }

    const auto tmp_dir = std::filesystem::temp_directory_path();
    const std::string group_commit_wal_path =
        (tmp_dir / "settlecore_bench_group_commit.wal").string();
    const std::string buffered_wal_path = (tmp_dir / "settlecore_bench_buffered.wal").string();
    const std::string fsync_wal_path = (tmp_dir / "settlecore_bench_fsync.wal").string();

    // 1. In-memory open-loop workload at 500k ops/s target arrival rate (expected_interval = 2000ns)
    const auto m_mem = settlecore::run_workload(
        "in_memory", settlecore::WalMode::MEMORY, "", orders, 2000LL, 0.0, mem_raw_hist,
        mem_co_hist);

    // 2. Group-Commit WAL (max_batch = 64 commands per single os_fdatasync)
    const uint64_t gc_orders = (orders < 100'000U) ? orders : 100'000U;
    const auto m_gc = settlecore::run_workload(
        "group_commit_wal", settlecore::WalMode::GROUP_COMMIT, group_commit_wal_path, gc_orders,
        10'000LL, 6.0, nullptr, nullptr);

    // 3. Buffered WAL (write + batched fdatasync every 1024 records)
    const auto m_buf = settlecore::run_workload(
        "buffered_wal", settlecore::WalMode::BUFFERED, buffered_wal_path, orders, 10'000LL,
        6.0, nullptr, nullptr);

    // 4. Synchronous per-command fdatasync WAL (representative synced sample)
    const uint64_t fsync_orders = (orders < 64U) ? orders : 64U;
    const auto m_fsync = settlecore::run_workload(
        "fsync_wal", settlecore::WalMode::FSYNC, fsync_wal_path, fsync_orders, 200'000'000LL,
        10.0, nullptr, nullptr);

    // Write latency_percentiles.csv (raw service time + open-loop CO-corrected columns)
    {
        std::ofstream ofs(hdr_out, std::ios::trunc);
        ofs << "percentile,latency_ns,latency_us,co_latency_ns,co_latency_us,sample_count,"
               "co_sample_count\n";
        const double pcts[5] = {50.0, 90.0, 99.0, 99.9, 99.99};
        for (double p : pcts) {
            const int64_t ns = hdr_value_at_percentile(mem_raw_hist, p);
            const double us = static_cast<double>(ns) / 1000.0;
            const int64_t co_ns = hdr_value_at_percentile(mem_co_hist, p);
            const double co_us = static_cast<double>(co_ns) / 1000.0;
            ofs << std::fixed << std::setprecision(4) << p << "," << ns << ","
                << std::setprecision(3) << us << "," << co_ns << "," << co_us << ","
                << mem_raw_hist->total_count << "," << mem_co_hist->total_count << "\n";
        }
    }

    // Write wal_overhead.csv (all 4 WAL durability modes)
    {
        std::ofstream ofs(wal_out, std::ios::trunc);
        ofs << "mode,p50_us,p90_us,p99_us,p999_us,co_p99_us,co_p999_us,throughput_ops_sec\n";
        const settlecore::ModeMetrics modes[4] = {m_mem, m_buf, m_gc, m_fsync};
        for (const auto& m : modes) {
            ofs << m.mode_name << "," << std::fixed << std::setprecision(3) << m.p50_us << ","
                << m.p90_us << "," << m.p99_us << "," << m.p999_us << "," << m.co_p99_us << ","
                << m.co_p999_us << "," << std::setprecision(0) << m.throughput_ops_sec << "\n";
        }
    }

    hdr_close(mem_raw_hist);
    hdr_close(mem_co_hist);

    std::cout << "[SettleCore HdrHistogram] " << orders << " orders in-memory (open-loop 500k/s): "
              << "raw_p50=" << m_mem.p50_us << "us, raw_p99=" << m_mem.p99_us
              << "us, raw_p99.9=" << m_mem.p999_us << "us, raw_p99.99=" << m_mem.p9999_us
              << "us | co_p50=" << m_mem.co_p50_us << "us, co_p99=" << m_mem.co_p99_us
              << "us, co_p99.9=" << m_mem.co_p999_us << "us, co_p99.99=" << m_mem.co_p9999_us
              << "us, throughput=" << static_cast<uint64_t>(m_mem.throughput_ops_sec)
              << " ops/sec\n";
    std::cout << "[SettleCore WAL Buffered] (" << m_buf.sample_count
              << " orders) p50=" << m_buf.p50_us << "us, p99=" << m_buf.p99_us
              << "us, p99.9=" << m_buf.p999_us
              << "us, throughput=" << static_cast<uint64_t>(m_buf.throughput_ops_sec)
              << " ops/sec\n";
    std::cout << "[SettleCore WAL Group-Commit] (" << m_gc.sample_count
              << " orders) p50=" << m_gc.p50_us << "us, p99=" << m_gc.p99_us
              << "us, p99.9=" << m_gc.p999_us
              << "us, throughput=" << static_cast<uint64_t>(m_gc.throughput_ops_sec)
              << " ops/sec\n";
    std::cout << "[SettleCore WAL Fsync] (" << m_fsync.sample_count << " synced orders) p50="
              << m_fsync.p50_us << "us, p99=" << m_fsync.p99_us
              << "us, p99.9=" << m_fsync.p999_us
              << "us, throughput=" << static_cast<uint64_t>(m_fsync.throughput_ops_sec)
              << " ops/sec\n";
    return 0;
}
