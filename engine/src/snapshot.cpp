#include "settlecore/snapshot.hpp"

#include <cstring>
#include <fstream>
#include <vector>

#include "settlecore/matching_engine.hpp"
#include "settlecore/wal.hpp"

namespace settlecore {

namespace {

#pragma pack(push, 1)
struct SnapshotHeader {
    uint32_t magic;
    uint32_t version;
    uint64_t wal_seq;
    uint64_t next_trade_id;
    uint32_t market_count;
    uint32_t payload_crc32c;
    uint64_t total_orders;
    uint32_t header_crc32c;
    uint8_t reserved[20];
};
#pragma pack(pop)
static_assert(sizeof(SnapshotHeader) == 64, "SnapshotHeader must be 64 bytes");

#pragma pack(push, 1)
struct SnapshotMarketHeader {
    uint16_t market_id;
    uint8_t reserved[6];
    int64_t mark_price_1e8;
    int64_t collar_bps;
    uint64_t order_count;
};
#pragma pack(pop)
static_assert(sizeof(SnapshotMarketHeader) == 32, "SnapshotMarketHeader must be 32 bytes");

#pragma pack(push, 1)
struct SnapshotOrderRecord {
    uint64_t order_id;
    uint64_t account_id_low;
    uint64_t account_id_high;
    int64_t price_1e8;
    int64_t remaining_1e8;
    uint64_t timestamp_ns;
    uint16_t market_id;
    uint8_t side;
    uint8_t order_type;
    uint8_t time_in_force;
    uint8_t stp_mode;
    uint16_t reserved;
};
#pragma pack(pop)
static_assert(sizeof(SnapshotOrderRecord) == 56, "SnapshotOrderRecord must be 56 bytes");

void append_bytes(std::vector<uint8_t>& buf, const void* ptr, size_t len) {
    const auto* bytes = static_cast<const uint8_t*>(ptr);
    buf.insert(buf.end(), bytes, bytes + len);
}

}  // namespace

std::string Snapshot::make_filename(const std::string& dir, uint64_t wal_seq) {
    if (dir.empty()) {
        return "snapshot_" + std::to_string(wal_seq) + ".snap";
    }
    const char back = dir.back();
    if (back == '/' || back == '\\') {
        return dir + "snapshot_" + std::to_string(wal_seq) + ".snap";
    }
    return dir + "/snapshot_" + std::to_string(wal_seq) + ".snap";
}

int32_t Snapshot::write(const MatchingEngine& engine, const std::string& path) {
    if (path.empty()) {
        return SC_ERR_INVALID_ARG;
    }

    std::vector<uint8_t> payload;
    uint64_t total_orders = 0U;
    const auto& markets = engine.markets();

    for (const auto& kv : markets) {
        const MarketSlot& slot = *kv.second;
        SnapshotMarketHeader mh{};
        mh.market_id = slot.market_id;
        mh.mark_price_1e8 = slot.breaker.mark_price();
        mh.collar_bps = slot.breaker.collar_bps();
        mh.order_count = slot.book.active_order_count();
        total_orders += mh.order_count;
        append_bytes(payload, &mh, sizeof(mh));

        slot.book.for_each_resting_order_canonical([&payload](const OrderNode& n) {
            SnapshotOrderRecord rec{};
            rec.order_id = n.order_id;
            rec.account_id_low = n.account_id_low;
            rec.account_id_high = n.account_id_high;
            rec.price_1e8 = n.price_1e8;
            rec.remaining_1e8 = n.remaining_1e8;
            rec.timestamp_ns = n.timestamp_ns;
            rec.market_id = n.market_id;
            rec.side = n.side;
            rec.order_type = n.order_type;
            rec.time_in_force = n.time_in_force;
            rec.stp_mode = n.stp_mode;
            rec.reserved = 0U;
            append_bytes(payload, &rec, sizeof(rec));
        });
    }

    SnapshotHeader hdr{};
    hdr.magic = kSnapshotMagic;
    hdr.version = kSnapshotVersion;
    hdr.wal_seq = engine.wal_seq();
    hdr.next_trade_id = engine.next_trade_id();
    hdr.market_count = static_cast<uint32_t>(markets.size());
    hdr.payload_crc32c =
        payload.empty() ? 0U : crc32c(payload.data(), payload.size());
    hdr.total_orders = total_orders;
    hdr.header_crc32c =
        crc32c(reinterpret_cast<const uint8_t*>(&hdr), offsetof(SnapshotHeader, header_crc32c));

    std::ofstream ofs(path, std::ios::binary | std::ios::trunc);
    if (!ofs.is_open()) {
        return SC_ERR_IO;
    }
    ofs.write(reinterpret_cast<const char*>(&hdr), sizeof(hdr));
    if (!payload.empty()) {
        ofs.write(reinterpret_cast<const char*>(payload.data()),
                  static_cast<std::streamsize>(payload.size()));
    }
    ofs.flush();
    if (!ofs.good()) {
        return SC_ERR_IO;
    }
    return SC_OK;
}

int32_t Snapshot::load(MatchingEngine& engine,
                       const std::string& path,
                       uint64_t* out_snapshot_seq) {
    if (path.empty()) {
        return SC_ERR_INVALID_ARG;
    }

    std::ifstream ifs(path, std::ios::binary | std::ios::ate);
    if (!ifs.is_open()) {
        return SC_ERR_IO;
    }

    const std::ifstream::pos_type end_pos = ifs.tellg();
    if (end_pos < static_cast<std::ifstream::pos_type>(sizeof(SnapshotHeader))) {
        return SC_ERR_CORRUPT_SNAPSHOT;
    }
    const size_t total_size = static_cast<size_t>(end_pos);
    ifs.seekg(0, std::ios::beg);

    SnapshotHeader hdr{};
    ifs.read(reinterpret_cast<char*>(&hdr), sizeof(hdr));
    if (!ifs.good() || hdr.magic != kSnapshotMagic || hdr.version != kSnapshotVersion) {
        return SC_ERR_CORRUPT_SNAPSHOT;
    }

    const uint32_t expected_hdr_crc =
        crc32c(reinterpret_cast<const uint8_t*>(&hdr), offsetof(SnapshotHeader, header_crc32c));
    if (hdr.header_crc32c != expected_hdr_crc) {
        return SC_ERR_CORRUPT_SNAPSHOT;
    }

    const size_t payload_size = total_size - sizeof(SnapshotHeader);
    std::vector<uint8_t> payload(payload_size);
    if (payload_size > 0U) {
        ifs.read(reinterpret_cast<char*>(payload.data()),
                 static_cast<std::streamsize>(payload_size));
        if (!ifs.good()) {
            return SC_ERR_CORRUPT_SNAPSHOT;
        }
    }

    const uint32_t actual_payload_crc =
        payload.empty() ? 0U : crc32c(payload.data(), payload.size());
    if (actual_payload_crc != hdr.payload_crc32c) {
        return SC_ERR_CORRUPT_SNAPSHOT;
    }

    // Parse payload before mutating engine state.
    struct ParsedMarket {
        SnapshotMarketHeader header{};
        std::vector<SnapshotOrderRecord> orders;
    };
    std::vector<ParsedMarket> parsed_markets;
    parsed_markets.reserve(hdr.market_count);

    size_t offset = 0U;
    uint64_t counted_orders = 0U;
    for (uint32_t m = 0U; m < hdr.market_count; ++m) {
        if (offset + sizeof(SnapshotMarketHeader) > payload_size) {
            return SC_ERR_CORRUPT_SNAPSHOT;
        }
        ParsedMarket pm;
        std::memcpy(&pm.header, payload.data() + offset, sizeof(SnapshotMarketHeader));
        offset += sizeof(SnapshotMarketHeader);

        const size_t orders_bytes =
            static_cast<size_t>(pm.header.order_count) * sizeof(SnapshotOrderRecord);
        if (pm.header.order_count > (payload_size - offset) / sizeof(SnapshotOrderRecord)) {
            return SC_ERR_CORRUPT_SNAPSHOT;
        }
        pm.orders.resize(static_cast<size_t>(pm.header.order_count));
        if (orders_bytes > 0U) {
            std::memcpy(pm.orders.data(), payload.data() + offset, orders_bytes);
            offset += orders_bytes;
        }
        counted_orders += pm.header.order_count;
        parsed_markets.push_back(std::move(pm));
    }

    if (offset != payload_size || counted_orders != hdr.total_orders) {
        return SC_ERR_CORRUPT_SNAPSHOT;
    }

    engine.reset_state(hdr.wal_seq, hdr.next_trade_id);
    for (const ParsedMarket& pm : parsed_markets) {
        const int32_t rc = engine.ensure_market(pm.header.market_id);
        if (rc != SC_OK) {
            return rc;
        }
        CircuitBreaker* cb = engine.get_breaker(pm.header.market_id);
        if (cb != nullptr) {
            cb->set_collar_bps(pm.header.collar_bps);
            cb->set_mark_price(pm.header.mark_price_1e8);
        }
        OrderBook* book = engine.get_book(pm.header.market_id);
        for (const SnapshotOrderRecord& rec : pm.orders) {
            OrderNode node{};
            node.order_id = rec.order_id;
            node.account_id_low = rec.account_id_low;
            node.account_id_high = rec.account_id_high;
            node.price_1e8 = rec.price_1e8;
            node.remaining_1e8 = rec.remaining_1e8;
            node.timestamp_ns = rec.timestamp_ns;
            node.next_idx = kNullIdx;
            node.prev_idx = kNullIdx;
            node.market_id = rec.market_id;
            node.side = rec.side;
            node.order_type = rec.order_type;
            node.time_in_force = rec.time_in_force;
            node.stp_mode = rec.stp_mode;
            node.reserved_pad = 0U;
            const int32_t restore_rc = book->restore_resting_order(node);
            if (restore_rc != SC_OK) {
                return restore_rc;
            }
        }
    }

    if (out_snapshot_seq != nullptr) {
        *out_snapshot_seq = hdr.wal_seq;
    }
    return SC_OK;
}

}  // namespace settlecore
