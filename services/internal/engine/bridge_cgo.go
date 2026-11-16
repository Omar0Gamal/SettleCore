//go:build cgo && settlecore_cabi

package engine

/*
#cgo CFLAGS: -I/usr/local/include -I${SRCDIR}/../../../engine/include
#cgo LDFLAGS: -L/usr/local/lib -lsettlecore_engine -lstdc++
#include <stdint.h>
#include "settlecore/c_api.h"
*/
import "C"
import (
	"unsafe"
)

// SubmitViaCABI forwards an OrderCmd directly across the flat C ABI into libsettlecore_engine.a.
func SubmitViaCABI(handle unsafe.Pointer, cmd OrderCmd) ExecReport {
	var cCmd C.sc_order_cmd_t
	cCmd.wal_seq = C.uint64_t(cmd.WalSeq)
	cCmd.order_id = C.uint64_t(cmd.OrderID)
	cCmd.account_id_low = C.uint64_t(cmd.AccountIDLow)
	cCmd.account_id_high = C.uint64_t(cmd.AccountIDHigh)
	cCmd.price_1e8 = C.int64_t(cmd.Price1e8)
	cCmd.qty_1e8 = C.int64_t(cmd.Qty1e8)
	cCmd.timestamp_ns = C.uint64_t(cmd.TimestampNs)
	cCmd.market_id = C.uint16_t(cmd.MarketID)
	cCmd.opcode = C.uint8_t(cmd.Opcode)
	cCmd.side = C.uint8_t(cmd.Side)
	cCmd.order_type = C.uint8_t(cmd.OrderType)
	cCmd.time_in_force = C.uint8_t(cmd.TimeInForce)
	cCmd.stp_mode = C.uint8_t(cmd.STPMode)

	var cRep C.sc_exec_report_t
	var fillsBuf [256]C.sc_fill_t

	C.sc_engine_submit(
		(*C.sc_engine_t)(handle),
		&cCmd,
		&cRep,
		&fillsBuf[0],
		256,
	)

	n := int(cRep.fill_count)
	if n > len(fillsBuf) {
		n = len(fillsBuf)
	}
	fills := make([]FillEvent, n)
	for i := 0; i < n; i++ {
		fills[i] = FillEvent{
			TradeID:          uint64(fillsBuf[i].trade_id),
			MakerOrderID:     uint64(fillsBuf[i].maker_order_id),
			TakerOrderID:     uint64(fillsBuf[i].taker_order_id),
			MakerAccountLow:  uint64(fillsBuf[i].maker_account_low),
			MakerAccountHigh: uint64(fillsBuf[i].maker_account_high),
			ExecPrice1e8:     int64(fillsBuf[i].exec_price_1e8),
			FillQty1e8:       int64(fillsBuf[i].fill_qty_1e8),
			QuoteNotional1e8: int64(fillsBuf[i].quote_notional_1e8),
		}
	}

	return ExecReport{
		WalSeq:          uint64(cRep.wal_seq),
		OrderID:         uint64(cRep.order_id),
		FilledQty1e8:    int64(cRep.filled_qty_1e8),
		RemainingQty1e8: int64(cRep.remaining_qty_1e8),
		Fills:           fills,
		Status:          uint8(cRep.status),
		ErrorCode:       int32(cRep.error_code),
	}
}
