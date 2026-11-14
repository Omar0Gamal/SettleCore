#include "settlecore/c_api.h"

#include <algorithm>
#include <memory>
#include <vector>

#include "settlecore/matching_engine.hpp"

struct sc_engine_handle {
    std::unique_ptr<settlecore::MatchingEngine> impl;
};

extern "C" {

sc_engine_t* sc_engine_create(const sc_engine_config_t* config) {
    settlecore::EngineConfig cfg{};
    if (config != nullptr) {
        cfg.max_orders_per_book = config->max_orders_per_book;
        cfg.cb_collar_bps = config->cb_collar_bps;
        cfg.wal_mode = static_cast<settlecore::WalMode>(config->wal_mode);
        if (config->wal_path != nullptr) {
            cfg.wal_path = config->wal_path;
        }
    }

    auto engine = std::make_unique<settlecore::MatchingEngine>(cfg);
    if (!engine->is_ok()) {
        return nullptr;
    }
    auto* handle = new sc_engine_handle{std::move(engine)};
    return handle;
}

void sc_engine_destroy(sc_engine_t* engine) {
    delete engine;
}

int32_t sc_engine_submit(sc_engine_t* engine,
                         const sc_order_cmd_t* cmd,
                         sc_exec_report_t* out_report,
                         sc_fill_t* out_fills_buf,
                         uint32_t max_fills) {
    if (engine == nullptr || engine->impl == nullptr || cmd == nullptr || out_report == nullptr) {
        return SC_C_ERR_INVALID_ARG;
    }
    return engine->impl->submit(*cmd, *out_report, out_fills_buf, max_fills);
}

int32_t sc_engine_submit_batch(sc_engine_t* engine,
                               const sc_order_cmd_t* cmds,
                               uint32_t cmd_count,
                               sc_exec_report_t* out_reports,
                               sc_fill_t* out_fills_buf,
                               uint32_t max_total_fills,
                               uint32_t* out_total_fills) {
    if (engine == nullptr || engine->impl == nullptr) {
        return SC_C_ERR_INVALID_ARG;
    }
    if (cmd_count > 0U && (cmds == nullptr || out_reports == nullptr)) {
        return SC_C_ERR_INVALID_ARG;
    }
    return engine->impl->submit_batch(cmds, cmd_count, out_reports, out_fills_buf,
                                      max_total_fills, out_total_fills);
}

int32_t sc_engine_set_mark_price(sc_engine_t* engine,
                                 uint16_t market_id,
                                 int64_t mark_price_1e8,
                                 int64_t collar_bps) {
    if (engine == nullptr || engine->impl == nullptr) {
        return SC_C_ERR_INVALID_ARG;
    }
    return engine->impl->set_mark_price(market_id, mark_price_1e8, collar_bps);
}

int32_t sc_engine_get_l2_depth(const sc_engine_t* engine,
                               uint16_t market_id,
                               uint32_t max_levels,
                               sc_l2_level_t* out_bids,
                               uint32_t* out_bid_count,
                               sc_l2_level_t* out_asks,
                               uint32_t* out_ask_count) {
    if (engine == nullptr || engine->impl == nullptr) {
        return SC_C_ERR_INVALID_ARG;
    }
    std::vector<settlecore::L2Level> bids;
    std::vector<settlecore::L2Level> asks;
    const int32_t rc = engine->impl->get_l2_depth(market_id, max_levels, bids, asks);
    if (rc != SC_C_OK) {
        return rc;
    }

    const uint32_t n_bids = static_cast<uint32_t>(std::min<size_t>(bids.size(), max_levels));
    if (out_bid_count != nullptr) {
        *out_bid_count = n_bids;
    }
    if (out_bids != nullptr) {
        for (uint32_t i = 0U; i < n_bids; ++i) {
            out_bids[i] = sc_l2_level_t{bids[i].price_1e8, bids[i].volume_1e8, bids[i].order_count, 0U};
        }
    }

    const uint32_t n_asks = static_cast<uint32_t>(std::min<size_t>(asks.size(), max_levels));
    if (out_ask_count != nullptr) {
        *out_ask_count = n_asks;
    }
    if (out_asks != nullptr) {
        for (uint32_t i = 0U; i < n_asks; ++i) {
            out_asks[i] = sc_l2_level_t{asks[i].price_1e8, asks[i].volume_1e8, asks[i].order_count, 0U};
        }
    }
    return SC_C_OK;
}

int32_t sc_engine_replay_wal(sc_engine_t* engine,
                             const char* wal_path,
                             uint64_t min_wal_seq_exclusive,
                             uint64_t* out_last_seq,
                             uint64_t* out_replayed_count) {
    if (engine == nullptr || engine->impl == nullptr || wal_path == nullptr) {
        return SC_C_ERR_INVALID_ARG;
    }
    return engine->impl->replay_wal(wal_path, min_wal_seq_exclusive, out_last_seq, out_replayed_count);
}

int32_t sc_engine_write_snapshot(sc_engine_t* engine, const char* snapshot_path) {
    if (engine == nullptr || engine->impl == nullptr || snapshot_path == nullptr) {
        return SC_C_ERR_INVALID_ARG;
    }
    return engine->impl->write_snapshot(snapshot_path);
}

int32_t sc_engine_load_snapshot(sc_engine_t* engine,
                                const char* snapshot_path,
                                uint64_t* out_snapshot_seq) {
    if (engine == nullptr || engine->impl == nullptr || snapshot_path == nullptr) {
        return SC_C_ERR_INVALID_ARG;
    }
    return engine->impl->load_snapshot(snapshot_path, out_snapshot_seq);
}

int32_t sc_engine_state_sha256(const sc_engine_t* engine, uint8_t out_hash[32]) {
    if (engine == nullptr || engine->impl == nullptr || out_hash == nullptr) {
        return SC_C_ERR_INVALID_ARG;
    }
    engine->impl->compute_state_sha256(out_hash);
    return SC_C_OK;
}

}  // extern "C"
