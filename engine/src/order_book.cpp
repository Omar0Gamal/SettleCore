#include "settlecore/order_book.hpp"

#include <algorithm>
#include <cstring>
#include <utility>

namespace settlecore {

namespace {

inline uint64_t hash_u64(uint64_t x) noexcept {
    x += 0x9e3779b97f4a7c15ULL;
    x = (x ^ (x >> 30U)) * 0xbf58476d1ce4e5b9ULL;
    x = (x ^ (x >> 27U)) * 0x94d049bb133111ebULL;
    return x ^ (x >> 31U);
}

inline uint64_t hash_u128(uint64_t low, uint64_t high) noexcept {
    return hash_u64(low ^ (hash_u64(high) + 0x9e3779b97f4a7c15ULL));
}

inline uint32_t next_pow2(uint32_t v) noexcept {
    if (v <= 16U) {
        return 16U;
    }
    --v;
    v |= v >> 1U;
    v |= v >> 2U;
    v |= v >> 4U;
    v |= v >> 8U;
    v |= v >> 16U;
    return v + 1U;
}

}  // namespace

// -----------------------------------------------------------------------------
// FlatOrderMap (open-addressed, backward-shift deletion, zero tombstones)
// -----------------------------------------------------------------------------

void OrderBook::FlatOrderMap::init(uint32_t min_capacity) {
    const uint32_t cap = next_pow2(std::max(min_capacity, 1024U));
    slots_.assign(cap, OrderMapSlot{});
    mask_ = cap - 1U;
    count_ = 0U;
}

void OrderBook::FlatOrderMap::clear() noexcept {
    std::fill(slots_.begin(), slots_.end(), OrderMapSlot{});
    count_ = 0U;
}

uint32_t OrderBook::FlatOrderMap::find(uint64_t order_id) const noexcept {
    if (order_id == 0U || slots_.empty()) {
        return kNullIdx;
    }
    uint32_t idx = static_cast<uint32_t>(hash_u64(order_id)) & mask_;
    for (;;) {
        const OrderMapSlot& slot = slots_[idx];
        if (slot.key == 0U) {
            return kNullIdx;
        }
        if (slot.key == order_id) {
            return slot.value;
        }
        idx = (idx + 1U) & mask_;
    }
}

void OrderBook::FlatOrderMap::insert(uint64_t order_id, uint32_t pool_idx) {
    uint32_t idx = static_cast<uint32_t>(hash_u64(order_id)) & mask_;
    for (;;) {
        OrderMapSlot& slot = slots_[idx];
        if (slot.key == 0U) {
            slot.key = order_id;
            slot.value = pool_idx;
            ++count_;
            return;
        }
        if (slot.key == order_id) {
            slot.value = pool_idx;
            return;
        }
        idx = (idx + 1U) & mask_;
    }
}

bool OrderBook::FlatOrderMap::erase(uint64_t order_id) noexcept {
    if (order_id == 0U || slots_.empty()) {
        return false;
    }
    uint32_t idx = static_cast<uint32_t>(hash_u64(order_id)) & mask_;
    for (;;) {
        if (slots_[idx].key == 0U) {
            return false;
        }
        if (slots_[idx].key == order_id) {
            break;
        }
        idx = (idx + 1U) & mask_;
    }

    uint32_t j = idx;
    for (;;) {
        slots_[j].key = 0U;
        slots_[j].value = kNullIdx;
        uint32_t k = j;
        for (;;) {
            k = (k + 1U) & mask_;
            if (slots_[k].key == 0U) {
                --count_;
                return true;
            }
            const uint32_t home = static_cast<uint32_t>(hash_u64(slots_[k].key)) & mask_;
            if (((j - home) & mask_) < ((k - home) & mask_)) {
                slots_[j] = slots_[k];
                j = k;
                break;
            }
        }
    }
}

// -----------------------------------------------------------------------------
// FlatPriceMap
// -----------------------------------------------------------------------------

void OrderBook::FlatPriceMap::init(uint32_t min_capacity) {
    const uint32_t cap = next_pow2(std::max(min_capacity, 256U));
    slots_.assign(cap, PriceMapSlot{});
    mask_ = cap - 1U;
    count_ = 0U;
}

void OrderBook::FlatPriceMap::clear() noexcept {
    std::fill(slots_.begin(), slots_.end(), PriceMapSlot{});
    count_ = 0U;
}

void OrderBook::FlatPriceMap::rehash(uint32_t new_capacity) {
    std::vector<PriceMapSlot> old = std::move(slots_);
    slots_.assign(new_capacity, PriceMapSlot{});
    mask_ = new_capacity - 1U;
    count_ = 0U;
    for (const PriceMapSlot& s : old) {
        if (s.occupied != 0U) {
            insert(s.key, s.value);
        }
    }
}

uint32_t OrderBook::FlatPriceMap::find(int64_t price_1e8) const noexcept {
    if (slots_.empty()) {
        return kNullIdx;
    }
    uint32_t idx = static_cast<uint32_t>(hash_u64(static_cast<uint64_t>(price_1e8))) & mask_;
    for (;;) {
        const PriceMapSlot& slot = slots_[idx];
        if (slot.occupied == 0U) {
            return kNullIdx;
        }
        if (slot.key == price_1e8) {
            return slot.value;
        }
        idx = (idx + 1U) & mask_;
    }
}

void OrderBook::FlatPriceMap::insert(int64_t price_1e8, uint32_t level_idx) {
    if ((count_ + 1U) * 10U >= static_cast<uint32_t>(slots_.size()) * 7U) {
        rehash(static_cast<uint32_t>(slots_.size()) * 2U);
    }
    uint32_t idx = static_cast<uint32_t>(hash_u64(static_cast<uint64_t>(price_1e8))) & mask_;
    for (;;) {
        PriceMapSlot& slot = slots_[idx];
        if (slot.occupied == 0U) {
            slot.key = price_1e8;
            slot.value = level_idx;
            slot.occupied = 1U;
            ++count_;
            return;
        }
        if (slot.key == price_1e8) {
            slot.value = level_idx;
            return;
        }
        idx = (idx + 1U) & mask_;
    }
}

bool OrderBook::FlatPriceMap::erase(int64_t price_1e8) noexcept {
    if (slots_.empty()) {
        return false;
    }
    uint32_t idx = static_cast<uint32_t>(hash_u64(static_cast<uint64_t>(price_1e8))) & mask_;
    for (;;) {
        if (slots_[idx].occupied == 0U) {
            return false;
        }
        if (slots_[idx].key == price_1e8) {
            break;
        }
        idx = (idx + 1U) & mask_;
    }

    uint32_t j = idx;
    for (;;) {
        slots_[j].occupied = 0U;
        slots_[j].value = kNullIdx;
        uint32_t k = j;
        for (;;) {
            k = (k + 1U) & mask_;
            if (slots_[k].occupied == 0U) {
                --count_;
                return true;
            }
            const uint32_t home =
                static_cast<uint32_t>(hash_u64(static_cast<uint64_t>(slots_[k].key))) & mask_;
            if (((j - home) & mask_) < ((k - home) & mask_)) {
                slots_[j] = slots_[k];
                j = k;
                break;
            }
        }
    }
}

// -----------------------------------------------------------------------------
// FlatAccountMap
// -----------------------------------------------------------------------------

void OrderBook::FlatAccountMap::init(uint32_t min_capacity) {
    const uint32_t cap = next_pow2(std::max(min_capacity, 256U));
    slots_.assign(cap, AccountMapSlot{});
    mask_ = cap - 1U;
    count_ = 0U;
}

void OrderBook::FlatAccountMap::clear() noexcept {
    std::fill(slots_.begin(), slots_.end(), AccountMapSlot{});
    count_ = 0U;
}

void OrderBook::FlatAccountMap::rehash(uint32_t new_capacity) {
    std::vector<AccountMapSlot> old = std::move(slots_);
    slots_.assign(new_capacity, AccountMapSlot{});
    mask_ = new_capacity - 1U;
    count_ = 0U;
    for (const AccountMapSlot& s : old) {
        if (s.occupied != 0U) {
            upsert(s.low, s.high, s.head_idx);
        }
    }
}

uint32_t OrderBook::FlatAccountMap::find(uint64_t low, uint64_t high) const noexcept {
    if (slots_.empty()) {
        return kNullIdx;
    }
    uint32_t idx = static_cast<uint32_t>(hash_u128(low, high)) & mask_;
    for (;;) {
        const AccountMapSlot& slot = slots_[idx];
        if (slot.occupied == 0U) {
            return kNullIdx;
        }
        if (slot.low == low && slot.high == high) {
            return slot.head_idx;
        }
        idx = (idx + 1U) & mask_;
    }
}

void OrderBook::FlatAccountMap::upsert(uint64_t low, uint64_t high, uint32_t head_idx) {
    if ((count_ + 1U) * 10U >= static_cast<uint32_t>(slots_.size()) * 7U) {
        rehash(static_cast<uint32_t>(slots_.size()) * 2U);
    }
    uint32_t idx = static_cast<uint32_t>(hash_u128(low, high)) & mask_;
    for (;;) {
        AccountMapSlot& slot = slots_[idx];
        if (slot.occupied == 0U) {
            slot.low = low;
            slot.high = high;
            slot.head_idx = head_idx;
            slot.occupied = 1U;
            ++count_;
            return;
        }
        if (slot.low == low && slot.high == high) {
            slot.head_idx = head_idx;
            return;
        }
        idx = (idx + 1U) & mask_;
    }
}

bool OrderBook::FlatAccountMap::erase(uint64_t low, uint64_t high) noexcept {
    if (slots_.empty()) {
        return false;
    }
    uint32_t idx = static_cast<uint32_t>(hash_u128(low, high)) & mask_;
    for (;;) {
        if (slots_[idx].occupied == 0U) {
            return false;
        }
        if (slots_[idx].low == low && slots_[idx].high == high) {
            break;
        }
        idx = (idx + 1U) & mask_;
    }

    uint32_t j = idx;
    for (;;) {
        slots_[j].occupied = 0U;
        slots_[j].head_idx = kNullIdx;
        uint32_t k = j;
        for (;;) {
            k = (k + 1U) & mask_;
            if (slots_[k].occupied == 0U) {
                --count_;
                return true;
            }
            const uint32_t home =
                static_cast<uint32_t>(hash_u128(slots_[k].low, slots_[k].high)) & mask_;
            if (((j - home) & mask_) < ((k - home) & mask_)) {
                slots_[j] = slots_[k];
                j = k;
                break;
            }
        }
    }
}

// -----------------------------------------------------------------------------
// OrderBook Construction & Pool Management
// -----------------------------------------------------------------------------

OrderBook::OrderBook(uint16_t market_id, uint32_t max_orders)
    : market_id_(market_id), max_orders_(std::max(max_orders, 16U)) {
    pool_.resize(max_orders_);
    node_level_idx_.assign(max_orders_, kNullIdx);
    account_links_.resize(max_orders_);

    for (uint32_t i = 0U; i + 1U < max_orders_; ++i) {
        pool_[i].next_idx = i + 1U;
    }
    pool_[max_orders_ - 1U].next_idx = kNullIdx;
    free_node_head_ = 0U;

    const uint32_t initial_levels = std::min(max_orders_, 262'144U);
    levels_.resize(initial_levels);
    for (uint32_t i = 0U; i + 1U < initial_levels; ++i) {
        levels_[i].left = i + 1U;
    }
    levels_[initial_levels - 1U].left = kNullIdx;
    free_level_head_ = 0U;

    order_map_.init(max_orders_ * 2U);
    bid_price_map_.init(std::min(max_orders_ * 2U, 262'144U));
    ask_price_map_.init(std::min(max_orders_ * 2U, 262'144U));
    account_head_map_.init(std::min(max_orders_, 65'536U));
}

void OrderBook::clear() noexcept {
    active_orders_ = 0U;
    for (uint32_t i = 0U; i + 1U < max_orders_; ++i) {
        pool_[i].next_idx = i + 1U;
    }
    pool_[max_orders_ - 1U].next_idx = kNullIdx;
    free_node_head_ = 0U;
    std::fill(node_level_idx_.begin(), node_level_idx_.end(), kNullIdx);

    const uint32_t level_cap = static_cast<uint32_t>(levels_.size());
    for (uint32_t i = 0U; i + 1U < level_cap; ++i) {
        levels_[i].left = i + 1U;
    }
    if (level_cap > 0U) {
        levels_[level_cap - 1U].left = kNullIdx;
        free_level_head_ = 0U;
    } else {
        free_level_head_ = kNullIdx;
    }

    bid_root_ = kNullIdx;
    ask_root_ = kNullIdx;
    best_bid_idx_ = kNullIdx;
    best_ask_idx_ = kNullIdx;
    bid_level_count_ = 0U;
    ask_level_count_ = 0U;

    order_map_.clear();
    bid_price_map_.clear();
    ask_price_map_.clear();
    account_head_map_.clear();
}

uint32_t OrderBook::alloc_node() noexcept {
    if (free_node_head_ == kNullIdx) {
        return kNullIdx;
    }
    const uint32_t idx = free_node_head_;
    free_node_head_ = pool_[idx].next_idx;
    ++active_orders_;
    return idx;
}

void OrderBook::free_node(uint32_t pool_idx) noexcept {
    pool_[pool_idx].next_idx = free_node_head_;
    free_node_head_ = pool_idx;
    node_level_idx_[pool_idx] = kNullIdx;
    --active_orders_;
}

uint32_t OrderBook::alloc_level() {
    if (free_level_head_ == kNullIdx) {
        const uint32_t old_size = static_cast<uint32_t>(levels_.size());
        const uint32_t new_size = std::max(old_size * 2U, 256U);
        levels_.resize(new_size);
        for (uint32_t i = old_size; i + 1U < new_size; ++i) {
            levels_[i].left = i + 1U;
        }
        levels_[new_size - 1U].left = kNullIdx;
        free_level_head_ = old_size;
    }
    const uint32_t idx = free_level_head_;
    free_level_head_ = levels_[idx].left;
    levels_[idx] = PriceLevel{};
    return idx;
}

void OrderBook::free_level(uint32_t level_idx) noexcept {
    levels_[level_idx].left = free_level_head_;
    free_level_head_ = level_idx;
}

// -----------------------------------------------------------------------------
// Account Order Intrusive Index (for O(K) cancel_all_account)
// -----------------------------------------------------------------------------

void OrderBook::link_account_order(uint32_t pool_idx, uint64_t low, uint64_t high) {
    const uint32_t old_head = account_head_map_.find(low, high);
    account_links_[pool_idx].prev = kNullIdx;
    account_links_[pool_idx].next = old_head;
    if (old_head != kNullIdx) {
        account_links_[old_head].prev = pool_idx;
    }
    account_head_map_.upsert(low, high, pool_idx);
}

void OrderBook::unlink_account_order(uint32_t pool_idx, uint64_t low, uint64_t high) noexcept {
    const uint32_t prev = account_links_[pool_idx].prev;
    const uint32_t next = account_links_[pool_idx].next;
    if (prev != kNullIdx) {
        account_links_[prev].next = next;
    }
    if (next != kNullIdx) {
        account_links_[next].prev = prev;
    }
    if (prev == kNullIdx) {
        if (next == kNullIdx) {
            account_head_map_.erase(low, high);
        } else {
            account_head_map_.upsert(low, high, next);
        }
    }
    account_links_[pool_idx].prev = kNullIdx;
    account_links_[pool_idx].next = kNullIdx;
}

// -----------------------------------------------------------------------------
// Intrusive Pool-Backed AVL Tree for Price Levels
// -----------------------------------------------------------------------------

int32_t OrderBook::node_height(uint32_t idx) const noexcept {
    return (idx == kNullIdx) ? 0 : levels_[idx].height;
}

void OrderBook::update_height(uint32_t idx) noexcept {
    const int32_t lh = node_height(levels_[idx].left);
    const int32_t rh = node_height(levels_[idx].right);
    levels_[idx].height = 1 + std::max(lh, rh);
}

int32_t OrderBook::balance_factor(uint32_t idx) const noexcept {
    return (idx == kNullIdx) ? 0 : (node_height(levels_[idx].left) - node_height(levels_[idx].right));
}

uint32_t OrderBook::tree_min(uint32_t root) const noexcept {
    if (root == kNullIdx) {
        return kNullIdx;
    }
    while (levels_[root].left != kNullIdx) {
        root = levels_[root].left;
    }
    return root;
}

uint32_t OrderBook::tree_max(uint32_t root) const noexcept {
    if (root == kNullIdx) {
        return kNullIdx;
    }
    while (levels_[root].right != kNullIdx) {
        root = levels_[root].right;
    }
    return root;
}

uint32_t OrderBook::tree_next(uint32_t node) const noexcept {
    if (node == kNullIdx) {
        return kNullIdx;
    }
    if (levels_[node].right != kNullIdx) {
        return tree_min(levels_[node].right);
    }
    uint32_t p = levels_[node].parent;
    while (p != kNullIdx && node == levels_[p].right) {
        node = p;
        p = levels_[p].parent;
    }
    return p;
}

uint32_t OrderBook::tree_prev(uint32_t node) const noexcept {
    if (node == kNullIdx) {
        return kNullIdx;
    }
    if (levels_[node].left != kNullIdx) {
        return tree_max(levels_[node].left);
    }
    uint32_t p = levels_[node].parent;
    while (p != kNullIdx && node == levels_[p].left) {
        node = p;
        p = levels_[p].parent;
    }
    return p;
}

uint32_t OrderBook::rotate_left(uint32_t x, uint32_t& root) noexcept {
    const uint32_t y = levels_[x].right;
    const uint32_t b = levels_[y].left;
    const uint32_t xp = levels_[x].parent;

    levels_[y].left = x;
    levels_[x].parent = y;
    levels_[x].right = b;
    if (b != kNullIdx) {
        levels_[b].parent = x;
    }

    levels_[y].parent = xp;
    if (xp == kNullIdx) {
        root = y;
    } else if (levels_[xp].left == x) {
        levels_[xp].left = y;
    } else {
        levels_[xp].right = y;
    }

    update_height(x);
    update_height(y);
    return y;
}

uint32_t OrderBook::rotate_right(uint32_t y, uint32_t& root) noexcept {
    const uint32_t x = levels_[y].left;
    const uint32_t b = levels_[x].right;
    const uint32_t yp = levels_[y].parent;

    levels_[x].right = y;
    levels_[y].parent = x;
    levels_[y].left = b;
    if (b != kNullIdx) {
        levels_[b].parent = y;
    }

    levels_[x].parent = yp;
    if (yp == kNullIdx) {
        root = x;
    } else if (levels_[yp].left == y) {
        levels_[yp].left = x;
    } else {
        levels_[yp].right = x;
    }

    update_height(y);
    update_height(x);
    return x;
}

void OrderBook::rebalance_up(uint32_t idx, uint32_t& root) noexcept {
    while (idx != kNullIdx) {
        update_height(idx);
        const int32_t bf = balance_factor(idx);
        if (bf > 1) {
            if (balance_factor(levels_[idx].left) < 0) {
                rotate_left(levels_[idx].left, root);
            }
            idx = rotate_right(idx, root);
        } else if (bf < -1) {
            if (balance_factor(levels_[idx].right) > 0) {
                rotate_right(levels_[idx].right, root);
            }
            idx = rotate_left(idx, root);
        }
        idx = levels_[idx].parent;
    }
}

void OrderBook::tree_insert(uint32_t level_idx, uint32_t& root) noexcept {
    levels_[level_idx].left = kNullIdx;
    levels_[level_idx].right = kNullIdx;
    levels_[level_idx].parent = kNullIdx;
    levels_[level_idx].height = 1;

    if (root == kNullIdx) {
        root = level_idx;
        return;
    }

    const int64_t price = levels_[level_idx].price_1e8;
    uint32_t cur = root;
    uint32_t parent = kNullIdx;
    while (cur != kNullIdx) {
        parent = cur;
        if (price < levels_[cur].price_1e8) {
            cur = levels_[cur].left;
        } else {
            cur = levels_[cur].right;
        }
    }

    levels_[level_idx].parent = parent;
    if (price < levels_[parent].price_1e8) {
        levels_[parent].left = level_idx;
    } else {
        levels_[parent].right = level_idx;
    }

    rebalance_up(parent, root);
}

void OrderBook::tree_erase(uint32_t z, uint32_t& root) noexcept {
    uint32_t rebalance_start = kNullIdx;

    if (levels_[z].left != kNullIdx && levels_[z].right != kNullIdx) {
        const uint32_t y = tree_min(levels_[z].right);
        const uint32_t zp = levels_[z].parent;
        const uint32_t zl = levels_[z].left;
        const uint32_t zr = levels_[z].right;
        const uint32_t yp = levels_[y].parent;
        const uint32_t yr = levels_[y].right;

        if (zp == kNullIdx) {
            root = y;
        } else if (levels_[zp].left == z) {
            levels_[zp].left = y;
        } else {
            levels_[zp].right = y;
        }
        levels_[y].parent = zp;
        levels_[y].left = zl;
        levels_[zl].parent = y;

        if (y == zr) {
            levels_[y].right = z;
            levels_[z].parent = y;
        } else {
            levels_[y].right = zr;
            levels_[zr].parent = y;
            levels_[yp].left = z;
            levels_[z].parent = yp;
        }

        levels_[z].left = kNullIdx;
        levels_[z].right = yr;
        if (yr != kNullIdx) {
            levels_[yr].parent = z;
        }
        std::swap(levels_[z].height, levels_[y].height);
    }

    // Now z has at most one child.
    const uint32_t child = (levels_[z].left != kNullIdx) ? levels_[z].left : levels_[z].right;
    const uint32_t zp = levels_[z].parent;
    if (child != kNullIdx) {
        levels_[child].parent = zp;
    }
    if (zp == kNullIdx) {
        root = child;
    } else if (levels_[zp].left == z) {
        levels_[zp].left = child;
    } else {
        levels_[zp].right = child;
    }
    rebalance_start = zp;

    levels_[z].left = kNullIdx;
    levels_[z].right = kNullIdx;
    levels_[z].parent = kNullIdx;

    if (rebalance_start != kNullIdx) {
        rebalance_up(rebalance_start, root);
    }
}

// -----------------------------------------------------------------------------
// Level & Queue Operations
// -----------------------------------------------------------------------------

uint32_t OrderBook::get_or_create_level(Side side, int64_t price_1e8) {
    FlatPriceMap& pmap = (side == Side::BUY) ? bid_price_map_ : ask_price_map_;
    uint32_t lvl = pmap.find(price_1e8);
    if (lvl != kNullIdx) {
        return lvl;
    }

    lvl = alloc_level();
    levels_[lvl].price_1e8 = price_1e8;
    pmap.insert(price_1e8, lvl);

    if (side == Side::BUY) {
        tree_insert(lvl, bid_root_);
        ++bid_level_count_;
        if (best_bid_idx_ == kNullIdx || price_1e8 > levels_[best_bid_idx_].price_1e8) {
            best_bid_idx_ = lvl;
        }
    } else {
        tree_insert(lvl, ask_root_);
        ++ask_level_count_;
        if (best_ask_idx_ == kNullIdx || price_1e8 < levels_[best_ask_idx_].price_1e8) {
            best_ask_idx_ = lvl;
        }
    }
    return lvl;
}

void OrderBook::remove_level_if_empty(Side side, uint32_t level_idx) noexcept {
    if (levels_[level_idx].order_count != 0U) {
        return;
    }
    const int64_t price = levels_[level_idx].price_1e8;
    if (side == Side::BUY) {
        bid_price_map_.erase(price);
        if (level_idx == best_bid_idx_) {
            best_bid_idx_ = tree_prev(level_idx);
        }
        tree_erase(level_idx, bid_root_);
        --bid_level_count_;
    } else {
        ask_price_map_.erase(price);
        if (level_idx == best_ask_idx_) {
            best_ask_idx_ = tree_next(level_idx);
        }
        tree_erase(level_idx, ask_root_);
        --ask_level_count_;
    }
    free_level(level_idx);
}

void OrderBook::append_node_to_level(uint32_t level_idx, uint32_t pool_idx) noexcept {
    PriceLevel& lvl = levels_[level_idx];
    OrderNode& node = pool_[pool_idx];
    node.prev_idx = lvl.tail_idx;
    node.next_idx = kNullIdx;
    if (lvl.tail_idx != kNullIdx) {
        pool_[lvl.tail_idx].next_idx = pool_idx;
    } else {
        lvl.head_idx = pool_idx;
    }
    lvl.tail_idx = pool_idx;
    lvl.total_volume_1e8 += node.remaining_1e8;
    ++lvl.order_count;
    node_level_idx_[pool_idx] = level_idx;
}

void OrderBook::remove_node_from_book(uint32_t pool_idx) noexcept {
    const OrderNode& node = pool_[pool_idx];
    const uint32_t level_idx = node_level_idx_[pool_idx];
    PriceLevel& lvl = levels_[level_idx];

    if (node.prev_idx != kNullIdx) {
        pool_[node.prev_idx].next_idx = node.next_idx;
    } else {
        lvl.head_idx = node.next_idx;
    }
    if (node.next_idx != kNullIdx) {
        pool_[node.next_idx].prev_idx = node.prev_idx;
    } else {
        lvl.tail_idx = node.prev_idx;
    }

    lvl.total_volume_1e8 -= node.remaining_1e8;
    --lvl.order_count;

    const Side side = static_cast<Side>(node.side);
    const uint64_t oid = node.order_id;
    const uint64_t acct_low = node.account_id_low;
    const uint64_t acct_high = node.account_id_high;

    unlink_account_order(pool_idx, acct_low, acct_high);
    order_map_.erase(oid);
    free_node(pool_idx);
    remove_level_if_empty(side, level_idx);
}

// -----------------------------------------------------------------------------
// Queries
// -----------------------------------------------------------------------------

int64_t OrderBook::best_bid_price() const noexcept {
    return (best_bid_idx_ == kNullIdx) ? 0 : levels_[best_bid_idx_].price_1e8;
}

int64_t OrderBook::best_ask_price() const noexcept {
    return (best_ask_idx_ == kNullIdx) ? 0 : levels_[best_ask_idx_].price_1e8;
}

int64_t OrderBook::best_bid_volume() const noexcept {
    return (best_bid_idx_ == kNullIdx) ? 0 : levels_[best_bid_idx_].total_volume_1e8;
}

int64_t OrderBook::best_ask_volume() const noexcept {
    return (best_ask_idx_ == kNullIdx) ? 0 : levels_[best_ask_idx_].total_volume_1e8;
}

bool OrderBook::contains_order(uint64_t order_id) const noexcept {
    return order_map_.find(order_id) != kNullIdx;
}

const OrderNode* OrderBook::find_order(uint64_t order_id) const noexcept {
    const uint32_t idx = order_map_.find(order_id);
    return (idx == kNullIdx) ? nullptr : &pool_[idx];
}

// -----------------------------------------------------------------------------
// Pre-Match Non-Mutating Checks (Circuit Breaker & FOK)
// -----------------------------------------------------------------------------

bool OrderBook::would_trip_circuit_breaker(const OrderSubmitRequest& req,
                                           const CircuitBreaker& breaker) const noexcept {
    if (!breaker.is_active()) {
        return false;
    }

    int64_t remaining = req.qty_1e8;
    uint32_t lvl = (req.side == Side::BUY) ? best_ask_idx_ : best_bid_idx_;
    while (lvl != kNullIdx && remaining > 0) {
        const int64_t maker_price = levels_[lvl].price_1e8;
        if (req.order_type == OrderType::LIMIT) {
            if (req.side == Side::BUY && maker_price > req.price_1e8) {
                break;
            }
            if (req.side == Side::SELL && maker_price < req.price_1e8) {
                break;
            }
        }

        // Check orders at this level (skipping self-orders that would be canceled without filling
        // under CANCEL_OLDEST, and stopping if STP CANCEL_NEWEST/CANCEL_BOTH would abort).
        uint32_t idx = levels_[lvl].head_idx;
        while (idx != kNullIdx && remaining > 0) {
            const OrderNode& maker = pool_[idx];
            const bool is_self = (maker.account_id_low == req.account_id_low &&
                                  maker.account_id_high == req.account_id_high);
            if (is_self) {
                if (req.stp_mode == StpMode::CANCEL_NEWEST || req.stp_mode == StpMode::CANCEL_BOTH) {
                    return false;
                }
                idx = maker.next_idx;
                continue;
            }
            if (!breaker.is_price_allowed(maker_price)) {
                return true;
            }
            remaining -= maker.remaining_1e8;
            idx = maker.next_idx;
        }

        lvl = (req.side == Side::BUY) ? tree_next(lvl) : tree_prev(lvl);
    }
    return false;
}

bool OrderBook::has_fok_liquidity(const OrderSubmitRequest& req) const noexcept {
    int64_t remaining = req.qty_1e8;
    uint32_t lvl = (req.side == Side::BUY) ? best_ask_idx_ : best_bid_idx_;
    while (lvl != kNullIdx && remaining > 0) {
        const int64_t maker_price = levels_[lvl].price_1e8;
        if (req.order_type == OrderType::LIMIT) {
            if (req.side == Side::BUY && maker_price > req.price_1e8) {
                break;
            }
            if (req.side == Side::SELL && maker_price < req.price_1e8) {
                break;
            }
        }

        uint32_t idx = levels_[lvl].head_idx;
        while (idx != kNullIdx && remaining > 0) {
            const OrderNode& maker = pool_[idx];
            const bool is_self = (maker.account_id_low == req.account_id_low &&
                                  maker.account_id_high == req.account_id_high);
            if (is_self) {
                if (req.stp_mode == StpMode::CANCEL_NEWEST || req.stp_mode == StpMode::CANCEL_BOTH) {
                    return false;
                }
                idx = maker.next_idx;
                continue;
            }
            remaining -= maker.remaining_1e8;
            idx = maker.next_idx;
        }

        lvl = (req.side == Side::BUY) ? tree_next(lvl) : tree_prev(lvl);
    }
    return remaining <= 0;
}

// -----------------------------------------------------------------------------
// Submit / Match / Cancel / Restore / L2
// -----------------------------------------------------------------------------

OrderSubmitResult OrderBook::submit(const OrderSubmitRequest& req,
                                    uint64_t& next_trade_id,
                                    const CircuitBreaker* breaker,
                                    Fill* out_fills_buf,
                                    uint32_t max_fills,
                                    uint32_t* out_total_fills) {
    OrderSubmitResult res{};
    if (out_total_fills != nullptr) {
        *out_total_fills = 0U;
    }

    if (req.order_id == 0U) {
        res.status = OrderStatus::REJECTED;
        res.error_code = SC_ERR_INVALID_ARG;
        return res;
    }
    if (!is_valid_price_or_qty(req.qty_1e8)) {
        res.status = OrderStatus::REJECTED;
        res.error_code = SC_ERR_INVALID_NUMERIC_BOUND;
        return res;
    }
    if (req.order_type == OrderType::LIMIT) {
        if (!is_valid_price_or_qty(req.price_1e8)) {
            res.status = OrderStatus::REJECTED;
            res.error_code = SC_ERR_INVALID_NUMERIC_BOUND;
            return res;
        }
    } else if (req.price_1e8 < 0 || req.price_1e8 > kMaxPriceOrQty1e8) {
        res.status = OrderStatus::REJECTED;
        res.error_code = SC_ERR_INVALID_NUMERIC_BOUND;
        return res;
    }

    if (order_map_.find(req.order_id) != kNullIdx) {
        res.status = OrderStatus::REJECTED;
        res.error_code = SC_ERR_DUPLICATE_ORDER_ID;
        return res;
    }

    if (breaker != nullptr && would_trip_circuit_breaker(req, *breaker)) {
        res.status = OrderStatus::CB_REJECTED;
        res.error_code = SC_ERR_CIRCUIT_BREAKER_TRIPPED;
        return res;
    }

    if (req.time_in_force == TimeInForce::FOK && !has_fok_liquidity(req)) {
        res.status = OrderStatus::CANCELED;
        res.error_code = SC_OK;
        return res;
    }

    // If pool is already full and an incoming GTC limit order will not cross top of book,
    // fail deterministically before any state change.
    if (req.order_type == OrderType::LIMIT && req.time_in_force == TimeInForce::GTC &&
        free_node_head_ == kNullIdx) {
        const bool crosses =
            (req.side == Side::BUY)
                ? (best_ask_idx_ != kNullIdx && req.price_1e8 >= levels_[best_ask_idx_].price_1e8)
                : (best_bid_idx_ != kNullIdx && req.price_1e8 <= levels_[best_bid_idx_].price_1e8);
        if (!crosses) {
            res.status = OrderStatus::REJECTED;
            res.error_code = SC_ERR_ORDER_POOL_EXHAUSTED;
            return res;
        }
    }

    int64_t remaining = req.qty_1e8;
    int64_t filled = 0;
    uint32_t total_fills = 0U;
    uint32_t written_fills = 0U;
    bool stp_canceled_taker = false;

    while (remaining > 0) {
        const uint32_t lvl_idx = (req.side == Side::BUY) ? best_ask_idx_ : best_bid_idx_;
        if (lvl_idx == kNullIdx) {
            break;
        }
        const int64_t maker_price = levels_[lvl_idx].price_1e8;
        if (req.order_type == OrderType::LIMIT) {
            if (req.side == Side::BUY && maker_price > req.price_1e8) {
                break;
            }
            if (req.side == Side::SELL && maker_price < req.price_1e8) {
                break;
            }
        }

        uint32_t maker_idx = levels_[lvl_idx].head_idx;
        while (maker_idx != kNullIdx && remaining > 0) {
            OrderNode& maker = pool_[maker_idx];
            const uint32_t next_maker_idx = maker.next_idx;

            if (maker.account_id_low == req.account_id_low &&
                maker.account_id_high == req.account_id_high) {
                if (req.stp_mode == StpMode::CANCEL_NEWEST) {
                    stp_canceled_taker = true;
                    break;
                }
                if (req.stp_mode == StpMode::CANCEL_BOTH) {
                    remove_node_from_book(maker_idx);
                    stp_canceled_taker = true;
                    break;
                }
                // StpMode::CANCEL_OLDEST
                remove_node_from_book(maker_idx);
                maker_idx = next_maker_idx;
                continue;
            }

            const int64_t fill_qty = std::min(remaining, maker.remaining_1e8);
            const int64_t notional = compute_quote_notional(maker_price, fill_qty);
            const uint64_t tid = next_trade_id++;

            if (out_fills_buf != nullptr && written_fills < max_fills) {
                out_fills_buf[written_fills] = Fill{
                    tid,
                    maker.order_id,
                    req.order_id,
                    maker.account_id_low,
                    maker.account_id_high,
                    maker_price,
                    fill_qty,
                    notional,
                };
                ++written_fills;
            }
            ++total_fills;

            remaining -= fill_qty;
            filled += fill_qty;
            maker.remaining_1e8 -= fill_qty;
            levels_[lvl_idx].total_volume_1e8 -= fill_qty;

            if (maker.remaining_1e8 == 0) {
                remove_node_from_book(maker_idx);
            }
            maker_idx = next_maker_idx;
        }

        if (stp_canceled_taker) {
            break;
        }
    }

    if (out_total_fills != nullptr) {
        *out_total_fills = total_fills;
    }

    res.filled_qty_1e8 = filled;
    res.fill_count = (out_fills_buf != nullptr) ? written_fills : 0U;

    if (stp_canceled_taker) {
        res.remaining_qty_1e8 = 0;
        res.status = OrderStatus::STP_CANCELED;
        res.error_code = SC_OK;
        return res;
    }

    if (remaining == 0) {
        res.remaining_qty_1e8 = 0;
        res.status = OrderStatus::FILLED;
        res.error_code = SC_OK;
        return res;
    }

    if (req.order_type == OrderType::MARKET || req.time_in_force == TimeInForce::IOC ||
        req.time_in_force == TimeInForce::FOK) {
        res.remaining_qty_1e8 = 0;
        res.status = OrderStatus::CANCELED;
        res.error_code = SC_OK;
        return res;
    }

    // Rest remaining GTC limit order onto the book.
    const uint32_t pool_idx = alloc_node();
    if (pool_idx == kNullIdx) {
        res.remaining_qty_1e8 = 0;
        res.status = OrderStatus::REJECTED;
        res.error_code = SC_ERR_ORDER_POOL_EXHAUSTED;
        return res;
    }

    OrderNode& node = pool_[pool_idx];
    node.order_id = req.order_id;
    node.account_id_low = req.account_id_low;
    node.account_id_high = req.account_id_high;
    node.price_1e8 = req.price_1e8;
    node.remaining_1e8 = remaining;
    node.timestamp_ns = req.timestamp_ns;
    node.next_idx = kNullIdx;
    node.prev_idx = kNullIdx;
    node.market_id = market_id_;
    node.side = static_cast<uint8_t>(req.side);
    node.order_type = static_cast<uint8_t>(req.order_type);
    node.time_in_force = static_cast<uint8_t>(req.time_in_force);
    node.stp_mode = static_cast<uint8_t>(req.stp_mode);
    node.reserved_pad = 0U;

    const uint32_t level_idx = get_or_create_level(req.side, req.price_1e8);
    append_node_to_level(level_idx, pool_idx);
    order_map_.insert(req.order_id, pool_idx);
    link_account_order(pool_idx, req.account_id_low, req.account_id_high);

    res.remaining_qty_1e8 = remaining;
    res.status = (filled > 0) ? OrderStatus::PARTIALLY_FILLED : OrderStatus::ACCEPTED;
    res.error_code = SC_OK;
    return res;
}

int32_t OrderBook::cancel(uint64_t order_id, int64_t* out_canceled_qty) {
    const uint32_t pool_idx = order_map_.find(order_id);
    if (pool_idx == kNullIdx) {
        return SC_ERR_ORDER_NOT_FOUND;
    }
    if (out_canceled_qty != nullptr) {
        *out_canceled_qty = pool_[pool_idx].remaining_1e8;
    }
    remove_node_from_book(pool_idx);
    return SC_OK;
}

uint32_t OrderBook::cancel_all_account(uint64_t account_id_low, uint64_t account_id_high) {
    uint32_t canceled = 0U;
    for (;;) {
        const uint32_t head = account_head_map_.find(account_id_low, account_id_high);
        if (head == kNullIdx) {
            break;
        }
        remove_node_from_book(head);
        ++canceled;
    }
    return canceled;
}

int32_t OrderBook::restore_resting_order(const OrderNode& node) {
    if (node.order_id == 0U) {
        return SC_ERR_INVALID_ARG;
    }
    if (!is_valid_price_or_qty(node.price_1e8) || !is_valid_price_or_qty(node.remaining_1e8)) {
        return SC_ERR_INVALID_NUMERIC_BOUND;
    }
    if (order_map_.find(node.order_id) != kNullIdx) {
        return SC_ERR_DUPLICATE_ORDER_ID;
    }
    const uint32_t pool_idx = alloc_node();
    if (pool_idx == kNullIdx) {
        return SC_ERR_ORDER_POOL_EXHAUSTED;
    }

    pool_[pool_idx] = node;
    pool_[pool_idx].market_id = market_id_;
    pool_[pool_idx].next_idx = kNullIdx;
    pool_[pool_idx].prev_idx = kNullIdx;

    const Side side = static_cast<Side>(node.side);
    const uint32_t level_idx = get_or_create_level(side, node.price_1e8);
    append_node_to_level(level_idx, pool_idx);
    order_map_.insert(node.order_id, pool_idx);
    link_account_order(pool_idx, node.account_id_low, node.account_id_high);
    return SC_OK;
}

void OrderBook::get_l2_depth(uint32_t max_levels,
                             std::vector<L2Level>& out_bids,
                             std::vector<L2Level>& out_asks) const {
    out_bids.clear();
    out_asks.clear();
    if (max_levels == 0U) {
        return;
    }

    uint32_t count = 0U;
    for (uint32_t lvl = best_bid_idx_; lvl != kNullIdx && count < max_levels;
         lvl = tree_prev(lvl)) {
        out_bids.push_back(
            L2Level{levels_[lvl].price_1e8, levels_[lvl].total_volume_1e8, levels_[lvl].order_count});
        ++count;
    }

    count = 0U;
    for (uint32_t lvl = best_ask_idx_; lvl != kNullIdx && count < max_levels;
         lvl = tree_next(lvl)) {
        out_asks.push_back(
            L2Level{levels_[lvl].price_1e8, levels_[lvl].total_volume_1e8, levels_[lvl].order_count});
        ++count;
    }
}

}  // namespace settlecore
