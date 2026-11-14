#pragma once

#include <cstdint>
#include <string>

#include "settlecore/types.hpp"

namespace settlecore {

class MatchingEngine;

inline constexpr uint32_t kSnapshotMagic = 0x5343534EU;  // "SCSN"
inline constexpr uint32_t kSnapshotVersion = 1U;

class Snapshot {
  public:
    static std::string make_filename(const std::string& dir, uint64_t wal_seq);

    static int32_t write(const MatchingEngine& engine, const std::string& path);

    static int32_t load(MatchingEngine& engine,
                        const std::string& path,
                        uint64_t* out_snapshot_seq = nullptr);
};

}  // namespace settlecore
