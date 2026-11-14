#include "settlecore/c_api.h"
#include "settlecore/matching_engine.hpp"
#include "settlecore/snapshot.hpp"
#include "settlecore/wal.hpp"

#include <gtest/gtest.h>

#include <cstring>
#include <filesystem>
#include <fstream>
#include <string>
#include <vector>

namespace settlecore {
namespace {

std::filesystem::path temp_path(const std::string& name) {
    return std::filesystem::temp_directory_path() / name;
}

TEST(WalReplayTest, Crc32cMatchesSoftwareAndKnownVector) {
    const uint8_t vec[] = {'1', '2', '3', '4', '5', '6', '7', '8', '9'};
    EXPECT_EQ(crc32c_sw(vec, sizeof(vec)), 0xE3069283U);
    EXPECT_EQ(crc32c(vec, sizeof(vec)), 0xE3069283U);
}

TEST(WalReplayTest, DetectsMidFileCorruptionAndTruncatesTornTail) {
    const std::filesystem::path wal_file = temp_path("settlecore_test_torn.wal");
    std::filesystem::remove(wal_file);

    {
        WalWriter writer;
        ASSERT_EQ(writer.open(wal_file.string(), WalMode::BUFFERED), SC_OK);
        for (uint64_t i = 1U; i <= 5U; ++i) {
            WalRecord rec{};
            rec.opcode = static_cast<uint8_t>(WalOpcode::SUBMIT_ORDER);
            rec.market_id = kMarketEthUsdc;
            rec.wal_seq = i;
            rec.order_id = i;
            rec.price_1e8 = 2000LL * kScale1e8;
            rec.qty_1e8 = 1LL * kScale1e8;
            ASSERT_EQ(writer.append(rec), SC_OK);
        }
        ASSERT_EQ(writer.sync(), SC_OK);
    }

    // Append 19 torn trailing bytes
    {
        std::ofstream ofs(wal_file, std::ios::binary | std::ios::app);
        const char junk[19] = {'T', 'O', 'R', 'N', '_', 'T', 'A', 'I', 'L', '!',
                               '1', '2', '3', '4', '5', '6', '7', '8', '9'};
        ofs.write(junk, sizeof(junk));
    }

    std::vector<WalRecord> recs;
    EXPECT_EQ(WalReader::read_all(wal_file.string(), recs, false, nullptr), SC_ERR_CORRUPT_WAL);

    size_t truncated_bytes = 0U;
    ASSERT_EQ(WalReader::read_all(wal_file.string(), recs, true, &truncated_bytes), SC_OK);
    EXPECT_EQ(truncated_bytes, 19U);
    ASSERT_EQ(recs.size(), 5U);
    EXPECT_EQ(std::filesystem::file_size(wal_file), 5U * sizeof(WalRecord));

    // Corrupt a byte inside record #2 (offset 64 + 24)
    {
        std::fstream fs(wal_file, std::ios::in | std::ios::out | std::ios::binary);
        fs.seekp(64 + 24, std::ios::beg);
        char bad = static_cast<char>(0xFF);
        fs.write(&bad, 1);
    }
    EXPECT_EQ(WalReader::read_all(wal_file.string(), recs, true, nullptr), SC_ERR_CORRUPT_WAL);
    std::filesystem::remove(wal_file);
}

TEST(WalReplayTest, SnapshotPlusTailWalReplayMatchesLiveStateSha256) {
    const std::filesystem::path wal_file = temp_path("settlecore_test_replay.wal");
    const std::filesystem::path snap_file = temp_path("settlecore_test_snapshot.snap");
    std::filesystem::remove(wal_file);
    std::filesystem::remove(snap_file);

    uint8_t live_hash[32]{};
    uint64_t snap_seq = 0U;

    {
        EngineConfig cfg{};
        cfg.max_orders_per_book = 4096U;
        cfg.wal_mode = WalMode::BUFFERED;
        cfg.wal_path = wal_file.string();

        MatchingEngine live(cfg);
        ASSERT_TRUE(live.is_ok());
        ASSERT_EQ(live.set_mark_price(kMarketEthUsdc, 2000LL * kScale1e8, 1000LL), SC_OK);

        sc_exec_report_t report{};
        sc_fill_t fills[16]{};

        // First 60 orders before snapshot
        for (uint64_t i = 1U; i <= 60U; ++i) {
            sc_order_cmd_t cmd{};
            cmd.market_id = kMarketEthUsdc;
            cmd.opcode = SC_OP_SUBMIT_ORDER;
            cmd.order_id = i;
            cmd.account_id_low = (i % 7U) + 1U;
            cmd.side = (i % 2U == 0U) ? SC_SIDE_SELL : SC_SIDE_BUY;
            cmd.price_1e8 = static_cast<int64_t>(1995U + (i % 11U)) * kScale1e8;
            cmd.qty_1e8 = 2LL * kScale1e8;
            ASSERT_EQ(live.submit(cmd, report, fills, 16U), SC_OK);
        }

        // Write snapshot at current wal_seq
        ASSERT_EQ(live.write_snapshot(snap_file.string()), SC_OK);
        snap_seq = live.wal_seq();
        EXPECT_EQ(snap_seq, 60U);

        // Next 40 commands after snapshot (including cancels & mark updates)
        for (uint64_t i = 61U; i <= 95U; ++i) {
            sc_order_cmd_t cmd{};
            cmd.market_id = kMarketEthUsdc;
            cmd.opcode = SC_OP_SUBMIT_ORDER;
            cmd.order_id = i;
            cmd.account_id_low = (i % 5U) + 10U;
            cmd.side = (i % 2U == 0U) ? SC_SIDE_SELL : SC_SIDE_BUY;
            cmd.price_1e8 = static_cast<int64_t>(1990U + (i % 15U)) * kScale1e8;
            cmd.qty_1e8 = 1LL * kScale1e8;
            ASSERT_EQ(live.submit(cmd, report, fills, 16U), SC_OK);
        }
        sc_order_cmd_t cancel_cmd{};
        cancel_cmd.market_id = kMarketEthUsdc;
        cancel_cmd.opcode = SC_OP_CANCEL_ALL_ACCOUNT;
        cancel_cmd.account_id_low = 2U;
        ASSERT_EQ(live.submit(cancel_cmd, report, fills, 16U), SC_OK);

        ASSERT_EQ(live.sync_wal(), SC_OK);
        live.compute_state_sha256(live_hash);
    }

    // Cold recovery into fresh engine: load snapshot + replay WAL tail
    {
        EngineConfig cfg{};
        cfg.max_orders_per_book = 4096U;
        cfg.wal_mode = WalMode::MEMORY;
        MatchingEngine recovered(cfg);

        uint64_t loaded_seq = 0U;
        ASSERT_EQ(recovered.load_snapshot(snap_file.string(), &loaded_seq), SC_OK);
        EXPECT_EQ(loaded_seq, snap_seq);

        uint64_t last_seq = 0U;
        uint64_t replayed = 0U;
        ASSERT_EQ(recovered.replay_wal(wal_file.string(), loaded_seq, &last_seq, &replayed),
                  SC_OK);
        EXPECT_EQ(last_seq, 96U);
        EXPECT_EQ(replayed, 36U);

        uint8_t recovered_hash[32]{};
        recovered.compute_state_sha256(recovered_hash);
        EXPECT_EQ(std::memcmp(live_hash, recovered_hash, 32U), 0);
    }

    // Corrupt snapshot detection
    {
        std::fstream fs(snap_file, std::ios::in | std::ios::out | std::ios::binary);
        fs.seekp(10, std::ios::beg);
        char bad = static_cast<char>(0xAA);
        fs.write(&bad, 1);
    }
    MatchingEngine bad_target(EngineConfig{4096U, 1000LL, WalMode::MEMORY, ""});
    EXPECT_EQ(bad_target.load_snapshot(snap_file.string(), nullptr), SC_ERR_CORRUPT_SNAPSHOT);

    std::filesystem::remove(wal_file);
    std::filesystem::remove(snap_file);
}

TEST(WalReplayTest, GroupCommitBatchBufferingAndExplicitSync) {
    const std::filesystem::path wal_file = temp_path("settlecore_test_group_commit.wal");
    std::filesystem::remove(wal_file);

    {
        WalWriter writer;
        ASSERT_EQ(writer.open(wal_file.string(), WalMode::GROUP_COMMIT, true, nullptr, 64U), SC_OK);

        // Append 20 records (< 64): must remain in user-space page-aligned buffer until sync().
        for (uint64_t i = 1U; i <= 20U; ++i) {
            WalRecord rec{};
            rec.opcode = static_cast<uint8_t>(WalOpcode::SUBMIT_ORDER);
            rec.market_id = kMarketEthUsdc;
            rec.wal_seq = i;
            rec.order_id = i;
            rec.price_1e8 = 2000LL * kScale1e8;
            rec.qty_1e8 = 1LL * kScale1e8;
            ASSERT_EQ(writer.append(rec), SC_OK);
        }
        EXPECT_EQ(writer.group_pending_count(), 20U);

        std::vector<WalRecord> before_sync;
        ASSERT_EQ(WalReader::read_all(wal_file.string(), before_sync, false, nullptr), SC_OK);
        EXPECT_EQ(before_sync.size(), 0U);

        ASSERT_EQ(writer.sync(), SC_OK);
        EXPECT_EQ(writer.group_pending_count(), 0U);

        std::vector<WalRecord> after_sync;
        ASSERT_EQ(WalReader::read_all(wal_file.string(), after_sync, false, nullptr), SC_OK);
        ASSERT_EQ(after_sync.size(), 20U);

        // Append 150 more records: auto-flushes at 64 and 128, leaving 22 pending until sync().
        for (uint64_t i = 21U; i <= 170U; ++i) {
            WalRecord rec{};
            rec.opcode = static_cast<uint8_t>(WalOpcode::SUBMIT_ORDER);
            rec.market_id = kMarketEthUsdc;
            rec.wal_seq = i;
            rec.order_id = i;
            rec.price_1e8 = 2000LL * kScale1e8;
            rec.qty_1e8 = 1LL * kScale1e8;
            ASSERT_EQ(writer.append(rec), SC_OK);
        }
        EXPECT_EQ(writer.group_pending_count(), 22U);

        std::vector<WalRecord> mid_flush;
        ASSERT_EQ(WalReader::read_all(wal_file.string(), mid_flush, false, nullptr), SC_OK);
        EXPECT_EQ(mid_flush.size(), 148U);

        ASSERT_EQ(writer.sync(), SC_OK);
        EXPECT_EQ(writer.group_pending_count(), 0U);
    }

    std::vector<WalRecord> all_recs;
    ASSERT_EQ(WalReader::read_all(wal_file.string(), all_recs, false, nullptr), SC_OK);
    ASSERT_EQ(all_recs.size(), 170U);
    for (uint64_t i = 0U; i < 170U; ++i) {
        EXPECT_EQ(all_recs[i].wal_seq, i + 1U);
        EXPECT_TRUE(verify_wal_record(all_recs[i]));
    }

    std::filesystem::remove(wal_file);
}

TEST(WalReplayTest, EventLogWriterRoundTripAndIntegrity) {
    const std::filesystem::path wal_file = temp_path("settlecore_test_evlog.wal");
    const std::filesystem::path ev_file = temp_path("settlecore_test_evlog.wal.evlog");
    std::filesystem::remove(wal_file);
    std::filesystem::remove(ev_file);

    {
        EngineConfig cfg{};
        cfg.max_orders_per_book = 1024U;
        cfg.cb_collar_bps = 1000LL;
        cfg.wal_mode = WalMode::GROUP_COMMIT;
        cfg.wal_path = wal_file.string();

        MatchingEngine engine(cfg);
        ASSERT_TRUE(engine.is_ok());
        ASSERT_EQ(engine.set_mark_price(kMarketEthUsdc, 2000LL * kScale1e8, 1000LL), SC_OK);

        sc_exec_report_t report{};
        sc_fill_t fills[8]{};

        // 1. Resting SELL 3 @ 2000 -> ORDER_ACCEPTED
        sc_order_cmd_t s1{};
        s1.market_id = kMarketEthUsdc;
        s1.opcode = SC_OP_SUBMIT_ORDER;
        s1.order_id = 10U;
        s1.account_id_low = 1U;
        s1.side = SC_SIDE_SELL;
        s1.price_1e8 = 2000LL * kScale1e8;
        s1.qty_1e8 = 3LL * kScale1e8;
        ASSERT_EQ(engine.submit(s1, report, fills, 8U), SC_OK);

        // 2. Crossing BUY 2 @ 2000 -> TRADE_FILL (maker not terminal, taker terminal)
        sc_order_cmd_t b1{};
        b1.market_id = kMarketEthUsdc;
        b1.opcode = SC_OP_SUBMIT_ORDER;
        b1.order_id = 11U;
        b1.account_id_low = 2U;
        b1.side = SC_SIDE_BUY;
        b1.price_1e8 = 2000LL * kScale1e8;
        b1.qty_1e8 = 2LL * kScale1e8;
        ASSERT_EQ(engine.submit(b1, report, fills, 8U), SC_OK);

        // 3. Cancel remaining 1 @ 2000 -> ORDER_CANCELED
        sc_order_cmd_t c1{};
        c1.market_id = kMarketEthUsdc;
        c1.opcode = SC_OP_CANCEL_ORDER;
        c1.order_id = 10U;
        ASSERT_EQ(engine.submit(c1, report, nullptr, 0U), SC_OK);

        // 4. Unknown market -> ORDER_REJECTED
        sc_order_cmd_t bad{};
        bad.market_id = 99U;
        bad.opcode = SC_OP_SUBMIT_ORDER;
        bad.order_id = 12U;
        bad.price_1e8 = 2000LL * kScale1e8;
        bad.qty_1e8 = 1LL * kScale1e8;
        EXPECT_EQ(engine.submit(bad, report, nullptr, 0U), SC_ERR_UNKNOWN_MARKET);

        ASSERT_EQ(engine.sync_wal(), SC_OK);
    }

    std::vector<WalEventRecord> events;
    ASSERT_EQ(read_event_log(ev_file.string(), events, false, nullptr), SC_OK);
    ASSERT_EQ(events.size(), 4U);

    EXPECT_EQ(events[0].magic, kEventLogMagic);
    EXPECT_EQ(events[0].event_type, static_cast<uint8_t>(WalEventType::ORDER_ACCEPTED));
    EXPECT_EQ(events[0].order_id, 10U);
    EXPECT_EQ(events[0].status, SC_STATUS_ACCEPTED);
    EXPECT_EQ(events[0].fill_qty_1e8, 3LL * kScale1e8);
    EXPECT_TRUE(verify_event_record(events[0]));

    EXPECT_EQ(events[1].event_type, static_cast<uint8_t>(WalEventType::TRADE_FILL));
    EXPECT_EQ(events[1].order_id, 11U);
    EXPECT_EQ(events[1].maker_order_id, 10U);
    EXPECT_EQ(events[1].trade_id, 1U);
    EXPECT_EQ(events[1].exec_price_1e8, 2000LL * kScale1e8);
    EXPECT_EQ(events[1].fill_qty_1e8, 2LL * kScale1e8);
    EXPECT_EQ(events[1].flags & kEventFlagMakerTerminal, 0U);
    EXPECT_NE(events[1].flags & kEventFlagTakerTerminal, 0U);

    EXPECT_EQ(events[2].event_type, static_cast<uint8_t>(WalEventType::ORDER_CANCELED));
    EXPECT_EQ(events[2].order_id, 10U);
    EXPECT_EQ(events[2].status, SC_STATUS_CANCELED);
    EXPECT_EQ(events[2].fill_qty_1e8, 1LL * kScale1e8);

    EXPECT_EQ(events[3].event_type, static_cast<uint8_t>(WalEventType::ORDER_REJECTED));
    EXPECT_EQ(events[3].order_id, 12U);
    EXPECT_EQ(events[3].status, SC_STATUS_REJECTED);

    // Append 11 torn trailing bytes and verify truncation recovery.
    {
        std::ofstream ofs(ev_file, std::ios::binary | std::ios::app);
        const char junk[11] = {'T', 'O', 'R', 'N', '_', 'E', 'V', 'L', 'O', 'G', '!'};
        ofs.write(junk, sizeof(junk));
    }
    EXPECT_EQ(read_event_log(ev_file.string(), events, false, nullptr), SC_ERR_CORRUPT_WAL);
    size_t dropped = 0U;
    ASSERT_EQ(read_event_log(ev_file.string(), events, true, &dropped), SC_OK);
    EXPECT_EQ(dropped, 11U);
    EXPECT_EQ(events.size(), 4U);

    std::filesystem::remove(wal_file);
    std::filesystem::remove(ev_file);
}

}  // namespace
}  // namespace settlecore
