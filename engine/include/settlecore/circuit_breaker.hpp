#pragma once

#include <cstdint>

#include "settlecore/types.hpp"

namespace settlecore {

// Per-market price collar protecting against aggressive execution beyond +-collar_bps
// of the reference mark price (default 1000 bps = +-10%).
class CircuitBreaker {
  public:
    explicit CircuitBreaker(int64_t collar_bps = kDefaultCollarBps) noexcept;

    void set_mark_price(int64_t mark_price_1e8) noexcept;
    void set_collar_bps(int64_t collar_bps) noexcept;
    void reset() noexcept;

    [[nodiscard]] bool is_active() const noexcept { return mark_price_1e8_ > 0; }
    [[nodiscard]] int64_t mark_price() const noexcept { return mark_price_1e8_; }
    [[nodiscard]] int64_t collar_bps() const noexcept { return collar_bps_; }
    [[nodiscard]] int64_t min_price() const noexcept { return min_price_1e8_; }
    [[nodiscard]] int64_t max_price() const noexcept { return max_price_1e8_; }

    // Returns true if executing at exec_price_1e8 is permitted within [min_price, max_price].
    [[nodiscard]] bool is_price_allowed(int64_t exec_price_1e8) const noexcept;

  private:
    void recompute_bounds() noexcept;

    int64_t mark_price_1e8_ = 0;
    int64_t collar_bps_ = kDefaultCollarBps;
    int64_t min_price_1e8_ = 0;
    int64_t max_price_1e8_ = kMaxPriceOrQty1e8;
};

}  // namespace settlecore
