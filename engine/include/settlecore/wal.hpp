#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

#include "settlecore/types.hpp"

namespace settlecore {

inline constexpr uint32_t kWalMagic = 0x5343574CU;      // "SCWL"
inline constexpr uint8_t kWalVersion = 1U;
inline constexpr uint32_t kEventLogMagic = 0x53434556U;  // "SCEV"
inline constexpr uint8_t kEventLogVersion = 1U;
inline constexpr uint32_t kDefaultGroupCommitMaxBatch = 64U;

enum class WalEventType : uint8_t {
    ORDER_ACCEPTED = 1,
    TRADE_FILL = 2,
    ORDER_CANCELED = 3,
    ORDER_REJECTED = 4,
};

inline constexpr uint8_t kEventFlagSideSell = 0x01U;
inline constexpr uint8_t kEventFlagMakerTerminal = 0x02U;
inline constexpr uint8_t kEventFlagTakerTerminal = 0x04U;

#pragma pack(push, 1)
struct WalRecord {
    uint32_t magic;            // [0..3]   0x5343574C ("SCWL")
    uint8_t version;           // [4]      1
    uint8_t opcode;            // [5]      WalOpcode
    uint16_t market_id;        // [6..7]
    uint16_t flags;            // [8..9]   packed side/order_type/tif/stp_mode
    uint16_t reserved;         // [10..11] 0x0000
    uint32_t crc32c;           // [12..15] CRC32C over bytes [16..63]
    uint64_t wal_seq;          // [16..23]
    uint64_t order_id;         // [24..31]
    uint64_t account_id_low;   // [32..39]
    uint64_t account_id_high;  // [40..47]
    int64_t price_1e8;         // [48..55]
    int64_t qty_1e8;           // [56..63]
};
#pragma pack(pop)
static_assert(sizeof(WalRecord) == 64, "WalRecord must be exactly 64 bytes");

#pragma pack(push, 1)
struct WalEventRecord {
    uint32_t magic;           // [0..3]   0x53434556 ("SCEV")
    uint8_t version;          // [4]      1
    uint8_t event_type;       // [5]      WalEventType (1..4)
    uint16_t market_id;       // [6..7]
    uint8_t status;           // [8]      OrderStatus / SC_STATUS_*
    uint8_t flags;            // [9]      bit0=side, bit1=maker_terminal, bit2=taker_terminal
    uint16_t reserved;        // [10..11] 0x0000
    uint32_t crc32c;          // [12..15] CRC32C over bytes [16..63]
    uint64_t wal_seq;         // [16..23]
    uint64_t order_id;        // [24..31] taker order_id for TRADE_FILL
    uint64_t maker_order_id;  // [32..39]
    uint64_t trade_id;        // [40..47]
    int64_t exec_price_1e8;   // [48..55]
    int64_t fill_qty_1e8;     // [56..63]
};
#pragma pack(pop)
static_assert(sizeof(WalEventRecord) == 64, "WalEventRecord must be exactly 64 bytes");

inline uint16_t pack_wal_flags(uint8_t side,
                               uint8_t order_type,
                               uint8_t time_in_force,
                               uint8_t stp_mode) noexcept {
    const uint16_t s = static_cast<uint16_t>(side & 0x03U);
    const uint16_t t = static_cast<uint16_t>((order_type & 0x03U) << 2U);
    const uint16_t tif = static_cast<uint16_t>((time_in_force & 0x03U) << 4U);
    const uint16_t stp = static_cast<uint16_t>((stp_mode & 0x03U) << 6U);
    return static_cast<uint16_t>(s | t | tif | stp);
}

inline void unpack_wal_flags(uint16_t flags,
                             uint8_t& out_side,
                             uint8_t& out_order_type,
                             uint8_t& out_tif,
                             uint8_t& out_stp_mode) noexcept {
    out_side = static_cast<uint8_t>(flags & 0x03U);
    out_order_type = static_cast<uint8_t>((flags >> 2U) & 0x03U);
    out_tif = static_cast<uint8_t>((flags >> 4U) & 0x03U);
    out_stp_mode = static_cast<uint8_t>((flags >> 6U) & 0x03U);
}

// Software Castagnoli CRC32C (0x82F63B78).
uint32_t crc32c_sw(const uint8_t* data, size_t len, uint32_t seed = 0U) noexcept;

// Castagnoli CRC32C using hardware SSE4.2 when available, falling back to software table.
uint32_t crc32c(const uint8_t* data, size_t len, uint32_t seed = 0U) noexcept;

// Populate magic, version, reserved=0, and crc32c over bytes [16..63].
void finalize_wal_record(WalRecord& rec) noexcept;

// Validate magic, version, and crc32c over bytes [16..63].
[[nodiscard]] bool verify_wal_record(const WalRecord& rec) noexcept;

// Populate magic, version, reserved=0, and crc32c over bytes [16..63] for execution events.
void finalize_event_record(WalEventRecord& rec) noexcept;

// Validate magic, version, and crc32c over bytes [16..63] for execution events.
[[nodiscard]] bool verify_event_record(const WalEventRecord& rec) noexcept;

namespace detail {

// Shared page-aligned 64-byte binary log writer backing both WalWriter and EventLogWriter.
template <typename RecordT>
class BinaryLogWriter {
  public:
    BinaryLogWriter() = default;
    ~BinaryLogWriter();

    BinaryLogWriter(const BinaryLogWriter&) = delete;
    BinaryLogWriter& operator=(const BinaryLogWriter&) = delete;

    int32_t open(const std::string& path,
                 WalMode mode,
                 bool truncate_torn_tail = true,
                 size_t* out_truncated_bytes = nullptr,
                 uint32_t group_commit_max_batch = kDefaultGroupCommitMaxBatch);

    void close() noexcept;

    int32_t append(RecordT& rec);
    int32_t sync() noexcept;

    [[nodiscard]] WalMode mode() const noexcept { return mode_; }
    [[nodiscard]] bool is_open() const noexcept;
    [[nodiscard]] uint64_t record_count() const noexcept { return record_count_; }
    [[nodiscard]] uint32_t group_commit_max_batch() const noexcept {
        return group_commit_max_batch_;
    }
    [[nodiscard]] uint32_t group_pending_count() const noexcept { return group_pending_count_; }
    [[nodiscard]] const std::vector<RecordT>& memory_records() const noexcept {
        return memory_records_;
    }

  private:
    int32_t flush_group_buffer() noexcept;
    void allocate_group_buffer(uint32_t max_batch);
    void free_group_buffer() noexcept;

    WalMode mode_ = WalMode::MEMORY;
    std::string path_;
    int fd_ = -1;
    uint64_t record_count_ = 0;
    uint64_t buffered_since_sync_ = 0;
    uint32_t group_commit_max_batch_ = kDefaultGroupCommitMaxBatch;
    uint32_t group_pending_count_ = 0;
    RecordT* group_buf_ = nullptr;
    std::vector<RecordT> memory_records_;
};

}  // namespace detail

using WalWriter = detail::BinaryLogWriter<WalRecord>;
using EventLogWriter = detail::BinaryLogWriter<WalEventRecord>;

class WalReader {
  public:
    // Read all valid 64-byte records from `path`.
    // When `truncate_torn_tail` is true, incomplete or corrupt trailing frame at EOF
    // is truncated safely; mid-file corruption always returns SC_ERR_CORRUPT_WAL.
    static int32_t read_all(const std::string& path,
                            std::vector<WalRecord>& out_records,
                            bool truncate_torn_tail = false,
                            size_t* out_truncated_bytes = nullptr);
};

// Read all valid 64-byte execution event records from `path` (`wal_path + ".evlog"`).
int32_t read_event_log(const std::string& path,
                       std::vector<WalEventRecord>& out_records,
                       bool truncate_torn_tail = false,
                       size_t* out_truncated_bytes = nullptr);

}  // namespace settlecore
