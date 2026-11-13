#include "settlecore/circuit_breaker.hpp"

#include <algorithm>

namespace settlecore {

CircuitBreaker::CircuitBreaker(int64_t collar_bps) noexcept : collar_bps_(collar_bps) {
    if (collar_bps_ <= 0) {
        collar_bps_ = kDefaultCollarBps;
    }
    recompute_bounds();
}

void CircuitBreaker::set_mark_price(int64_t mark_price_1e8) noexcept {
    mark_price_1e8_ = (mark_price_1e8 > 0) ? mark_price_1e8 : 0;
    recompute_bounds();
}

void CircuitBreaker::set_collar_bps(int64_t collar_bps) noexcept {
    collar_bps_ = (collar_bps > 0) ? collar_bps : kDefaultCollarBps;
    recompute_bounds();
}

void CircuitBreaker::reset() noexcept {
    mark_price_1e8_ = 0;
    recompute_bounds();
}

bool CircuitBreaker::is_price_allowed(int64_t exec_price_1e8) const noexcept {
    if (mark_price_1e8_ <= 0) {
        return true;
    }
    return exec_price_1e8 >= min_price_1e8_ && exec_price_1e8 <= max_price_1e8_;
}

void CircuitBreaker::recompute_bounds() noexcept {
    if (mark_price_1e8_ <= 0) {
        min_price_1e8_ = 0;
        max_price_1e8_ = kMaxPriceOrQty1e8;
        return;
    }
    const int64_t clamped_bps = std::min(collar_bps_, kBpsDenom);
    min_price_1e8_ = mul_div_128(mark_price_1e8_, kBpsDenom - clamped_bps, kBpsDenom);
    max_price_1e8_ = mul_div_128(mark_price_1e8_, kBpsDenom + collar_bps_, kBpsDenom);
}

}  // namespace settlecore
