#include "settlecore/matching_engine.hpp"

#include <gtest/gtest.h>

namespace settlecore {
namespace {

sc_order_cmd_t make_order(uint64_t oid,
                          uint64_t acct,
                          uint8_t side,
                          int64_t price_1e8,
                          int64_t qty_1e8,
                          uint8_t order_type = SC_ORDER_TYPE_LIMIT,
                          uint8_t tif = SC_TIF_GTC,
                          uint8_t stp = SC_STP_CANCEL_NEWEST) {
    sc_order_cmd_t cmd{};
    cmd.market_id = kMarketEthUsdc;
    cmd.opcode = SC_OP_SUBMIT_ORDER;
    cmd.order_id = oid;
    cmd.account_id_low = acct;
    cmd.side = side;
    cmd.price_1e8 = price_1e8;
    cmd.qty_1e8 = qty_1e8;
    cmd.order_type = order_type;
    cmd.time_in_force = tif;
    cmd.stp_mode = stp;
    return cmd;
}

TEST(StpAndCircuitBreakerTest, SelfTradePreventionCancelNewest) {
    MatchingEngine engine(EngineConfig{1024U, 1000LL, WalMode::MEMORY, ""});
    sc_exec_report_t report{};
    sc_fill_t fills[8]{};

    // Account 10 rests sell @ 2000, Account 20 rests sell @ 2000
    ASSERT_EQ(engine.submit(make_order(1U, 10U, SC_SIDE_SELL, 2000LL * kScale1e8, 2LL * kScale1e8),
                            report, fills, 8U),
              SC_OK);
    ASSERT_EQ(engine.submit(make_order(2U, 20U, SC_SIDE_SELL, 2000LL * kScale1e8, 2LL * kScale1e8),
                            report, fills, 8U),
              SC_OK);

    // Account 20 submits buy @ 2000 for 4 with CANCEL_NEWEST:
    // Fills 2 against Account 10, then hits self-order #2 and cancels incoming remainder.
    ASSERT_EQ(engine.submit(make_order(3U, 20U, SC_SIDE_BUY, 2000LL * kScale1e8, 4LL * kScale1e8,
                                       SC_ORDER_TYPE_LIMIT, SC_TIF_GTC, SC_STP_CANCEL_NEWEST),
                            report, fills, 8U),
              SC_OK);
    EXPECT_EQ(report.status, SC_STATUS_STP_CANCELED);
    EXPECT_EQ(report.filled_qty_1e8, 2LL * kScale1e8);
    EXPECT_EQ(report.remaining_qty_1e8, 0LL);
    EXPECT_EQ(report.fill_count, 1U);
    EXPECT_TRUE(engine.get_book(kMarketEthUsdc)->contains_order(2U));
    EXPECT_FALSE(engine.get_book(kMarketEthUsdc)->contains_order(3U));
}

TEST(StpAndCircuitBreakerTest, SelfTradePreventionCancelOldest) {
    MatchingEngine engine(EngineConfig{1024U, 1000LL, WalMode::MEMORY, ""});
    sc_exec_report_t report{};
    sc_fill_t fills[8]{};

    // Account 20 rests sell @ 2000, Account 30 rests sell @ 2000
    ASSERT_EQ(engine.submit(make_order(1U, 20U, SC_SIDE_SELL, 2000LL * kScale1e8, 2LL * kScale1e8),
                            report, fills, 8U),
              SC_OK);
    ASSERT_EQ(engine.submit(make_order(2U, 30U, SC_SIDE_SELL, 2000LL * kScale1e8, 3LL * kScale1e8),
                            report, fills, 8U),
              SC_OK);

    // Account 20 buys 3 @ 2000 with CANCEL_OLDEST: cancels #1, fills 3 against #2.
    ASSERT_EQ(engine.submit(make_order(3U, 20U, SC_SIDE_BUY, 2000LL * kScale1e8, 3LL * kScale1e8,
                                       SC_ORDER_TYPE_LIMIT, SC_TIF_GTC, SC_STP_CANCEL_OLDEST),
                            report, fills, 8U),
              SC_OK);
    EXPECT_EQ(report.status, SC_STATUS_FILLED);
    EXPECT_EQ(report.filled_qty_1e8, 3LL * kScale1e8);
    EXPECT_EQ(report.fill_count, 1U);
    EXPECT_EQ(fills[0].maker_order_id, 2U);
    EXPECT_FALSE(engine.get_book(kMarketEthUsdc)->contains_order(1U));
    EXPECT_FALSE(engine.get_book(kMarketEthUsdc)->contains_order(2U));
}

TEST(StpAndCircuitBreakerTest, SelfTradePreventionCancelBoth) {
    MatchingEngine engine(EngineConfig{1024U, 1000LL, WalMode::MEMORY, ""});
    sc_exec_report_t report{};
    sc_fill_t fills[8]{};

    ASSERT_EQ(engine.submit(make_order(1U, 20U, SC_SIDE_SELL, 2000LL * kScale1e8, 2LL * kScale1e8),
                            report, fills, 8U),
              SC_OK);

    ASSERT_EQ(engine.submit(make_order(2U, 20U, SC_SIDE_BUY, 2000LL * kScale1e8, 2LL * kScale1e8,
                                       SC_ORDER_TYPE_LIMIT, SC_TIF_GTC, SC_STP_CANCEL_BOTH),
                            report, fills, 8U),
              SC_OK);
    EXPECT_EQ(report.status, SC_STATUS_STP_CANCELED);
    EXPECT_EQ(report.filled_qty_1e8, 0LL);
    EXPECT_EQ(report.fill_count, 0U);
    EXPECT_EQ(engine.get_book(kMarketEthUsdc)->active_order_count(), 0U);
}

TEST(StpAndCircuitBreakerTest, TenPercentCollarRejectsAggressiveCrashAndResets) {
    MatchingEngine engine(EngineConfig{1024U, 1000LL, WalMode::MEMORY, ""});
    ASSERT_EQ(engine.set_mark_price(kMarketEthUsdc, 2000LL * kScale1e8, 1000LL), SC_OK);

    // Allowed band: [1800, 2200]
    sc_exec_report_t report{};
    sc_fill_t fills[8]{};

    // Passive bid at 1750 (outside collar) and bid at 1800 (exact lower boundary)
    ASSERT_EQ(engine.submit(make_order(1U, 10U, SC_SIDE_BUY, 1750LL * kScale1e8, 2LL * kScale1e8),
                            report, fills, 8U),
              SC_OK);
    ASSERT_EQ(engine.submit(make_order(2U, 11U, SC_SIDE_BUY, 1800LL * kScale1e8, 1LL * kScale1e8),
                            report, fills, 8U),
              SC_OK);

    // Aggressive sell for 1 @ 1800 executes inside [1800, 2200] -> succeeds!
    ASSERT_EQ(engine.submit(make_order(3U, 12U, SC_SIDE_SELL, 1800LL * kScale1e8, 1LL * kScale1e8),
                            report, fills, 8U),
              SC_OK);
    EXPECT_EQ(report.status, SC_STATUS_FILLED);

    // Aggressive MARKET sell now hits resting bid at 1750 (< 1800) -> circuit breaker trips!
    const int32_t rc = engine.submit(
        make_order(4U, 12U, SC_SIDE_SELL, 0LL, 1LL * kScale1e8, SC_ORDER_TYPE_MARKET, SC_TIF_IOC),
        report, fills, 8U);
    EXPECT_EQ(rc, SC_ERR_CIRCUIT_BREAKER_TRIPPED);
    EXPECT_EQ(report.status, SC_STATUS_CB_REJECTED);
    EXPECT_TRUE(engine.get_book(kMarketEthUsdc)->contains_order(1U));

    // Upper collar test: resting ask at 2250 (> 2200)
    ASSERT_EQ(engine.submit(make_order(5U, 13U, SC_SIDE_SELL, 2250LL * kScale1e8, 1LL * kScale1e8),
                            report, fills, 8U),
              SC_OK);
    const int32_t rc_up = engine.submit(
        make_order(6U, 14U, SC_SIDE_BUY, 2300LL * kScale1e8, 1LL * kScale1e8), report, fills, 8U);
    EXPECT_EQ(rc_up, SC_ERR_CIRCUIT_BREAKER_TRIPPED);

    // Reset mark price via OP_UPDATE_MARK_PRICE to 2200 -> [1980, 2420], now 2250 ask is fillable
    sc_order_cmd_t mark_cmd{};
    mark_cmd.market_id = kMarketEthUsdc;
    mark_cmd.opcode = SC_OP_UPDATE_MARK_PRICE;
    mark_cmd.price_1e8 = 2200LL * kScale1e8;
    ASSERT_EQ(engine.submit(mark_cmd, report, nullptr, 0U), SC_OK);

    ASSERT_EQ(engine.submit(make_order(7U, 14U, SC_SIDE_BUY, 2300LL * kScale1e8, 1LL * kScale1e8),
                            report, fills, 8U),
              SC_OK);
    EXPECT_EQ(report.status, SC_STATUS_FILLED);
}

}  // namespace
}  // namespace settlecore
