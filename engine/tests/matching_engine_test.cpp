#include "settlecore/c_api.h"
#include "settlecore/matching_engine.hpp"

#include <gtest/gtest.h>

#include <cstring>

namespace {

sc_order_cmd_t make_cmd(uint16_t market_id,
                        uint8_t opcode,
                        uint64_t order_id,
                        uint64_t acct_low,
                        uint8_t side,
                        int64_t price_1e8,
                        int64_t qty_1e8,
                        uint8_t order_type = SC_ORDER_TYPE_LIMIT,
                        uint8_t tif = SC_TIF_GTC) {
    sc_order_cmd_t cmd{};
    cmd.market_id = market_id;
    cmd.opcode = opcode;
    cmd.order_id = order_id;
    cmd.account_id_low = acct_low;
    cmd.side = side;
    cmd.price_1e8 = price_1e8;
    cmd.qty_1e8 = qty_1e8;
    cmd.order_type = order_type;
    cmd.time_in_force = tif;
    return cmd;
}

TEST(MatchingEngineCApiTest, MultiMarketSubmitMatchAndL2) {
    sc_engine_config_t cfg{};
    cfg.max_orders_per_book = 1024U;
    cfg.cb_collar_bps = 1000LL;
    cfg.wal_mode = SC_WAL_MODE_MEMORY;

    sc_engine_t* engine = sc_engine_create(&cfg);
    ASSERT_NE(engine, nullptr);

    sc_exec_report_t report{};
    sc_fill_t fills[8]{};

    // Market 1 (ETH/USDC): resting ask & bid
    auto cmd1 = make_cmd(1U, SC_OP_SUBMIT_ORDER, 1U, 100U, SC_SIDE_SELL, 2500LL * 100'000'000LL,
                         2LL * 100'000'000LL);
    ASSERT_EQ(sc_engine_submit(engine, &cmd1, &report, fills, 8U), SC_C_OK);
    EXPECT_EQ(report.wal_seq, 1U);
    EXPECT_EQ(report.status, SC_STATUS_ACCEPTED);

    // Market 2 (BTC/USDC): separate book
    auto cmd2 = make_cmd(2U, SC_OP_SUBMIT_ORDER, 2U, 101U, SC_SIDE_BUY, 90000LL * 100'000'000LL,
                         1LL * 100'000'000LL);
    ASSERT_EQ(sc_engine_submit(engine, &cmd2, &report, fills, 8U), SC_C_OK);
    EXPECT_EQ(report.wal_seq, 2U);

    // Cross on Market 1 only
    auto cmd3 = make_cmd(1U, SC_OP_SUBMIT_ORDER, 3U, 102U, SC_SIDE_BUY, 2500LL * 100'000'000LL,
                         1LL * 100'000'000LL);
    ASSERT_EQ(sc_engine_submit(engine, &cmd3, &report, fills, 8U), SC_C_OK);
    EXPECT_EQ(report.status, SC_STATUS_FILLED);
    EXPECT_EQ(report.fill_count, 1U);
    EXPECT_EQ(fills[0].maker_order_id, 1U);
    EXPECT_EQ(fills[0].taker_order_id, 3U);

    sc_l2_level_t bids[4]{};
    sc_l2_level_t asks[4]{};
    uint32_t nb = 0U;
    uint32_t na = 0U;
    ASSERT_EQ(sc_engine_get_l2_depth(engine, 1U, 4U, bids, &nb, asks, &na), SC_C_OK);
    EXPECT_EQ(nb, 0U);
    ASSERT_EQ(na, 1U);
    EXPECT_EQ(asks[0].volume_1e8, 1LL * 100'000'000LL);

    ASSERT_EQ(sc_engine_get_l2_depth(engine, 2U, 4U, bids, &nb, asks, &na), SC_C_OK);
    ASSERT_EQ(nb, 1U);
    EXPECT_EQ(na, 0U);

    sc_engine_destroy(engine);
}

TEST(MatchingEngineCApiTest, CancelOnDisconnectAcrossAllMarkets) {
    sc_engine_config_t cfg{};
    cfg.max_orders_per_book = 1024U;
    sc_engine_t* engine = sc_engine_create(&cfg);
    ASSERT_NE(engine, nullptr);

    sc_exec_report_t report{};
    auto c1 = make_cmd(1U, SC_OP_SUBMIT_ORDER, 10U, 555U, SC_SIDE_BUY, 2000LL * 100'000'000LL,
                       1LL * 100'000'000LL);
    auto c2 = make_cmd(2U, SC_OP_SUBMIT_ORDER, 11U, 555U, SC_SIDE_SELL, 95000LL * 100'000'000LL,
                       1LL * 100'000'000LL);
    auto c3 = make_cmd(1U, SC_OP_SUBMIT_ORDER, 12U, 777U, SC_SIDE_BUY, 1990LL * 100'000'000LL,
                       1LL * 100'000'000LL);

    ASSERT_EQ(sc_engine_submit(engine, &c1, &report, nullptr, 0U), SC_C_OK);
    ASSERT_EQ(sc_engine_submit(engine, &c2, &report, nullptr, 0U), SC_C_OK);
    ASSERT_EQ(sc_engine_submit(engine, &c3, &report, nullptr, 0U), SC_C_OK);

    // Cancel-on-Disconnect for account 555 across all markets (market_id == 0)
    auto cod = make_cmd(0U, SC_OP_CANCEL_ALL_ACCOUNT, 0U, 555U, 0U, 0LL, 0LL);
    ASSERT_EQ(sc_engine_submit(engine, &cod, &report, nullptr, 0U), SC_C_OK);

    sc_l2_level_t bids[4]{};
    sc_l2_level_t asks[4]{};
    uint32_t nb = 0U;
    uint32_t na = 0U;
    ASSERT_EQ(sc_engine_get_l2_depth(engine, 1U, 4U, bids, &nb, asks, &na), SC_C_OK);
    ASSERT_EQ(nb, 1U);
    EXPECT_EQ(bids[0].price_1e8, 1990LL * 100'000'000LL);
    EXPECT_EQ(na, 0U);

    ASSERT_EQ(sc_engine_get_l2_depth(engine, 2U, 4U, bids, &nb, asks, &na), SC_C_OK);
    EXPECT_EQ(nb, 0U);
    EXPECT_EQ(na, 0U);

    sc_engine_destroy(engine);
}

TEST(MatchingEngineCApiTest, CanonicalSha256Determinism) {
    sc_engine_config_t cfg{};
    cfg.max_orders_per_book = 1024U;
    sc_engine_t* e1 = sc_engine_create(&cfg);
    sc_engine_t* e2 = sc_engine_create(&cfg);
    ASSERT_NE(e1, nullptr);
    ASSERT_NE(e2, nullptr);

    sc_exec_report_t report{};
    for (uint64_t i = 1U; i <= 5U; ++i) {
        auto cmd = make_cmd(1U, SC_OP_SUBMIT_ORDER, i, 100U + i, SC_SIDE_BUY,
                            static_cast<int64_t>(2000U + i) * 100'000'000LL, 100'000'000LL);
        ASSERT_EQ(sc_engine_submit(e1, &cmd, &report, nullptr, 0U), SC_C_OK);
        ASSERT_EQ(sc_engine_submit(e2, &cmd, &report, nullptr, 0U), SC_C_OK);
    }

    uint8_t h1[32]{};
    uint8_t h2[32]{};
    ASSERT_EQ(sc_engine_state_sha256(e1, h1), SC_C_OK);
    ASSERT_EQ(sc_engine_state_sha256(e2, h2), SC_C_OK);
    EXPECT_EQ(std::memcmp(h1, h2, 32U), 0);

    // Mutate e2 and confirm hash changes
    auto cancel = make_cmd(1U, SC_OP_CANCEL_ORDER, 3U, 0U, 0U, 0LL, 0LL);
    ASSERT_EQ(sc_engine_submit(e2, &cancel, &report, nullptr, 0U), SC_C_OK);
    uint8_t h3[32]{};
    ASSERT_EQ(sc_engine_state_sha256(e2, h3), SC_C_OK);
    EXPECT_NE(std::memcmp(h1, h3, 32U), 0);

    sc_engine_destroy(e1);
    sc_engine_destroy(e2);
}

}  // namespace
