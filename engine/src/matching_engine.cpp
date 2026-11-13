#include "settlecore/matching_engine.hpp"

#include <cstring>

#include "settlecore/snapshot.hpp"

namespace settlecore {

namespace {

constexpr std::array<uint32_t, 64> kSha256K = {
    0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U, 0x3956c25bU, 0x59f111f1U, 0x923f82a4U,
    0xab1c5ed5U, 0xd807aa98U, 0x12835b01U, 0x243185beU, 0x550c7dc3U, 0x72be5d74U, 0x80deb1feU,
    0x9bdc06a7U, 0xc19bf174U, 0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU, 0x2de92c6fU,
    0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU, 0x983e5152U, 0xa831c66dU, 0xb00327c8U, 0xbf597fc7U,
    0xc6e00bf3U, 0xd5a79147U, 0x06ca6351U, 0x14292967U, 0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU,
    0x53380d13U, 0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U, 0xa2bfe8a1U, 0xa81a664bU,
    0xc24b8b70U, 0xc76c51a3U, 0xd192e819U, 0xd6990624U, 0xf40e3585U, 0x106aa070U, 0x19a4c116U,
    0x1e376c08U, 0x2748774cU, 0x34b0bcb5U, 0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU, 0x682e6ff3U,
    0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U, 0x90befffaU, 0xa4506cebU, 0xbef9a3f7U,
    0xc67178f2U,
};

inline uint32_t rotr(uint32_t x, uint32_t n) noexcept {
    return (x >> n) | (x << (32U - n));
}

inline void write_le64(uint8_t* dst, uint64_t val) noexcept {
    dst[0] = static_cast<uint8_t>(val & 0xFFU);
    dst[1] = static_cast<uint8_t>((val >> 8U) & 0xFFU);
    dst[2] = static_cast<uint8_t>((val >> 16U) & 0xFFU);
    dst[3] = static_cast<uint8_t>((val >> 24U) & 0xFFU);
    dst[4] = static_cast<uint8_t>((val >> 32U) & 0xFFU);
    dst[5] = static_cast<uint8_t>((val >> 40U) & 0xFFU);
    dst[6] = static_cast<uint8_t>((val >> 48U) & 0xFFU);
    dst[7] = static_cast<uint8_t>((val >> 56U) & 0xFFU);
}

}  // namespace

// -----------------------------------------------------------------------------
// Portable SHA-256 (FIPS 180-4)
// -----------------------------------------------------------------------------

Sha256::Sha256() noexcept {
    state_[0] = 0x6a09e667U;
    state_[1] = 0xbb67ae85U;
    state_[2] = 0x3c6ef372U;
    state_[3] = 0xa54ff53aU;
    state_[4] = 0x510e527fU;
    state_[5] = 0x9b05688cU;
    state_[6] = 0x1f83d9abU;
    state_[7] = 0x5be0cd19U;
}

void Sha256::transform(const uint8_t block[64]) noexcept {
    uint32_t w[64]{};
    for (size_t i = 0U; i < 16U; ++i) {
        w[i] = (static_cast<uint32_t>(block[i * 4U]) << 24U) |
               (static_cast<uint32_t>(block[i * 4U + 1U]) << 16U) |
               (static_cast<uint32_t>(block[i * 4U + 2U]) << 8U) |
               static_cast<uint32_t>(block[i * 4U + 3U]);
    }
    for (size_t i = 16U; i < 64U; ++i) {
        const uint32_t s0 = rotr(w[i - 15U], 7U) ^ rotr(w[i - 15U], 18U) ^ (w[i - 15U] >> 3U);
        const uint32_t s1 = rotr(w[i - 2U], 17U) ^ rotr(w[i - 2U], 19U) ^ (w[i - 2U] >> 10U);
        w[i] = w[i - 16U] + s0 + w[i - 7U] + s1;
    }

    uint32_t a = state_[0];
    uint32_t b = state_[1];
    uint32_t c = state_[2];
    uint32_t d = state_[3];
    uint32_t e = state_[4];
    uint32_t f = state_[5];
    uint32_t g = state_[6];
    uint32_t h = state_[7];

    for (size_t i = 0U; i < 64U; ++i) {
        const uint32_t s1 = rotr(e, 6U) ^ rotr(e, 11U) ^ rotr(e, 25U);
        const uint32_t ch = (e & f) ^ ((~e) & g);
        const uint32_t temp1 = h + s1 + ch + kSha256K[i] + w[i];
        const uint32_t s0 = rotr(a, 2U) ^ rotr(a, 13U) ^ rotr(a, 22U);
        const uint32_t maj = (a & b) ^ (a & c) ^ (b & c);
        const uint32_t temp2 = s0 + maj;

        h = g;
        g = f;
        f = e;
        e = d + temp1;
        d = c;
        c = b;
        b = a;
        a = temp1 + temp2;
    }

    state_[0] += a;
    state_[1] += b;
    state_[2] += c;
    state_[3] += d;
    state_[4] += e;
    state_[5] += f;
    state_[6] += g;
    state_[7] += h;
}

void Sha256::update(const uint8_t* data, size_t len) noexcept {
    bit_len_ += static_cast<uint64_t>(len) * 8ULL;
    size_t offset = 0U;
    if (buffer_len_ > 0U) {
        const size_t take = std::min(len, 64U - buffer_len_);
        std::memcpy(buffer_.data() + buffer_len_, data, take);
        buffer_len_ += take;
        offset += take;
        if (buffer_len_ == 64U) {
            transform(buffer_.data());
            buffer_len_ = 0U;
        }
    }
    while (offset + 64U <= len) {
        transform(data + offset);
        offset += 64U;
    }
    if (offset < len) {
        const size_t rem = len - offset;
        std::memcpy(buffer_.data(), data + offset, rem);
        buffer_len_ = rem;
    }
}

void Sha256::finalize(uint8_t out_hash[32]) noexcept {
    buffer_[buffer_len_++] = 0x80U;
    if (buffer_len_ > 56U) {
        while (buffer_len_ < 64U) {
            buffer_[buffer_len_++] = 0U;
        }
        transform(buffer_.data());
        buffer_len_ = 0U;
    }
    while (buffer_len_ < 56U) {
        buffer_[buffer_len_++] = 0U;
    }
    for (int i = 7; i >= 0; --i) {
        buffer_[buffer_len_++] = static_cast<uint8_t>((bit_len_ >> (static_cast<unsigned>(i) * 8U)) & 0xFFU);
    }
    transform(buffer_.data());

    for (size_t i = 0U; i < 8U; ++i) {
        out_hash[i * 4U] = static_cast<uint8_t>((state_[i] >> 24U) & 0xFFU);
        out_hash[i * 4U + 1U] = static_cast<uint8_t>((state_[i] >> 16U) & 0xFFU);
        out_hash[i * 4U + 2U] = static_cast<uint8_t>((state_[i] >> 8U) & 0xFFU);
        out_hash[i * 4U + 3U] = static_cast<uint8_t>(state_[i] & 0xFFU);
    }
}

// -----------------------------------------------------------------------------
// MatchingEngine
// -----------------------------------------------------------------------------

MatchingEngine::MatchingEngine(const EngineConfig& config) : config_(config) {
    if (config_.max_orders_per_book == 0U) {
        config_.max_orders_per_book = kDefaultMaxOrders;
    }
    if (config_.cb_collar_bps <= 0) {
        config_.cb_collar_bps = kDefaultCollarBps;
    }
    if (config_.group_commit_max_batch == 0U) {
        config_.group_commit_max_batch = kDefaultGroupCommitMaxBatch;
    }

    ensure_market(kMarketEthUsdc);
    ensure_market(kMarketBtcUsdc);

    if (config_.wal_mode != WalMode::MEMORY && !config_.wal_path.empty()) {
        init_error_ = wal_.open(config_.wal_path, config_.wal_mode, true, nullptr,
                                config_.group_commit_max_batch);
        if (init_error_ == SC_OK) {
            init_error_ = event_log_.open(config_.wal_path + ".evlog", config_.wal_mode, true,
                                          nullptr, config_.group_commit_max_batch);
        }
        if (init_error_ == SC_OK) {
            event_fills_scratch_.resize(4096U);
        }
    }
}

int32_t MatchingEngine::ensure_market(uint16_t market_id) {
    if (market_id == 0U) {
        return SC_ERR_UNKNOWN_MARKET;
    }
    if (get_slot(market_id) == nullptr) {
        auto slot = std::make_unique<MarketSlot>(
            market_id, config_.max_orders_per_book, config_.cb_collar_bps);
        MarketSlot* raw_ptr = slot.get();
        if (market_id < kFastMarketArraySize) {
            fast_markets_[market_id] = std::move(slot);
        } else {
            overflow_markets_.emplace(market_id, std::move(slot));
        }
        markets_.emplace(market_id, raw_ptr);
    }
    return SC_OK;
}

OrderBook* MatchingEngine::get_book(uint16_t market_id) noexcept {
    MarketSlot* slot = get_slot(market_id);
    return (slot == nullptr) ? nullptr : &slot->book;
}

const OrderBook* MatchingEngine::get_book(uint16_t market_id) const noexcept {
    const MarketSlot* slot = get_slot(market_id);
    return (slot == nullptr) ? nullptr : &slot->book;
}

CircuitBreaker* MatchingEngine::get_breaker(uint16_t market_id) noexcept {
    MarketSlot* slot = get_slot(market_id);
    return (slot == nullptr) ? nullptr : &slot->breaker;
}

const CircuitBreaker* MatchingEngine::get_breaker(uint16_t market_id) const noexcept {
    const MarketSlot* slot = get_slot(market_id);
    return (slot == nullptr) ? nullptr : &slot->breaker;
}

int32_t MatchingEngine::submit(const sc_order_cmd_t& cmd,
                               sc_exec_report_t& out_report,
                               sc_fill_t* out_fills_buf,
                               uint32_t max_fills) {
    return apply_command(cmd, true, out_report, out_fills_buf, max_fills);
}

int32_t MatchingEngine::submit_batch(const sc_order_cmd_t* cmds,
                                     uint32_t cmd_count,
                                     sc_exec_report_t* out_reports,
                                     sc_fill_t* out_fills_buf,
                                     uint32_t max_total_fills,
                                     uint32_t* out_total_fills) {
    if (out_total_fills != nullptr) {
        *out_total_fills = 0U;
    }
    if (cmd_count == 0U) {
        return SC_OK;
    }
    if (cmds == nullptr || out_reports == nullptr) {
        return SC_ERR_INVALID_ARG;
    }

    uint32_t fills_written = 0U;
    int32_t first_io_err = SC_OK;
    for (uint32_t i = 0U; i < cmd_count; ++i) {
        sc_fill_t* fill_dst = nullptr;
        uint32_t avail_fills = 0U;
        if (out_fills_buf != nullptr && fills_written < max_total_fills) {
            fill_dst = out_fills_buf + fills_written;
            avail_fills = max_total_fills - fills_written;
        }
        const int32_t rc = apply_command(cmds[i], true, out_reports[i], fill_dst, avail_fills);
        fills_written += out_reports[i].fill_count;
        if (rc == SC_ERR_IO && first_io_err == SC_OK) {
            first_io_err = rc;
        }
    }

    if (out_total_fills != nullptr) {
        *out_total_fills = fills_written;
    }

    if (first_io_err != SC_OK) {
        return first_io_err;
    }

    if (wal_.mode() == WalMode::GROUP_COMMIT) {
        return sync_wal();
    }
    return SC_OK;
}

void MatchingEngine::emit_event_records(const sc_order_cmd_t& cmd,
                                        uint64_t seq,
                                        const OrderBook* book,
                                        const sc_exec_report_t& report,
                                        const sc_fill_t* fills,
                                        uint32_t fill_count,
                                        int64_t canceled_qty_or_count) {
    const uint8_t side_flag = (cmd.side == SC_SIDE_SELL) ? kEventFlagSideSell : 0U;
    const bool taker_terminal = (report.status == SC_STATUS_FILLED ||
                                 report.status == SC_STATUS_CANCELED ||
                                 report.status == SC_STATUS_STP_CANCELED ||
                                 report.status == SC_STATUS_CB_REJECTED ||
                                 report.status == SC_STATUS_REJECTED);
    const uint8_t taker_flag = taker_terminal ? kEventFlagTakerTerminal : 0U;

    if (report.error_code != SC_OK) {
        WalEventRecord ev{};
        ev.event_type = static_cast<uint8_t>(WalEventType::ORDER_REJECTED);
        ev.market_id = cmd.market_id;
        ev.status = report.status;
        ev.flags = static_cast<uint8_t>(side_flag | kEventFlagTakerTerminal);
        ev.wal_seq = seq;
        ev.order_id = cmd.order_id;
        ev.exec_price_1e8 = cmd.price_1e8;
        ev.fill_qty_1e8 = cmd.qty_1e8;
        (void)event_log_.append(ev);
        return;
    }

    const WalOpcode op = static_cast<WalOpcode>(cmd.opcode);
    if (op == WalOpcode::SUBMIT_ORDER) {
        for (uint32_t i = 0U; i < fill_count; ++i) {
            const sc_fill_t& f = fills[i];
            const bool maker_terminal =
                (book != nullptr) ? !book->contains_order(f.maker_order_id) : false;
            WalEventRecord ev{};
            ev.event_type = static_cast<uint8_t>(WalEventType::TRADE_FILL);
            ev.market_id = cmd.market_id;
            ev.status = report.status;
            ev.flags = static_cast<uint8_t>(
                side_flag | (maker_terminal ? kEventFlagMakerTerminal : 0U) | taker_flag);
            ev.wal_seq = seq;
            ev.order_id = cmd.order_id;
            ev.maker_order_id = f.maker_order_id;
            ev.trade_id = f.trade_id;
            ev.exec_price_1e8 = f.exec_price_1e8;
            ev.fill_qty_1e8 = f.fill_qty_1e8;
            (void)event_log_.append(ev);
        }

        if (report.status == SC_STATUS_ACCEPTED || report.status == SC_STATUS_PARTIALLY_FILLED) {
            WalEventRecord ev{};
            ev.event_type = static_cast<uint8_t>(WalEventType::ORDER_ACCEPTED);
            ev.market_id = cmd.market_id;
            ev.status = report.status;
            ev.flags = side_flag;
            ev.wal_seq = seq;
            ev.order_id = cmd.order_id;
            ev.exec_price_1e8 = cmd.price_1e8;
            ev.fill_qty_1e8 = report.remaining_qty_1e8;
            (void)event_log_.append(ev);
        } else if (report.status == SC_STATUS_CANCELED ||
                   report.status == SC_STATUS_STP_CANCELED) {
            WalEventRecord ev{};
            ev.event_type = static_cast<uint8_t>(WalEventType::ORDER_CANCELED);
            ev.market_id = cmd.market_id;
            ev.status = report.status;
            ev.flags = static_cast<uint8_t>(side_flag | kEventFlagTakerTerminal);
            ev.wal_seq = seq;
            ev.order_id = cmd.order_id;
            ev.exec_price_1e8 = cmd.price_1e8;
            ev.fill_qty_1e8 = cmd.qty_1e8 - report.filled_qty_1e8;
            (void)event_log_.append(ev);
        }
    } else if (op == WalOpcode::CANCEL_ORDER || op == WalOpcode::CANCEL_ALL_ACCOUNT) {
        WalEventRecord ev{};
        ev.event_type = static_cast<uint8_t>(WalEventType::ORDER_CANCELED);
        ev.market_id = cmd.market_id;
        ev.status = report.status;
        ev.flags = kEventFlagTakerTerminal;
        ev.wal_seq = seq;
        ev.order_id = cmd.order_id;
        ev.fill_qty_1e8 = canceled_qty_or_count;
        (void)event_log_.append(ev);
    } else {
        WalEventRecord ev{};
        ev.event_type = static_cast<uint8_t>(WalEventType::ORDER_ACCEPTED);
        ev.market_id = cmd.market_id;
        ev.status = report.status;
        ev.wal_seq = seq;
        ev.order_id = cmd.order_id;
        ev.exec_price_1e8 = cmd.price_1e8;
        ev.fill_qty_1e8 = cmd.qty_1e8;
        (void)event_log_.append(ev);
    }
}

int32_t MatchingEngine::apply_command(const sc_order_cmd_t& cmd,
                                      bool write_wal,
                                      sc_exec_report_t& out_report,
                                      sc_fill_t* out_fills_buf,
                                      uint32_t max_fills) {
    out_report = sc_exec_report_t{};
    out_report.order_id = cmd.order_id;

    const uint64_t seq = (cmd.wal_seq != 0U) ? cmd.wal_seq : (wal_seq_ + 1U);
    if (seq > wal_seq_) {
        wal_seq_ = seq;
    }
    out_report.wal_seq = seq;

    const WalOpcode op = static_cast<WalOpcode>(cmd.opcode);
    const bool record_events = write_wal && event_log_.mode() != WalMode::MEMORY;
    MarketSlot* slot = nullptr;
    const sc_fill_t* ev_fills_ptr = out_fills_buf;
    uint32_t ev_fill_count = 0U;
    int64_t canceled_qty_or_count = 0;

    if (op == WalOpcode::SUBMIT_ORDER) {
        slot = get_slot(cmd.market_id);
        if (slot == nullptr) {
            out_report.status = SC_STATUS_REJECTED;
            out_report.error_code = SC_ERR_UNKNOWN_MARKET;
            if (record_events) {
                emit_event_records(cmd, seq, nullptr, out_report, nullptr, 0U, 0);
            }
            return SC_ERR_UNKNOWN_MARKET;
        }

        OrderSubmitRequest req{};
        req.order_id = cmd.order_id;
        req.account_id_low = cmd.account_id_low;
        req.account_id_high = cmd.account_id_high;
        req.price_1e8 = cmd.price_1e8;
        req.qty_1e8 = cmd.qty_1e8;
        req.timestamp_ns = cmd.timestamp_ns;
        req.side = static_cast<Side>(cmd.side);
        req.order_type = static_cast<OrderType>(cmd.order_type);
        req.time_in_force = static_cast<TimeInForce>(cmd.time_in_force);
        req.stp_mode = static_cast<StpMode>(cmd.stp_mode);

        sc_fill_t* submit_buf = out_fills_buf;
        uint32_t submit_max = max_fills;
        if (submit_buf == nullptr && record_events && !event_fills_scratch_.empty()) {
            submit_buf = event_fills_scratch_.data();
            submit_max = static_cast<uint32_t>(event_fills_scratch_.size());
        }

        uint32_t total_fills = 0U;
        const OrderSubmitResult res = slot->book.submit(
            req, next_trade_id_, &slot->breaker, submit_buf, submit_max, &total_fills);

        out_report.filled_qty_1e8 = res.filled_qty_1e8;
        out_report.remaining_qty_1e8 = res.remaining_qty_1e8;
        out_report.fill_count = (out_fills_buf != nullptr) ? res.fill_count : 0U;
        out_report.status = static_cast<uint8_t>(res.status);
        out_report.error_code = res.error_code;

        ev_fills_ptr = submit_buf;
        ev_fill_count = (submit_buf != nullptr) ? std::min(total_fills, submit_max) : 0U;

        if (res.error_code != SC_OK) {
            if (record_events) {
                emit_event_records(cmd, seq, &slot->book, out_report, nullptr, 0U, 0);
            }
            return res.error_code;
        }
    } else if (op == WalOpcode::CANCEL_ORDER) {
        slot = get_slot(cmd.market_id);
        if (slot == nullptr) {
            out_report.status = SC_STATUS_REJECTED;
            out_report.error_code = SC_ERR_UNKNOWN_MARKET;
            if (record_events) {
                emit_event_records(cmd, seq, nullptr, out_report, nullptr, 0U, 0);
            }
            return SC_ERR_UNKNOWN_MARKET;
        }
        int64_t canceled_qty = 0;
        const int32_t rc = slot->book.cancel(cmd.order_id, &canceled_qty);
        if (rc != SC_OK) {
            out_report.status = SC_STATUS_REJECTED;
            out_report.error_code = rc;
            if (record_events) {
                emit_event_records(cmd, seq, &slot->book, out_report, nullptr, 0U, 0);
            }
            return rc;
        }
        canceled_qty_or_count = canceled_qty;
        out_report.remaining_qty_1e8 = 0;
        out_report.status = SC_STATUS_CANCELED;
        out_report.error_code = SC_OK;
    } else if (op == WalOpcode::CANCEL_ALL_ACCOUNT) {
        uint32_t canceled_total = 0U;
        if (cmd.market_id == 0U) {
            for (auto& kv : markets_) {
                canceled_total +=
                    kv.second->book.cancel_all_account(cmd.account_id_low, cmd.account_id_high);
            }
        } else {
            slot = get_slot(cmd.market_id);
            if (slot == nullptr) {
                out_report.status = SC_STATUS_REJECTED;
                out_report.error_code = SC_ERR_UNKNOWN_MARKET;
                if (record_events) {
                    emit_event_records(cmd, seq, nullptr, out_report, nullptr, 0U, 0);
                }
                return SC_ERR_UNKNOWN_MARKET;
            }
            canceled_total =
                slot->book.cancel_all_account(cmd.account_id_low, cmd.account_id_high);
        }
        canceled_qty_or_count = static_cast<int64_t>(canceled_total);
        out_report.status = SC_STATUS_CANCELED;
        out_report.error_code = SC_OK;
    } else if (op == WalOpcode::UPDATE_MARK_PRICE) {
        slot = get_slot(cmd.market_id);
        if (slot == nullptr) {
            out_report.status = SC_STATUS_REJECTED;
            out_report.error_code = SC_ERR_UNKNOWN_MARKET;
            if (record_events) {
                emit_event_records(cmd, seq, nullptr, out_report, nullptr, 0U, 0);
            }
            return SC_ERR_UNKNOWN_MARKET;
        }
        if (cmd.price_1e8 < 0 || cmd.price_1e8 > kMaxPriceOrQty1e8) {
            out_report.status = SC_STATUS_REJECTED;
            out_report.error_code = SC_ERR_INVALID_NUMERIC_BOUND;
            if (record_events) {
                emit_event_records(cmd, seq, &slot->book, out_report, nullptr, 0U, 0);
            }
            return SC_ERR_INVALID_NUMERIC_BOUND;
        }
        slot->breaker.set_mark_price(cmd.price_1e8);
        out_report.status = SC_STATUS_ACCEPTED;
        out_report.error_code = SC_OK;
    } else if (op == WalOpcode::SNAPSHOT_BARRIER) {
        out_report.status = SC_STATUS_ACCEPTED;
        out_report.error_code = SC_OK;
    } else {
        out_report.status = SC_STATUS_REJECTED;
        out_report.error_code = SC_ERR_INVALID_ARG;
        if (record_events) {
            emit_event_records(cmd, seq, nullptr, out_report, nullptr, 0U, 0);
        }
        return SC_ERR_INVALID_ARG;
    }

    if (write_wal && wal_.mode() != WalMode::MEMORY) {
        WalRecord rec{};
        rec.opcode = cmd.opcode;
        rec.market_id = cmd.market_id;
        rec.flags = pack_wal_flags(cmd.side, cmd.order_type, cmd.time_in_force, cmd.stp_mode);
        rec.wal_seq = seq;
        rec.order_id = cmd.order_id;
        rec.account_id_low = cmd.account_id_low;
        rec.account_id_high = cmd.account_id_high;
        rec.price_1e8 = cmd.price_1e8;
        rec.qty_1e8 = cmd.qty_1e8;
        const int32_t wrc = wal_.append(rec);
        if (wrc != SC_OK) {
            out_report.error_code = wrc;
            return wrc;
        }
    }

    if (record_events) {
        const OrderBook* book_ptr = (slot != nullptr) ? &slot->book : nullptr;
        emit_event_records(cmd, seq, book_ptr, out_report, ev_fills_ptr, ev_fill_count,
                           canceled_qty_or_count);
    }

    return SC_OK;
}

int32_t MatchingEngine::set_mark_price(uint16_t market_id,
                                       int64_t mark_price_1e8,
                                       int64_t collar_bps) {
    MarketSlot* slot = get_slot(market_id);
    if (slot == nullptr) {
        return SC_ERR_UNKNOWN_MARKET;
    }
    if (mark_price_1e8 < 0 || mark_price_1e8 > kMaxPriceOrQty1e8) {
        return SC_ERR_INVALID_NUMERIC_BOUND;
    }
    if (collar_bps > 0) {
        slot->breaker.set_collar_bps(collar_bps);
    }
    slot->breaker.set_mark_price(mark_price_1e8);
    return SC_OK;
}

int32_t MatchingEngine::get_l2_depth(uint16_t market_id,
                                     uint32_t max_levels,
                                     std::vector<L2Level>& out_bids,
                                     std::vector<L2Level>& out_asks) const {
    const MarketSlot* slot = get_slot(market_id);
    if (slot == nullptr) {
        return SC_ERR_UNKNOWN_MARKET;
    }
    slot->book.get_l2_depth(max_levels, out_bids, out_asks);
    return SC_OK;
}

int32_t MatchingEngine::replay_wal(const std::string& wal_path,
                                   uint64_t min_wal_seq_exclusive,
                                   uint64_t* out_last_seq,
                                   uint64_t* out_replayed_count) {
    std::vector<WalRecord> records;
    const int32_t rrc = WalReader::read_all(wal_path, records, true, nullptr);
    if (rrc != SC_OK) {
        return rrc;
    }

    uint64_t replayed = 0U;
    uint64_t last_seq = wal_seq_;
    for (const WalRecord& rec : records) {
        if (rec.wal_seq <= min_wal_seq_exclusive) {
            continue;
        }
        sc_order_cmd_t cmd{};
        cmd.wal_seq = rec.wal_seq;
        cmd.order_id = rec.order_id;
        cmd.account_id_low = rec.account_id_low;
        cmd.account_id_high = rec.account_id_high;
        cmd.price_1e8 = rec.price_1e8;
        cmd.qty_1e8 = rec.qty_1e8;
        cmd.market_id = rec.market_id;
        cmd.opcode = rec.opcode;
        unpack_wal_flags(rec.flags, cmd.side, cmd.order_type, cmd.time_in_force, cmd.stp_mode);

        sc_exec_report_t report{};
        const int32_t rc = apply_command(cmd, false, report, nullptr, 0U);
        if (rc != SC_OK && rc != SC_ERR_CIRCUIT_BREAKER_TRIPPED &&
            rc != SC_ERR_ORDER_NOT_FOUND) {
            return rc;
        }
        last_seq = rec.wal_seq;
        ++replayed;
    }

    if (out_last_seq != nullptr) {
        *out_last_seq = last_seq;
    }
    if (out_replayed_count != nullptr) {
        *out_replayed_count = replayed;
    }
    return SC_OK;
}

int32_t MatchingEngine::write_snapshot(const std::string& path) const {
    return Snapshot::write(*this, path);
}

int32_t MatchingEngine::load_snapshot(const std::string& path, uint64_t* out_snapshot_seq) {
    return Snapshot::load(*this, path, out_snapshot_seq);
}

void MatchingEngine::reset_state(uint64_t wal_seq, uint64_t next_trade_id) noexcept {
    wal_seq_ = wal_seq;
    next_trade_id_ = next_trade_id;
    for (auto& kv : markets_) {
        kv.second->book.clear();
        kv.second->breaker.reset();
    }
}

void MatchingEngine::compute_state_sha256(uint8_t out_hash[32]) const noexcept {
    Sha256 hasher;
    for (const auto& kv : markets_) {
        kv.second->book.for_each_resting_order_canonical([&hasher](const OrderNode& node) {
            uint8_t buf[41]{};
            write_le64(buf, node.order_id);
            write_le64(buf + 8U, node.account_id_high);
            write_le64(buf + 16U, node.account_id_low);
            write_le64(buf + 24U, static_cast<uint64_t>(node.price_1e8));
            write_le64(buf + 32U, static_cast<uint64_t>(node.remaining_1e8));
            buf[40] = node.side;
            hasher.update(buf, sizeof(buf));
        });
    }
    hasher.finalize(out_hash);
}

}  // namespace settlecore
