#ifndef SETTLECORE_C_API_H
#define SETTLECORE_C_API_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct sc_engine_handle sc_engine_t;

/* Command opcodes */
#define SC_OP_SUBMIT_ORDER 1
#define SC_OP_CANCEL_ORDER 2
#define SC_OP_CANCEL_ALL_ACCOUNT 3
#define SC_OP_UPDATE_MARK_PRICE 4
#define SC_OP_SNAPSHOT_BARRIER 5

/* Side */
#define SC_SIDE_BUY 0
#define SC_SIDE_SELL 1

/* Order type */
#define SC_ORDER_TYPE_LIMIT 0
#define SC_ORDER_TYPE_MARKET 1

/* Time in force */
#define SC_TIF_GTC 0
#define SC_TIF_IOC 1
#define SC_TIF_FOK 2

/* Self-Trade Prevention */
#define SC_STP_CANCEL_NEWEST 0
#define SC_STP_CANCEL_OLDEST 1
#define SC_STP_CANCEL_BOTH 2

/* Execution report status */
#define SC_STATUS_ACCEPTED 0
#define SC_STATUS_PARTIALLY_FILLED 1
#define SC_STATUS_FILLED 2
#define SC_STATUS_CANCELED 3
#define SC_STATUS_STP_CANCELED 4
#define SC_STATUS_CB_REJECTED 5
#define SC_STATUS_REJECTED 6

/* WAL durability modes */
#define SC_WAL_MODE_MEMORY 0
#define SC_WAL_MODE_BUFFERED 1
#define SC_WAL_MODE_FSYNC 2
#define SC_WAL_MODE_GROUP_COMMIT 3
#define SC_WAL_GROUP_COMMIT 3

/* Error codes */
#define SC_C_OK 0
#define SC_C_ERR_INVALID_ARG (-1)
#define SC_C_ERR_INVALID_NUMERIC_BOUND (-2)
#define SC_C_ERR_UNKNOWN_MARKET (-3)
#define SC_C_ERR_ORDER_POOL_EXHAUSTED (-4)
#define SC_C_ERR_DUPLICATE_ORDER_ID (-5)
#define SC_C_ERR_ORDER_NOT_FOUND (-6)
#define SC_C_ERR_CIRCUIT_BREAKER_TRIPPED (-7)
#define SC_C_ERR_FOK_UNFILLABLE (-8)
#define SC_C_ERR_IO (-9)
#define SC_C_ERR_CORRUPT_WAL (-10)
#define SC_C_ERR_CORRUPT_SNAPSHOT (-11)
#define SC_C_ERR_OUT_OF_MEMORY (-12)

typedef struct {
    uint32_t max_orders_per_book; /* 0 = default 1,048,576 */
    int64_t cb_collar_bps;        /* 0 = default 1000 (+/- 10%) */
    uint8_t wal_mode;             /* SC_WAL_MODE_* */
    uint8_t _pad[7];
    const char* wal_path; /* NULL for in-memory only */
} sc_engine_config_t;

typedef struct {
    uint64_t wal_seq;
    uint64_t order_id;
    uint64_t account_id_low;
    uint64_t account_id_high;
    int64_t price_1e8;
    int64_t qty_1e8;
    uint64_t timestamp_ns;
    uint16_t market_id;
    uint8_t opcode;        /* 1=SUBMIT, 2=CANCEL, 3=CANCEL_ALL_ACCOUNT, 4=UPDATE_MARK_PRICE */
    uint8_t side;          /* 0=BUY, 1=SELL */
    uint8_t order_type;    /* 0=LIMIT, 1=MARKET */
    uint8_t time_in_force; /* 0=GTC, 1=IOC, 2=FOK */
    uint8_t stp_mode;      /* 0=CANCEL_NEWEST, 1=CANCEL_OLDEST, 2=CANCEL_BOTH */
    uint8_t _pad;
} sc_order_cmd_t;

typedef struct {
    uint64_t trade_id;
    uint64_t maker_order_id;
    uint64_t taker_order_id;
    uint64_t maker_account_low;
    uint64_t maker_account_high;
    int64_t exec_price_1e8;
    int64_t fill_qty_1e8;
    int64_t quote_notional_1e8;
} sc_fill_t;

typedef struct {
    uint64_t wal_seq;
    uint64_t order_id;
    int64_t filled_qty_1e8;
    int64_t remaining_qty_1e8;
    uint32_t fill_count;
    uint8_t status; /* 0=ACCEPTED..5=CB_REJECTED, 6=REJECTED */
    uint8_t _pad[3];
    int32_t error_code;
} sc_exec_report_t;

typedef struct {
    int64_t price_1e8;
    int64_t volume_1e8;
    uint32_t order_count;
    uint32_t _pad;
} sc_l2_level_t;

sc_engine_t* sc_engine_create(const sc_engine_config_t* config);
void sc_engine_destroy(sc_engine_t* engine);

int32_t sc_engine_submit(sc_engine_t* engine,
                         const sc_order_cmd_t* cmd,
                         sc_exec_report_t* out_report,
                         sc_fill_t* out_fills_buf,
                         uint32_t max_fills);

int32_t sc_engine_submit_batch(sc_engine_t* engine,
                               const sc_order_cmd_t* cmds,
                               uint32_t cmd_count,
                               sc_exec_report_t* out_reports,
                               sc_fill_t* out_fills_buf,
                               uint32_t max_total_fills,
                               uint32_t* out_total_fills);

int32_t sc_engine_set_mark_price(sc_engine_t* engine,
                                 uint16_t market_id,
                                 int64_t mark_price_1e8,
                                 int64_t collar_bps);

int32_t sc_engine_get_l2_depth(const sc_engine_t* engine,
                               uint16_t market_id,
                               uint32_t max_levels,
                               sc_l2_level_t* out_bids,
                               uint32_t* out_bid_count,
                               sc_l2_level_t* out_asks,
                               uint32_t* out_ask_count);

int32_t sc_engine_replay_wal(sc_engine_t* engine,
                             const char* wal_path,
                             uint64_t min_wal_seq_exclusive,
                             uint64_t* out_last_seq,
                             uint64_t* out_replayed_count);

int32_t sc_engine_write_snapshot(sc_engine_t* engine, const char* snapshot_path);

int32_t sc_engine_load_snapshot(sc_engine_t* engine,
                                const char* snapshot_path,
                                uint64_t* out_snapshot_seq);

int32_t sc_engine_state_sha256(const sc_engine_t* engine, uint8_t out_hash[32]);

#ifdef __cplusplus
}
#endif

#endif /* SETTLECORE_C_API_H */
