#pragma once

#include <array>
#include <cstdint>
#include <map>
#include <memory>
#include <string>
#include <vector>

#include "settlecore/c_api.h"
#include "settlecore/circuit_breaker.hpp"
#include "settlecore/order_book.hpp"
#include "settlecore/types.hpp"
#include "settlecore/wal.hpp"

namespace settlecore {

struct EngineConfig {
    uint32_t max_orders_per_book = kDefaultMaxOrders;
    int64_t cb_collar_bps = kDefaultCollarBps;
    WalMode wal_mode = WalMode::MEMORY;
    std::string wal_path;
    uint32_t group_commit_max_batch = kDefaultGroupCommitMaxBatch;
};

inline constexpr uint16_t kFastMarketArraySize = 16U;

struct MarketSlot {
    uint16_t market_id = 0;
    OrderBook book;
    CircuitBreaker breaker;

    MarketSlot(uint16_t id, uint32_t max_orders, int64_t collar_bps)
        : market_id(id), book(id, max_orders), breaker(collar_bps) {}
};

class MatchingEngine {
  public:
    explicit MatchingEngine(const EngineConfig& config = EngineConfig{});
    ~MatchingEngine() = default;

    MatchingEngine(const MatchingEngine&) = delete;
    MatchingEngine& operator=(const MatchingEngine&) = delete;

    [[nodiscard]] bool is_ok() const noexcept { return init_error_ == SC_OK; }
    [[nodiscard]] int32_t init_error() const noexcept { return init_error_; }

    int32_t ensure_market(uint16_t market_id);
    [[nodiscard]] OrderBook* get_book(uint16_t market_id) noexcept;
    [[nodiscard]] const OrderBook* get_book(uint16_t market_id) const noexcept;
    [[nodiscard]] CircuitBreaker* get_breaker(uint16_t market_id) noexcept;
    [[nodiscard]] const CircuitBreaker* get_breaker(uint16_t market_id) const noexcept;

    int32_t submit(const sc_order_cmd_t& cmd,
                   sc_exec_report_t& out_report,
                   sc_fill_t* out_fills_buf,
                   uint32_t max_fills);

    int32_t submit_batch(const sc_order_cmd_t* cmds,
                         uint32_t cmd_count,
                         sc_exec_report_t* out_reports,
                         sc_fill_t* out_fills_buf,
                         uint32_t max_total_fills,
                         uint32_t* out_total_fills);

    int32_t set_mark_price(uint16_t market_id, int64_t mark_price_1e8, int64_t collar_bps = 0);

    int32_t get_l2_depth(uint16_t market_id,
                         uint32_t max_levels,
                         std::vector<L2Level>& out_bids,
                         std::vector<L2Level>& out_asks) const;

    int32_t replay_wal(const std::string& wal_path,
                       uint64_t min_wal_seq_exclusive = 0,
                       uint64_t* out_last_seq = nullptr,
                       uint64_t* out_replayed_count = nullptr);

    int32_t write_snapshot(const std::string& path) const;
    int32_t load_snapshot(const std::string& path, uint64_t* out_snapshot_seq = nullptr);

    // Deterministic ADR-005 canonical SHA-256 digest of all resting orders across all markets.
    void compute_state_sha256(uint8_t out_hash[32]) const noexcept;

    // Reset all books and counters (used when loading a snapshot).
    void reset_state(uint64_t wal_seq, uint64_t next_trade_id) noexcept;

    [[nodiscard]] uint64_t wal_seq() const noexcept { return wal_seq_; }
    [[nodiscard]] uint64_t next_trade_id() const noexcept { return next_trade_id_; }
    [[nodiscard]] const std::map<uint16_t, MarketSlot*>& markets() const noexcept {
        return markets_;
    }

    // Direct WAL and Event Log access for inspection/testing.
    [[nodiscard]] const WalWriter& wal() const noexcept { return wal_; }
    [[nodiscard]] const EventLogWriter& event_log() const noexcept { return event_log_; }
    int32_t sync_wal() noexcept {
        const int32_t rc1 = wal_.sync();
        const int32_t rc2 = event_log_.sync();
        return (rc1 != SC_OK) ? rc1 : rc2;
    }

  private:
    [[nodiscard]] MarketSlot* get_slot(uint16_t market_id) noexcept {
        if (market_id < kFastMarketArraySize) {
            return fast_markets_[market_id].get();
        }
        auto it = overflow_markets_.find(market_id);
        return (it == overflow_markets_.end()) ? nullptr : it->second.get();
    }

    [[nodiscard]] const MarketSlot* get_slot(uint16_t market_id) const noexcept {
        if (market_id < kFastMarketArraySize) {
            return fast_markets_[market_id].get();
        }
        auto it = overflow_markets_.find(market_id);
        return (it == overflow_markets_.end()) ? nullptr : it->second.get();
    }

    int32_t apply_command(const sc_order_cmd_t& cmd,
                          bool write_wal,
                          sc_exec_report_t& out_report,
                          sc_fill_t* out_fills_buf,
                          uint32_t max_fills);

    void emit_event_records(const sc_order_cmd_t& cmd,
                            uint64_t seq,
                            const OrderBook* book,
                            const sc_exec_report_t& report,
                            const sc_fill_t* fills,
                            uint32_t fill_count,
                            int64_t canceled_qty_or_count);

    EngineConfig config_;
    std::array<std::unique_ptr<MarketSlot>, kFastMarketArraySize> fast_markets_{};
    std::map<uint16_t, std::unique_ptr<MarketSlot>> overflow_markets_;
    std::map<uint16_t, MarketSlot*> markets_;
    WalWriter wal_;
    EventLogWriter event_log_;
    std::vector<Fill> event_fills_scratch_;
    uint64_t wal_seq_ = 0;
    uint64_t next_trade_id_ = 1;
    int32_t init_error_ = SC_OK;
};

// Portable self-contained SHA-256 implementation.
class Sha256 {
  public:
    Sha256() noexcept;
    void update(const uint8_t* data, size_t len) noexcept;
    void finalize(uint8_t out_hash[32]) noexcept;

  private:
    void transform(const uint8_t block[64]) noexcept;

    uint32_t state_[8]{};
    uint64_t bit_len_ = 0;
    std::array<uint8_t, 64> buffer_{};
    size_t buffer_len_ = 0;
};

}  // namespace settlecore
