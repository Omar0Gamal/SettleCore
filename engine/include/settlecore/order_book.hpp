#pragma once

#include <cstddef>
#include <cstdint>
#include <vector>

#include "settlecore/circuit_breaker.hpp"
#include "settlecore/types.hpp"

namespace settlecore {

struct PriceLevel {
    int64_t price_1e8 = 0;
    int64_t total_volume_1e8 = 0;
    uint32_t head_idx = kNullIdx;
    uint32_t tail_idx = kNullIdx;
    uint32_t order_count = 0;
    uint32_t left = kNullIdx;
    uint32_t right = kNullIdx;
    uint32_t parent = kNullIdx;
    int32_t height = 1;
};

struct OrderSubmitRequest {
    uint64_t order_id = 0;
    uint64_t account_id_low = 0;
    uint64_t account_id_high = 0;
    int64_t price_1e8 = 0;
    int64_t qty_1e8 = 0;
    uint64_t timestamp_ns = 0;
    Side side = Side::BUY;
    OrderType order_type = OrderType::LIMIT;
    TimeInForce time_in_force = TimeInForce::GTC;
    StpMode stp_mode = StpMode::CANCEL_NEWEST;
};

struct OrderSubmitResult {
    int64_t filled_qty_1e8 = 0;
    int64_t remaining_qty_1e8 = 0;
    uint32_t fill_count = 0;
    OrderStatus status = OrderStatus::ACCEPTED;
    int32_t error_code = SC_OK;
};

// Pre-allocated single-threaded Central Limit Order Book for one market.
// Uses a contiguous 64-byte OrderNode slab pool, intrusive FIFO price queues,
// pool-backed AVL price-level index, and open-addressed flat hash tables.
class OrderBook {
  public:
    explicit OrderBook(uint16_t market_id, uint32_t max_orders = kDefaultMaxOrders);

    OrderBook(const OrderBook&) = delete;
    OrderBook& operator=(const OrderBook&) = delete;
    OrderBook(OrderBook&&) = default;
    OrderBook& operator=(OrderBook&&) = default;

    [[nodiscard]] uint16_t market_id() const noexcept { return market_id_; }
    [[nodiscard]] uint32_t max_orders() const noexcept { return max_orders_; }
    [[nodiscard]] uint32_t active_order_count() const noexcept { return active_orders_; }
    [[nodiscard]] uint32_t bid_level_count() const noexcept { return bid_level_count_; }
    [[nodiscard]] uint32_t ask_level_count() const noexcept { return ask_level_count_; }

    [[nodiscard]] int64_t best_bid_price() const noexcept;
    [[nodiscard]] int64_t best_ask_price() const noexcept;
    [[nodiscard]] int64_t best_bid_volume() const noexcept;
    [[nodiscard]] int64_t best_ask_volume() const noexcept;

    [[nodiscard]] bool contains_order(uint64_t order_id) const noexcept;
    [[nodiscard]] const OrderNode* find_order(uint64_t order_id) const noexcept;

    // Submit an order into the book. Zero heap allocations in the hot path.
    // Fills up to max_fills are written into out_fills_buf (if non-null).
    OrderSubmitResult submit(const OrderSubmitRequest& req,
                             uint64_t& next_trade_id,
                             const CircuitBreaker* breaker,
                             Fill* out_fills_buf,
                             uint32_t max_fills,
                             uint32_t* out_total_fills = nullptr);

    // O(1) cancel by order_id. Returns SC_OK or SC_ERR_ORDER_NOT_FOUND.
    int32_t cancel(uint64_t order_id, int64_t* out_canceled_qty = nullptr);

    // O(K) cancel-all for an account where K is the account's resting orders.
    uint32_t cancel_all_account(uint64_t account_id_low, uint64_t account_id_high);

    // Clear all resting orders without deallocating pre-allocated buffers.
    void clear() noexcept;

    // Direct resting insertion used during snapshot load.
    int32_t restore_resting_order(const OrderNode& node);

    // Deterministic L2 depth snapshots (bids descending, asks ascending).
    void get_l2_depth(uint32_t max_levels,
                      std::vector<L2Level>& out_bids,
                      std::vector<L2Level>& out_asks) const;

    // Iterate all resting orders in canonical ADR-005 order:
    // bids descending price, then asks ascending price, FIFO head->tail per level.
    template <typename Visitor>
    void for_each_resting_order_canonical(Visitor&& visitor) const {
        for (uint32_t lvl = tree_max(bid_root_); lvl != kNullIdx; lvl = tree_prev(lvl)) {
            uint32_t idx = levels_[lvl].head_idx;
            while (idx != kNullIdx) {
                visitor(pool_[idx]);
                idx = pool_[idx].next_idx;
            }
        }
        for (uint32_t lvl = tree_min(ask_root_); lvl != kNullIdx; lvl = tree_next(lvl)) {
            uint32_t idx = levels_[lvl].head_idx;
            while (idx != kNullIdx) {
                visitor(pool_[idx]);
                idx = pool_[idx].next_idx;
            }
        }
    }

  private:
    struct AccountLink {
        uint32_t prev = kNullIdx;
        uint32_t next = kNullIdx;
    };

    struct OrderMapSlot {
        uint64_t key = 0;
        uint32_t value = kNullIdx;
        uint32_t _pad = 0;
    };

    struct PriceMapSlot {
        int64_t key = 0;
        uint32_t value = kNullIdx;
        uint8_t occupied = 0;
        uint8_t _pad[3] = {0, 0, 0};
    };

    struct AccountMapSlot {
        uint64_t low = 0;
        uint64_t high = 0;
        uint32_t head_idx = kNullIdx;
        uint8_t occupied = 0;
        uint8_t _pad[3] = {0, 0, 0};
    };

    class FlatOrderMap {
      public:
        void init(uint32_t min_capacity);
        void clear() noexcept;
        [[nodiscard]] uint32_t find(uint64_t order_id) const noexcept;
        void insert(uint64_t order_id, uint32_t pool_idx);
        bool erase(uint64_t order_id) noexcept;

      private:
        std::vector<OrderMapSlot> slots_;
        uint32_t mask_ = 0;
        uint32_t count_ = 0;
    };

    class FlatPriceMap {
      public:
        void init(uint32_t min_capacity);
        void clear() noexcept;
        [[nodiscard]] uint32_t find(int64_t price_1e8) const noexcept;
        void insert(int64_t price_1e8, uint32_t level_idx);
        bool erase(int64_t price_1e8) noexcept;

      private:
        void rehash(uint32_t new_capacity);
        std::vector<PriceMapSlot> slots_;
        uint32_t mask_ = 0;
        uint32_t count_ = 0;
    };

    class FlatAccountMap {
      public:
        void init(uint32_t min_capacity);
        void clear() noexcept;
        [[nodiscard]] uint32_t find(uint64_t low, uint64_t high) const noexcept;
        void upsert(uint64_t low, uint64_t high, uint32_t head_idx);
        bool erase(uint64_t low, uint64_t high) noexcept;

      private:
        void rehash(uint32_t new_capacity);
        std::vector<AccountMapSlot> slots_;
        uint32_t mask_ = 0;
        uint32_t count_ = 0;
    };

    // Pool management
    uint32_t alloc_node() noexcept;
    void free_node(uint32_t pool_idx) noexcept;
    uint32_t alloc_level();
    void free_level(uint32_t level_idx) noexcept;

    // Intrusive account index
    void link_account_order(uint32_t pool_idx, uint64_t low, uint64_t high);
    void unlink_account_order(uint32_t pool_idx, uint64_t low, uint64_t high) noexcept;

    // Price level AVL tree helpers
    [[nodiscard]] uint32_t tree_min(uint32_t root) const noexcept;
    [[nodiscard]] uint32_t tree_max(uint32_t root) const noexcept;
    [[nodiscard]] uint32_t tree_next(uint32_t node) const noexcept;
    [[nodiscard]] uint32_t tree_prev(uint32_t node) const noexcept;
    [[nodiscard]] int32_t node_height(uint32_t idx) const noexcept;
    void update_height(uint32_t idx) noexcept;
    [[nodiscard]] int32_t balance_factor(uint32_t idx) const noexcept;
    uint32_t rotate_left(uint32_t x, uint32_t& root) noexcept;
    uint32_t rotate_right(uint32_t y, uint32_t& root) noexcept;
    void rebalance_up(uint32_t idx, uint32_t& root) noexcept;
    void tree_insert(uint32_t level_idx, uint32_t& root) noexcept;
    void tree_erase(uint32_t level_idx, uint32_t& root) noexcept;

    uint32_t get_or_create_level(Side side, int64_t price_1e8);
    void remove_level_if_empty(Side side, uint32_t level_idx) noexcept;
    void append_node_to_level(uint32_t level_idx, uint32_t pool_idx) noexcept;
    void remove_node_from_book(uint32_t pool_idx) noexcept;

    // Pre-match checks (no mutation)
    [[nodiscard]] bool would_trip_circuit_breaker(const OrderSubmitRequest& req,
                                                  const CircuitBreaker& breaker) const noexcept;
    [[nodiscard]] bool has_fok_liquidity(const OrderSubmitRequest& req) const noexcept;

    uint16_t market_id_ = kMarketEthUsdc;
    uint32_t max_orders_ = kDefaultMaxOrders;
    uint32_t active_orders_ = 0;

    std::vector<OrderNode> pool_;
    std::vector<uint32_t> node_level_idx_;
    std::vector<AccountLink> account_links_;
    uint32_t free_node_head_ = kNullIdx;

    std::vector<PriceLevel> levels_;
    uint32_t free_level_head_ = kNullIdx;

    uint32_t bid_root_ = kNullIdx;
    uint32_t ask_root_ = kNullIdx;
    uint32_t best_bid_idx_ = kNullIdx;
    uint32_t best_ask_idx_ = kNullIdx;
    uint32_t bid_level_count_ = 0;
    uint32_t ask_level_count_ = 0;

    FlatOrderMap order_map_;
    FlatPriceMap bid_price_map_;
    FlatPriceMap ask_price_map_;
    FlatAccountMap account_head_map_;
};

}  // namespace settlecore
