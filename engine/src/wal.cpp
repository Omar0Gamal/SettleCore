#include "settlecore/wal.hpp"

#include <algorithm>
#include <array>
#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <new>
#include <sys/stat.h>
#include <type_traits>

#if defined(_WIN32)
#include <io.h>
#if defined(_MSC_VER) || defined(__clang__)
#include <intrin.h>
#endif
#else
#include <unistd.h>
#if defined(__x86_64__) || defined(__i386__)
#include <cpuid.h>
#endif
#endif

#if defined(__x86_64__) || defined(__i386__) || defined(_M_X64)
#include <nmmintrin.h>
#endif

namespace settlecore {

namespace {

constexpr uint32_t kCrc32cPoly = 0x82F63B78U;
constexpr uint64_t kBufferedSyncBatch = 1024U;
constexpr size_t kPageAlignBytes = 4096U;

constexpr std::array<uint32_t, 256> make_crc32c_table() noexcept {
    std::array<uint32_t, 256> table{};
    for (uint32_t i = 0U; i < 256U; ++i) {
        uint32_t crc = i;
        for (uint32_t bit = 0U; bit < 8U; ++bit) {
            crc = (crc & 1U) ? ((crc >> 1U) ^ kCrc32cPoly) : (crc >> 1U);
        }
        table[i] = crc;
    }
    return table;
}

constexpr std::array<uint32_t, 256> kCrc32cTable = make_crc32c_table();

bool cpu_has_sse42() noexcept {
#if defined(__x86_64__) || defined(__i386__) || defined(_M_X64)
#if defined(_WIN32)
    int regs[4] = {0, 0, 0, 0};
    __cpuid(regs, 1);
    return (static_cast<uint32_t>(regs[2]) & (1U << 20U)) != 0U;
#else
    unsigned int eax = 0U;
    unsigned int ebx = 0U;
    unsigned int ecx = 0U;
    unsigned int edx = 0U;
    if (__get_cpuid(1U, &eax, &ebx, &ecx, &edx) == 0) {
        return false;
    }
    return (ecx & (1U << 20U)) != 0U;
#endif
#else
    return false;
#endif
}

#if defined(__x86_64__) || defined(_M_X64)
#if defined(__GNUC__) || defined(__clang__)
__attribute__((target("sse4.2")))
#endif
uint32_t crc32c_hw(const uint8_t* data, size_t len, uint32_t seed) noexcept {
    uint32_t crc = ~seed;
    size_t offset = 0U;
    while (offset + 8U <= len) {
        uint64_t word = 0U;
        std::memcpy(&word, data + offset, sizeof(word));
        crc = static_cast<uint32_t>(_mm_crc32_u64(crc, word));
        offset += 8U;
    }
    while (offset < len) {
        crc = _mm_crc32_u8(crc, data[offset]);
        ++offset;
    }
    return ~crc;
}
#endif

int os_open_rw(const char* path) noexcept {
#if defined(_WIN32)
    return ::_open(path, _O_RDWR | _O_CREAT | _O_BINARY, _S_IREAD | _S_IWRITE);
#else
    return ::open(path, O_RDWR | O_CREAT, 0644);
#endif
}

int os_open_ro(const char* path) noexcept {
#if defined(_WIN32)
    return ::_open(path, _O_RDONLY | _O_BINARY);
#else
    return ::open(path, O_RDONLY);
#endif
}

int os_close(int fd) noexcept {
#if defined(_WIN32)
    return ::_close(fd);
#else
    return ::close(fd);
#endif
}

int64_t os_file_size(int fd) noexcept {
#if defined(_WIN32)
    return ::_filelengthi64(fd);
#else
    struct stat st {};
    if (::fstat(fd, &st) != 0) {
        return -1;
    }
    return static_cast<int64_t>(st.st_size);
#endif
}

int64_t os_seek_set(int fd, int64_t offset) noexcept {
#if defined(_WIN32)
    return ::_lseeki64(fd, offset, SEEK_SET);
#else
    return static_cast<int64_t>(::lseek(fd, static_cast<off_t>(offset), SEEK_SET));
#endif
}

int32_t os_read_exact(int fd, uint8_t* buf, size_t len) noexcept {
    size_t done = 0U;
    while (done < len) {
#if defined(_WIN32)
        const int n = ::_read(fd, buf + done, static_cast<unsigned int>(len - done));
#else
        const ssize_t n = ::read(fd, buf + done, len - done);
#endif
        if (n < 0) {
            if (errno == EINTR) {
                continue;
            }
            return -1;
        }
        if (n == 0) {
            return static_cast<int32_t>(done);
        }
        done += static_cast<size_t>(n);
    }
    return static_cast<int32_t>(done);
}

bool os_write_all(int fd, const uint8_t* buf, size_t len) noexcept {
    size_t done = 0U;
    while (done < len) {
#if defined(_WIN32)
        const int n = ::_write(fd, buf + done, static_cast<unsigned int>(len - done));
#else
        const ssize_t n = ::write(fd, buf + done, len - done);
#endif
        if (n < 0) {
            if (errno == EINTR) {
                continue;
            }
            return false;
        }
        done += static_cast<size_t>(n);
    }
    return true;
}

bool os_truncate(int fd, int64_t size) noexcept {
#if defined(_WIN32)
    return ::_chsize_s(fd, size) == 0;
#else
    return ::ftruncate(fd, static_cast<off_t>(size)) == 0;
#endif
}

bool os_fdatasync(int fd) noexcept {
#if defined(_WIN32)
    return ::_commit(fd) == 0;
#elif defined(__linux__)
    return ::fdatasync(fd) == 0;
#else
    return ::fsync(fd) == 0;
#endif
}

}  // namespace

uint32_t crc32c_sw(const uint8_t* data, size_t len, uint32_t seed) noexcept {
    uint32_t crc = ~seed;
    for (size_t i = 0U; i < len; ++i) {
        crc = (crc >> 8U) ^ kCrc32cTable[(crc ^ data[i]) & 0xFFU];
    }
    return ~crc;
}

uint32_t crc32c(const uint8_t* data, size_t len, uint32_t seed) noexcept {
#if defined(__x86_64__) || defined(_M_X64)
    static const bool has_hw = cpu_has_sse42();
    if (has_hw) {
        return crc32c_hw(data, len, seed);
    }
#endif
    return crc32c_sw(data, len, seed);
}

void finalize_wal_record(WalRecord& rec) noexcept {
    rec.magic = kWalMagic;
    rec.version = kWalVersion;
    rec.reserved = 0U;
    const auto* raw = reinterpret_cast<const uint8_t*>(&rec);
    rec.crc32c = crc32c(raw + 16U, sizeof(WalRecord) - 16U);
}

bool verify_wal_record(const WalRecord& rec) noexcept {
    if (rec.magic != kWalMagic || rec.version != kWalVersion) {
        return false;
    }
    const auto* raw = reinterpret_cast<const uint8_t*>(&rec);
    const uint32_t expected = crc32c(raw + 16U, sizeof(WalRecord) - 16U);
    return rec.crc32c == expected;
}

void finalize_event_record(WalEventRecord& rec) noexcept {
    rec.magic = kEventLogMagic;
    rec.version = kEventLogVersion;
    rec.reserved = 0U;
    const auto* raw = reinterpret_cast<const uint8_t*>(&rec);
    rec.crc32c = crc32c(raw + 16U, sizeof(WalEventRecord) - 16U);
}

bool verify_event_record(const WalEventRecord& rec) noexcept {
    if (rec.magic != kEventLogMagic || rec.version != kEventLogVersion) {
        return false;
    }
    const auto* raw = reinterpret_cast<const uint8_t*>(&rec);
    const uint32_t expected = crc32c(raw + 16U, sizeof(WalEventRecord) - 16U);
    return rec.crc32c == expected;
}

namespace {

inline void finalize_record(WalRecord& rec) noexcept {
    finalize_wal_record(rec);
}

inline void finalize_record(WalEventRecord& rec) noexcept {
    finalize_event_record(rec);
}

inline bool verify_record(const WalRecord& rec) noexcept {
    return verify_wal_record(rec);
}

inline bool verify_record(const WalEventRecord& rec) noexcept {
    return verify_event_record(rec);
}

template <typename RecordT>
int32_t read_binary_log_all(const std::string& path,
                            std::vector<RecordT>& out_records,
                            bool truncate_torn_tail,
                            size_t* out_truncated_bytes) {
    out_records.clear();
    if (out_truncated_bytes != nullptr) {
        *out_truncated_bytes = 0U;
    }

    const int fd = truncate_torn_tail ? os_open_rw(path.c_str()) : os_open_ro(path.c_str());
    if (fd < 0) {
        return SC_ERR_IO;
    }

    const int64_t fsize = os_file_size(fd);
    if (fsize < 0) {
        os_close(fd);
        return SC_ERR_IO;
    }

    const int64_t full_frames = fsize / static_cast<int64_t>(sizeof(RecordT));
    out_records.reserve(static_cast<size_t>(full_frames));

    int64_t valid_end = 0;
    for (int64_t i = 0; i < full_frames; ++i) {
        RecordT rec{};
        if (os_read_exact(fd, reinterpret_cast<uint8_t*>(&rec), sizeof(rec)) !=
            static_cast<int32_t>(sizeof(rec))) {
            os_close(fd);
            return SC_ERR_IO;
        }
        if (!verify_record(rec)) {
            if (truncate_torn_tail && i + 1 == full_frames) {
                break;
            }
            os_close(fd);
            return SC_ERR_CORRUPT_WAL;
        }
        out_records.push_back(rec);
        valid_end += static_cast<int64_t>(sizeof(RecordT));
    }

    if (valid_end < fsize) {
        if (!truncate_torn_tail) {
            os_close(fd);
            return SC_ERR_CORRUPT_WAL;
        }
        const size_t dropped = static_cast<size_t>(fsize - valid_end);
        if (!os_truncate(fd, valid_end) || !os_fdatasync(fd)) {
            os_close(fd);
            return SC_ERR_IO;
        }
        if (out_truncated_bytes != nullptr) {
            *out_truncated_bytes = dropped;
        }
    }

    os_close(fd);
    return SC_OK;
}

}  // namespace

namespace detail {

template <typename RecordT>
BinaryLogWriter<RecordT>::~BinaryLogWriter() {
    close();
}

template <typename RecordT>
bool BinaryLogWriter<RecordT>::is_open() const noexcept {
    return mode_ == WalMode::MEMORY || fd_ >= 0;
}

template <typename RecordT>
void BinaryLogWriter<RecordT>::allocate_group_buffer(uint32_t max_batch) {
    free_group_buffer();
    group_commit_max_batch_ = std::max(max_batch, 1U);
    const size_t raw_bytes = static_cast<size_t>(group_commit_max_batch_) * sizeof(RecordT);
    const size_t aligned_bytes = ((raw_bytes + kPageAlignBytes - 1U) / kPageAlignBytes) * kPageAlignBytes;
    void* mem = ::operator new(aligned_bytes, std::align_val_t{kPageAlignBytes});
    std::memset(mem, 0, aligned_bytes);
    group_buf_ = static_cast<RecordT*>(mem);
    group_pending_count_ = 0U;
}

template <typename RecordT>
void BinaryLogWriter<RecordT>::free_group_buffer() noexcept {
    if (group_buf_ != nullptr) {
        ::operator delete(group_buf_, std::align_val_t{kPageAlignBytes});
        group_buf_ = nullptr;
    }
    group_pending_count_ = 0U;
}

template <typename RecordT>
int32_t BinaryLogWriter<RecordT>::flush_group_buffer() noexcept {
    if (group_pending_count_ == 0U) {
        return SC_OK;
    }
    if (fd_ < 0 || group_buf_ == nullptr) {
        return SC_ERR_IO;
    }
    const size_t bytes = static_cast<size_t>(group_pending_count_) * sizeof(RecordT);
    if (!os_write_all(fd_, reinterpret_cast<const uint8_t*>(group_buf_), bytes)) {
        return SC_ERR_IO;
    }
    group_pending_count_ = 0U;
    if (!os_fdatasync(fd_)) {
        return SC_ERR_IO;
    }
    buffered_since_sync_ = 0U;
    return SC_OK;
}

template <typename RecordT>
int32_t BinaryLogWriter<RecordT>::open(const std::string& path,
                                       WalMode mode,
                                       bool truncate_torn_tail,
                                       size_t* out_truncated_bytes,
                                       uint32_t group_commit_max_batch) {
    close();
    mode_ = mode;
    path_ = path;
    record_count_ = 0U;
    buffered_since_sync_ = 0U;
    group_commit_max_batch_ = std::max(group_commit_max_batch, 1U);
    if (out_truncated_bytes != nullptr) {
        *out_truncated_bytes = 0U;
    }

    if (mode_ == WalMode::MEMORY || path_.empty()) {
        mode_ = WalMode::MEMORY;
        return SC_OK;
    }

    fd_ = os_open_rw(path_.c_str());
    if (fd_ < 0) {
        return SC_ERR_IO;
    }

    const int64_t fsize = os_file_size(fd_);
    if (fsize < 0) {
        close();
        return SC_ERR_IO;
    }

    int64_t valid_end = 0;
    const int64_t full_frames = fsize / static_cast<int64_t>(sizeof(RecordT));
    for (int64_t i = 0; i < full_frames; ++i) {
        RecordT rec{};
        if (os_read_exact(fd_, reinterpret_cast<uint8_t*>(&rec), sizeof(rec)) !=
            static_cast<int32_t>(sizeof(rec))) {
            close();
            return SC_ERR_IO;
        }
        if (!verify_record(rec)) {
            if (truncate_torn_tail && i + 1 == full_frames) {
                break;
            }
            close();
            return SC_ERR_CORRUPT_WAL;
        }
        valid_end += static_cast<int64_t>(sizeof(RecordT));
        ++record_count_;
    }

    if (valid_end < fsize) {
        if (!truncate_torn_tail) {
            close();
            return SC_ERR_CORRUPT_WAL;
        }
        const size_t dropped = static_cast<size_t>(fsize - valid_end);
        if (!os_truncate(fd_, valid_end) || !os_fdatasync(fd_)) {
            close();
            return SC_ERR_IO;
        }
        if (out_truncated_bytes != nullptr) {
            *out_truncated_bytes = dropped;
        }
    }

    if (os_seek_set(fd_, valid_end) < 0) {
        close();
        return SC_ERR_IO;
    }

    if (mode_ == WalMode::GROUP_COMMIT) {
        allocate_group_buffer(group_commit_max_batch_);
    }
    return SC_OK;
}

template <typename RecordT>
void BinaryLogWriter<RecordT>::close() noexcept {
    if (fd_ >= 0) {
        if (mode_ == WalMode::GROUP_COMMIT) {
            (void)flush_group_buffer();
        } else if (mode_ != WalMode::MEMORY && buffered_since_sync_ > 0U) {
            os_fdatasync(fd_);
        }
        os_close(fd_);
        fd_ = -1;
    }
    free_group_buffer();
    buffered_since_sync_ = 0U;
}

template <typename RecordT>
int32_t BinaryLogWriter<RecordT>::append(RecordT& rec) {
    finalize_record(rec);

    if (mode_ == WalMode::MEMORY) {
        memory_records_.push_back(rec);
        ++record_count_;
        return SC_OK;
    }
    if (fd_ < 0) {
        return SC_ERR_IO;
    }

    if (mode_ == WalMode::GROUP_COMMIT) {
        if (group_buf_ == nullptr) {
            return SC_ERR_IO;
        }
        group_buf_[group_pending_count_] = rec;
        ++group_pending_count_;
        ++record_count_;
        if (group_pending_count_ >= group_commit_max_batch_) {
            return flush_group_buffer();
        }
        return SC_OK;
    }

    if (!os_write_all(fd_, reinterpret_cast<const uint8_t*>(&rec), sizeof(rec))) {
        return SC_ERR_IO;
    }
    ++record_count_;

    if (mode_ == WalMode::FSYNC) {
        if (!os_fdatasync(fd_)) {
            return SC_ERR_IO;
        }
        buffered_since_sync_ = 0U;
    } else {
        ++buffered_since_sync_;
        if (buffered_since_sync_ >= kBufferedSyncBatch) {
            if (!os_fdatasync(fd_)) {
                return SC_ERR_IO;
            }
            buffered_since_sync_ = 0U;
        }
    }
    return SC_OK;
}

template <typename RecordT>
int32_t BinaryLogWriter<RecordT>::sync() noexcept {
    if (mode_ == WalMode::MEMORY) {
        return SC_OK;
    }
    if (fd_ < 0) {
        return SC_ERR_IO;
    }
    if (mode_ == WalMode::GROUP_COMMIT) {
        return flush_group_buffer();
    }
    if (!os_fdatasync(fd_)) {
        return SC_ERR_IO;
    }
    buffered_since_sync_ = 0U;
    return SC_OK;
}

template class BinaryLogWriter<WalRecord>;
template class BinaryLogWriter<WalEventRecord>;

}  // namespace detail

int32_t WalReader::read_all(const std::string& path,
                            std::vector<WalRecord>& out_records,
                            bool truncate_torn_tail,
                            size_t* out_truncated_bytes) {
    return read_binary_log_all<WalRecord>(path, out_records, truncate_torn_tail,
                                          out_truncated_bytes);
}

int32_t read_event_log(const std::string& path,
                       std::vector<WalEventRecord>& out_records,
                       bool truncate_torn_tail,
                       size_t* out_truncated_bytes) {
    return read_binary_log_all<WalEventRecord>(path, out_records, truncate_torn_tail,
                                               out_truncated_bytes);
}

}  // namespace settlecore
