package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/metrics"
)

const (
	// DefaultIngressCapacity is the bounded CLOB ingress ring channel capacity (architecture.md §3).
	DefaultIngressCapacity = 65536
	// BackpressureThresholdPct sheds non-cancel orders above 85% utilization.
	BackpressureThresholdPct = 85
)

var (
	// ErrEngineBackpressure indicates >85% ingress ring utilization.
	ErrEngineBackpressure = errors.New("ERR_ENGINE_BACKPRESSURE")
	// ErrCircuitBreakerTripped indicates mark price collar exceeded.
	ErrCircuitBreakerTripped = errors.New("ERR_CIRCUIT_BREAKER_TRIPPED")
	// ErrOrderPoolExhausted indicates resting slab capacity reached.
	ErrOrderPoolExhausted = errors.New("ERR_ORDER_POOL_EXHAUSTED")
	// ErrSelfTradePrevented indicates STP prevented fill.
	ErrSelfTradePrevented = errors.New("ERR_SELF_TRADE_PREVENTED")
	// ErrEngineClosed indicates the dispatcher has stopped.
	ErrEngineClosed = errors.New("engine dispatcher closed")
)

// OrderNode mirrors the 64-byte OrderNode state in ADR-002.
type OrderNode struct {
	OrderID       uint64 `json:"order_id"`
	AccountIDLow  uint64 `json:"account_id_low"`
	AccountIDHigh uint64 `json:"account_id_high"`
	Price1e8      int64  `json:"price_1e8"`
	Remaining1e8  int64  `json:"remaining_1e8"`
	TimestampNs   uint64 `json:"timestamp_ns"`
	MarketID      uint16 `json:"market_id"`
	Side          uint8  `json:"side"`
	OrderType     uint8  `json:"order_type"`
	TimeInForce   uint8  `json:"time_in_force"`
	STPMode       uint8  `json:"stp_mode"`
}

type priceLevel struct {
	price1e8 int64
	totalQty int64
	orders   []*OrderNode // FIFO queue
}

type orderBook struct {
	marketID              uint16
	bids                  []*priceLevel // sorted descending by price1e8
	asks                  []*priceLevel // sorted ascending by price1e8
	byOrderID             map[uint64]*OrderNode
	markPrice1e8          int64
	circuitBreakerTripped bool
}

func newOrderBook(marketID uint16, initialMark1e8 int64) *orderBook {
	return &orderBook{
		marketID:     marketID,
		byOrderID:    make(map[uint64]*OrderNode, 1024),
		markPrice1e8: initialMark1e8,
	}
}

func (ob *orderBook) findLevel(levels []*priceLevel, price int64, isBid bool) (int, bool) {
	idx := sort.Search(len(levels), func(i int) bool {
		if isBid {
			return levels[i].price1e8 <= price
		}
		return levels[i].price1e8 >= price
	})
	if idx < len(levels) && levels[idx].price1e8 == price {
		return idx, true
	}
	return idx, false
}

func (ob *orderBook) addOrder(node *OrderNode) {
	isBid := node.Side == SideCodeBuy
	var levels []*priceLevel
	if isBid {
		levels = ob.bids
	} else {
		levels = ob.asks
	}

	idx, exists := ob.findLevel(levels, node.Price1e8, isBid)
	if !exists {
		lvl := &priceLevel{
			price1e8: node.Price1e8,
			orders:   make([]*OrderNode, 0, 4),
		}
		levels = append(levels, nil)
		copy(levels[idx+1:], levels[idx:])
		levels[idx] = lvl
		if isBid {
			ob.bids = levels
		} else {
			ob.asks = levels
		}
	}
	lvl := levels[idx]
	lvl.orders = append(lvl.orders, node)
	lvl.totalQty += node.Remaining1e8
	ob.byOrderID[node.OrderID] = node
}

func (ob *orderBook) removeOrder(orderID uint64) (*OrderNode, bool) {
	node, ok := ob.byOrderID[orderID]
	if !ok {
		return nil, false
	}
	delete(ob.byOrderID, orderID)

	isBid := node.Side == SideCodeBuy
	levels := ob.asks
	if isBid {
		levels = ob.bids
	}
	idx, exists := ob.findLevel(levels, node.Price1e8, isBid)
	if !exists {
		return node, true
	}
	lvl := levels[idx]
	for i, o := range lvl.orders {
		if o.OrderID == orderID {
			copy(lvl.orders[i:], lvl.orders[i+1:])
			lvl.orders[len(lvl.orders)-1] = nil
			lvl.orders = lvl.orders[:len(lvl.orders)-1]
			lvl.totalQty -= node.Remaining1e8
			break
		}
	}
	if len(lvl.orders) == 0 {
		copy(levels[idx:], levels[idx+1:])
		levels[len(levels)-1] = nil
		levels = levels[:len(levels)-1]
		if isBid {
			ob.bids = levels
		} else {
			ob.asks = levels
		}
	}
	return node, true
}

func (ob *orderBook) totalResting() int {
	return len(ob.byOrderID)
}

func (ob *orderBook) availableForFOK(side uint8, limitPrice int64, isMarket bool) int64 {
	var levels []*priceLevel
	if side == SideCodeBuy {
		levels = ob.asks
	} else {
		levels = ob.bids
	}
	var avail int64
	for _, lvl := range levels {
		if !isMarket {
			if side == SideCodeBuy && lvl.price1e8 > limitPrice {
				break
			}
			if side == SideCodeSell && lvl.price1e8 < limitPrice {
				break
			}
		}
		avail += lvl.totalQty
	}
	return avail
}

// EngineConfig configures the deterministic matching engine bridge.
type EngineConfig struct {
	WALDir            string
	SnapshotDir       string
	WALFsyncMode      string
	MaxRestingOrders  int
	CircuitBreakerBps int64
	IngressCapacity   int
	DisableWAL        bool
}

type dispatchReq struct {
	cmd  OrderCmd
	resp chan ExecReport
}

// ExecListener receives completed command reports from the pinned matching thread.
type ExecListener func(cmd OrderCmd, report ExecReport)

// Bridge serializes all order book mutations onto a single OS-pinned thread (runtime.LockOSThread())
// and provides C ABI compatibility, binary CRC32C WAL persistence, snapshots, and SHA-256 state hashing.
type Bridge struct {
	cfg        EngineConfig
	wal        *WALWriter
	evlog      *EventLogWriter
	books      map[uint16]*orderBook
	walSeq     uint64
	nextTrade  uint64
	ingress    chan dispatchReq
	cancelCh   chan dispatchReq
	stopCh     chan struct{}
	doneCh     chan struct{}
	closed     atomic.Bool
	listeners  []ExecListener
	listenerMu sync.RWMutex
	stateMu    sync.RWMutex // protects snapshot/read-only depth queries
	metricsReg *metrics.Registry
}

// NewBridge initializes the order books, WAL writer, and starts the single pinned OS-thread loop.
func NewBridge(cfg EngineConfig, reg *metrics.Registry) (*Bridge, error) {
	if cfg.MaxRestingOrders <= 0 {
		cfg.MaxRestingOrders = 1048576
	}
	if cfg.CircuitBreakerBps <= 0 {
		cfg.CircuitBreakerBps = 1000
	}
	if cfg.IngressCapacity <= 0 {
		cfg.IngressCapacity = DefaultIngressCapacity
	}
	if reg == nil {
		reg = metrics.Default
	}

	var wal *WALWriter
	var evlog *EventLogWriter
	var err error
	if !cfg.DisableWAL {
		wal, err = NewWALWriter(cfg.WALDir, cfg.WALFsyncMode)
		if err != nil {
			return nil, err
		}
		if wal.Path() != "" {
			evlog, err = NewEventLogWriter(wal.Path()+".evlog", cfg.WALFsyncMode)
			if err != nil {
				_ = wal.Close()
				return nil, err
			}
		}
	}

	b := &Bridge{
		cfg:   cfg,
		wal:   wal,
		evlog: evlog,
		books: map[uint16]*orderBook{
			MarketCodeETHUSDC: newOrderBook(MarketCodeETHUSDC, 3100*ledger.CLOBScale),
			MarketCodeBTCUSDC: newOrderBook(MarketCodeBTCUSDC, 95000*ledger.CLOBScale),
		},
		nextTrade:  1,
		ingress:    make(chan dispatchReq, cfg.IngressCapacity),
		cancelCh:   make(chan dispatchReq, cfg.IngressCapacity/4+64),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
		metricsReg: reg,
	}

	go b.runPinnedLoop()
	return b, nil
}

// AddListener registers a callback invoked after every command execution.
func (b *Bridge) AddListener(fn ExecListener) {
	b.listenerMu.Lock()
	defer b.listenerMu.Unlock()
	b.listeners = append(b.listeners, fn)
}

func (b *Bridge) notifyListeners(cmd OrderCmd, rep ExecReport) {
	b.listenerMu.RLock()
	fns := make([]ExecListener, len(b.listeners))
	copy(fns, b.listeners)
	b.listenerMu.RUnlock()
	for _, fn := range fns {
		fn(cmd, rep)
	}
}

func (b *Bridge) runPinnedLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(b.doneCh)

	for {
		// Always prioritize cancellation commands before new submissions.
		select {
		case req := <-b.cancelCh:
			rep := b.executeLocked(req.cmd)
			req.resp <- rep
			b.notifyListeners(req.cmd, rep)
			continue
		default:
		}

		select {
		case <-b.stopCh:
			return
		case req := <-b.cancelCh:
			rep := b.executeLocked(req.cmd)
			req.resp <- rep
			b.notifyListeners(req.cmd, rep)
		case req := <-b.ingress:
			rep := b.executeLocked(req.cmd)
			req.resp <- rep
			b.notifyListeners(req.cmd, rep)
		}
	}
}

// Submit dispatches a command to the pinned matching thread with 85% backpressure shedding.
func (b *Bridge) Submit(cmd OrderCmd) (ExecReport, error) {
	if b.closed.Load() {
		return ExecReport{ErrorCode: ErrCodeBackpressure}, ErrEngineClosed
	}
	isCancel := cmd.Opcode == OpCancelOrder || cmd.Opcode == OpCancelAllAccount
	if !isCancel && len(b.ingress)*100 >= cap(b.ingress)*BackpressureThresholdPct {
		return ExecReport{ErrorCode: ErrCodeBackpressure}, ErrEngineBackpressure
	}

	req := dispatchReq{
		cmd:  cmd,
		resp: make(chan ExecReport, 1),
	}
	if isCancel {
		select {
		case b.cancelCh <- req:
		case <-b.stopCh:
			return ExecReport{ErrorCode: ErrCodeBackpressure}, ErrEngineClosed
		}
	} else {
		select {
		case b.ingress <- req:
		case <-b.stopCh:
			return ExecReport{ErrorCode: ErrCodeBackpressure}, ErrEngineClosed
		}
	}

	select {
	case rep := <-req.resp:
		switch rep.ErrorCode {
		case ErrCodeCircuitBreakerTripped:
			return rep, ErrCircuitBreakerTripped
		case ErrCodeOrderPoolExhausted:
			return rep, ErrOrderPoolExhausted
		case ErrCodeSTPRejected:
			return rep, ErrSelfTradePrevented
		default:
			return rep, nil
		}
	case <-b.stopCh:
		return ExecReport{ErrorCode: ErrCodeBackpressure}, ErrEngineClosed
	}
}

// SyncWAL flushes both the command WAL and execution event log to disk.
func (b *Bridge) SyncWAL() error {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	if b.wal != nil {
		if err := b.wal.Sync(); err != nil {
			return err
		}
	}
	if b.evlog != nil {
		if err := b.evlog.Sync(); err != nil {
			return err
		}
	}
	return nil
}

// Close gracefully terminates the pinned matching loop and closes the WAL and event log.
func (b *Bridge) Close() error {
	if b.closed.Swap(true) {
		return nil
	}
	close(b.stopCh)
	<-b.doneCh
	var firstErr error
	if b.evlog != nil {
		if err := b.evlog.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if b.wal != nil {
		if err := b.wal.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (b *Bridge) executeLocked(cmd OrderCmd) ExecReport {
	start := time.Now()
	b.stateMu.Lock()
	defer b.stateMu.Unlock()

	if cmd.WalSeq > 0 {
		if cmd.WalSeq > b.walSeq {
			b.walSeq = cmd.WalSeq
		}
	} else {
		b.walSeq++
		cmd.WalSeq = b.walSeq
	}

	// Write WAL frame before mutating order book state.
	if b.wal != nil {
		rec := WALRecord{
			Magic:         WALMagic,
			Version:       WALVersion,
			Opcode:        cmd.Opcode,
			MarketID:      cmd.MarketID,
			Flags:         PackFlags(cmd.Side, cmd.OrderType, cmd.TimeInForce, cmd.STPMode),
			WalSeq:        cmd.WalSeq,
			OrderID:       cmd.OrderID,
			AccountIDLow:  cmd.AccountIDLow,
			AccountIDHigh: cmd.AccountIDHigh,
			Price1e8:      cmd.Price1e8,
			Qty1e8:        cmd.Qty1e8,
		}
		_ = b.wal.Append(rec)
	}

	ob, ok := b.books[cmd.MarketID]
	if !ok {
		return ExecReport{WalSeq: cmd.WalSeq, OrderID: cmd.OrderID, Status: ExecStatusCBRejected, ErrorCode: ErrCodeInvalidNumericBound}
	}

	var rep ExecReport
	switch cmd.Opcode {
	case OpUpdateMarkPrice:
		if cmd.Price1e8 <= 0 || cmd.Price1e8 > ledger.MaxCLOBValue {
			rep = ExecReport{WalSeq: cmd.WalSeq, ErrorCode: ErrCodeInvalidNumericBound}
		} else {
			ob.markPrice1e8 = cmd.Price1e8
			ob.circuitBreakerTripped = false
			if mkt, err := CodeToMarketID(cmd.MarketID); err == nil {
				b.metricsReg.SetCircuitBreaker(mkt, false)
			}
			rep = ExecReport{WalSeq: cmd.WalSeq, Status: ExecStatusAccepted, ErrorCode: ErrCodeOK}
		}
	case OpCancelOrder:
		node, removed := ob.removeOrder(cmd.OrderID)
		if !removed {
			rep = ExecReport{WalSeq: cmd.WalSeq, OrderID: cmd.OrderID, Status: ExecStatusCanceled, ErrorCode: ErrCodeOrderNotFound}
		} else {
			rep = ExecReport{
				WalSeq:          cmd.WalSeq,
				OrderID:         cmd.OrderID,
				RemainingQty1e8: node.Remaining1e8,
				Status:          ExecStatusCanceled,
				ErrorCode:       ErrCodeOK,
			}
		}
	case OpCancelAllAccount:
		var canceled []uint64
		for id, n := range ob.byOrderID {
			if n.AccountIDLow == cmd.AccountIDLow && n.AccountIDHigh == cmd.AccountIDHigh {
				canceled = append(canceled, id)
			}
		}
		sort.Slice(canceled, func(i, j int) bool { return canceled[i] < canceled[j] })
		for _, id := range canceled {
			ob.removeOrder(id)
		}
		rep = ExecReport{
			WalSeq:      cmd.WalSeq,
			Status:      ExecStatusCanceled,
			CanceledIDs: canceled,
			ErrorCode:   ErrCodeOK,
		}
	case OpSnapshotBarrier:
		rep = ExecReport{WalSeq: cmd.WalSeq, Status: ExecStatusAccepted, ErrorCode: ErrCodeOK}
	case OpSubmitOrder:
		rep = b.executeSubmit(ob, cmd)
	default:
		rep = ExecReport{WalSeq: cmd.WalSeq, ErrorCode: ErrCodeInvalidNumericBound}
	}

	rep.LatencyNs = time.Since(start).Nanoseconds()
	if cmd.Opcode == OpSubmitOrder {
		b.metricsReg.RecordMatchLatencyNs(rep.LatencyNs)
	}
	if b.evlog != nil {
		b.appendExecutionEvents(cmd, rep)
	}
	return rep
}

func (b *Bridge) appendExecutionEvents(cmd OrderCmd, rep ExecReport) {
	var sideFlag uint8
	if cmd.Side == SideCodeSell {
		sideFlag = EventFlagSideSell
	}
	takerTermFlag := uint8(0)
	if rep.Status == ExecStatusFilled || rep.Status == ExecStatusCanceled {
		takerTermFlag = EventFlagTakerTerminal
	}
	for _, f := range rep.Fills {
		flags := sideFlag | takerTermFlag
		if f.MakerRemaining == 0 {
			flags |= EventFlagMakerTerminal
		}
		_ = b.evlog.Append(WalEventRecord{
			EventType:    EventTypeTradeFill,
			MarketID:     cmd.MarketID,
			Status:       rep.Status,
			Flags:        flags,
			WalSeq:       rep.WalSeq,
			OrderID:      cmd.OrderID,
			MakerOrderID: f.MakerOrderID,
			TradeID:      f.TradeID,
			ExecPrice1e8: f.ExecPrice1e8,
			FillQty1e8:   f.FillQty1e8,
		})
	}
	for _, cid := range rep.CanceledIDs {
		_ = b.evlog.Append(WalEventRecord{
			EventType: EventTypeOrderCanceled,
			MarketID:  cmd.MarketID,
			Status:    ExecStatusCanceled,
			WalSeq:    rep.WalSeq,
			OrderID:   cid,
		})
	}
	switch cmd.Opcode {
	case OpCancelOrder:
		if rep.ErrorCode == ErrCodeOK {
			_ = b.evlog.Append(WalEventRecord{
				EventType:  EventTypeOrderCanceled,
				MarketID:   cmd.MarketID,
				Status:     ExecStatusCanceled,
				WalSeq:     rep.WalSeq,
				OrderID:    cmd.OrderID,
				FillQty1e8: rep.RemainingQty1e8,
			})
		}
	case OpSubmitOrder:
		switch rep.Status {
		case ExecStatusAccepted, ExecStatusPartiallyFilled:
			_ = b.evlog.Append(WalEventRecord{
				EventType:    EventTypeOrderAccepted,
				MarketID:     cmd.MarketID,
				Status:       rep.Status,
				Flags:        sideFlag,
				WalSeq:       rep.WalSeq,
				OrderID:      cmd.OrderID,
				ExecPrice1e8: cmd.Price1e8,
				FillQty1e8:   rep.RemainingQty1e8,
			})
		case ExecStatusCBRejected, ExecStatusSTPCanceled:
			_ = b.evlog.Append(WalEventRecord{
				EventType:    EventTypeOrderRejected,
				MarketID:     cmd.MarketID,
				Status:       rep.Status,
				Flags:        sideFlag,
				WalSeq:       rep.WalSeq,
				OrderID:      cmd.OrderID,
				ExecPrice1e8: cmd.Price1e8,
				FillQty1e8:   rep.RemainingQty1e8,
			})
		case ExecStatusCanceled:
			if rep.FilledQty1e8 == 0 {
				_ = b.evlog.Append(WalEventRecord{
					EventType:    EventTypeOrderCanceled,
					MarketID:     cmd.MarketID,
					Status:       rep.Status,
					Flags:        sideFlag,
					WalSeq:       rep.WalSeq,
					OrderID:      cmd.OrderID,
					ExecPrice1e8: cmd.Price1e8,
					FillQty1e8:   rep.RemainingQty1e8,
				})
			}
		}
	}
}

func (b *Bridge) totalRestingAcrossBooks() int {
	total := 0
	for _, ob := range b.books {
		total += ob.totalResting()
	}
	return total
}

func (b *Bridge) isPriceOutsideCollar(ob *orderBook, price1e8 int64) bool {
	if ob.markPrice1e8 <= 0 || price1e8 <= 0 {
		return false
	}
	diff := price1e8 - ob.markPrice1e8
	if diff < 0 {
		diff = -diff
	}
	// diff * 10000 > markPrice1e8 * cbBps (exact integer comparison via __int128-equivalent)
	maxDev := (ob.markPrice1e8 * b.cfg.CircuitBreakerBps) / 10_000
	return diff > maxDev
}

func (b *Bridge) executeSubmit(ob *orderBook, cmd OrderCmd) ExecReport {
	if cmd.Qty1e8 < ledger.MinCLOBValue || cmd.Qty1e8 > ledger.MaxCLOBValue {
		return ExecReport{WalSeq: cmd.WalSeq, OrderID: cmd.OrderID, Status: ExecStatusCBRejected, ErrorCode: ErrCodeInvalidNumericBound}
	}
	isMarket := cmd.OrderType == OrderTypeCodeMarket
	if !isMarket && (cmd.Price1e8 < ledger.MinCLOBValue || cmd.Price1e8 > ledger.MaxCLOBValue) {
		return ExecReport{WalSeq: cmd.WalSeq, OrderID: cmd.OrderID, Status: ExecStatusCBRejected, ErrorCode: ErrCodeInvalidNumericBound}
	}

	// Circuit breaker check: if already tripped or aggressive order crosses > +/-10% collar.
	if ob.circuitBreakerTripped {
		return ExecReport{WalSeq: cmd.WalSeq, OrderID: cmd.OrderID, RemainingQty1e8: cmd.Qty1e8, Status: ExecStatusCBRejected, ErrorCode: ErrCodeCircuitBreakerTripped}
	}
	if !isMarket && b.isAggressiveCrossing(ob, cmd.Side, cmd.Price1e8) && b.isPriceOutsideCollar(ob, cmd.Price1e8) {
		ob.circuitBreakerTripped = true
		if mkt, err := CodeToMarketID(cmd.MarketID); err == nil {
			b.metricsReg.SetCircuitBreaker(mkt, true)
		}
		return ExecReport{WalSeq: cmd.WalSeq, OrderID: cmd.OrderID, RemainingQty1e8: cmd.Qty1e8, Status: ExecStatusCBRejected, ErrorCode: ErrCodeCircuitBreakerTripped}
	}

	// FOK pre-check: ensure sufficient opposite liquidity exists before mutating book.
	if cmd.TimeInForce == TIFCodeFOK {
		avail := ob.availableForFOK(cmd.Side, cmd.Price1e8, isMarket)
		if avail < cmd.Qty1e8 {
			return ExecReport{
				WalSeq:          cmd.WalSeq,
				OrderID:         cmd.OrderID,
				RemainingQty1e8: cmd.Qty1e8,
				Status:          ExecStatusCanceled,
				ErrorCode:       ErrCodeFOKUnfilled,
			}
		}
	}

	remaining := cmd.Qty1e8
	filled := int64(0)
	var fills []FillEvent
	var stpCanceled []uint64
	stpTriggered := false

	for remaining > 0 {
		var oppLevels []*priceLevel
		if cmd.Side == SideCodeBuy {
			oppLevels = ob.asks
		} else {
			oppLevels = ob.bids
		}
		if len(oppLevels) == 0 {
			break
		}
		bestLvl := oppLevels[0]
		if !isMarket {
			if cmd.Side == SideCodeBuy && bestLvl.price1e8 > cmd.Price1e8 {
				break
			}
			if cmd.Side == SideCodeSell && bestLvl.price1e8 < cmd.Price1e8 {
				break
			}
		}
		// Check collar on execution price as well.
		if b.isPriceOutsideCollar(ob, bestLvl.price1e8) {
			ob.circuitBreakerTripped = true
			if mkt, err := CodeToMarketID(cmd.MarketID); err == nil {
				b.metricsReg.SetCircuitBreaker(mkt, true)
			}
			if filled == 0 {
				return ExecReport{WalSeq: cmd.WalSeq, OrderID: cmd.OrderID, RemainingQty1e8: cmd.Qty1e8, Status: ExecStatusCBRejected, ErrorCode: ErrCodeCircuitBreakerTripped}
			}
			break
		}

		maker := bestLvl.orders[0]
		// Self-Trade Prevention (STP) check.
		if maker.AccountIDLow == cmd.AccountIDLow && maker.AccountIDHigh == cmd.AccountIDHigh {
			stpTriggered = true
			switch cmd.STPMode {
			case STPCodeCancelNewest:
				remaining = 0
				goto doneMatching
			case STPCodeCancelOldest:
				ob.removeOrder(maker.OrderID)
				stpCanceled = append(stpCanceled, maker.OrderID)
				continue
			case STPCodeCancelBoth:
				ob.removeOrder(maker.OrderID)
				stpCanceled = append(stpCanceled, maker.OrderID)
				remaining = 0
				goto doneMatching
			}
		}

		matchQty := remaining
		if maker.Remaining1e8 < matchQty {
			matchQty = maker.Remaining1e8
		}
		notional, err := ledger.ComputeQuoteNotional1e8(bestLvl.price1e8, matchQty)
		if err != nil {
			return ExecReport{WalSeq: cmd.WalSeq, OrderID: cmd.OrderID, Status: ExecStatusCBRejected, ErrorCode: ErrCodeInvalidNumericBound}
		}

		maker.Remaining1e8 -= matchQty
		bestLvl.totalQty -= matchQty
		remaining -= matchQty
		filled += matchQty

		tradeID := b.nextTrade
		b.nextTrade++
		fills = append(fills, FillEvent{
			TradeID:          tradeID,
			MakerOrderID:     maker.OrderID,
			TakerOrderID:     cmd.OrderID,
			MakerAccountLow:  maker.AccountIDLow,
			MakerAccountHigh: maker.AccountIDHigh,
			ExecPrice1e8:     bestLvl.price1e8,
			FillQty1e8:       matchQty,
			QuoteNotional1e8: notional,
			MakerRemaining:   maker.Remaining1e8,
		})

		if maker.Remaining1e8 == 0 {
			ob.removeOrder(maker.OrderID)
		}
	}

doneMatching:
	// Rest unfilled GTC limit remainder in book.
	status := ExecStatusFilled
	errCode := ErrCodeOK
	if remaining > 0 {
		if cmd.OrderType == OrderTypeCodeLimit && cmd.TimeInForce == TIFCodeGTC {
			if b.totalRestingAcrossBooks() >= b.cfg.MaxRestingOrders {
				return ExecReport{
					WalSeq:          cmd.WalSeq,
					OrderID:         cmd.OrderID,
					FilledQty1e8:    filled,
					RemainingQty1e8: remaining,
					Fills:           fills,
					CanceledIDs:     stpCanceled,
					Status:          ExecStatusCanceled,
					ErrorCode:       ErrCodeOrderPoolExhausted,
				}
			}
			ob.addOrder(&OrderNode{
				OrderID:       cmd.OrderID,
				AccountIDLow:  cmd.AccountIDLow,
				AccountIDHigh: cmd.AccountIDHigh,
				Price1e8:      cmd.Price1e8,
				Remaining1e8:  remaining,
				TimestampNs:   cmd.TimestampNs,
				MarketID:      cmd.MarketID,
				Side:          cmd.Side,
				OrderType:     cmd.OrderType,
				TimeInForce:   cmd.TimeInForce,
				STPMode:       cmd.STPMode,
			})
			if filled > 0 {
				status = ExecStatusPartiallyFilled
			} else {
				status = ExecStatusAccepted
			}
		} else {
			if stpTriggered && filled == 0 {
				status = ExecStatusSTPCanceled
				errCode = ErrCodeSTPRejected
			} else if filled > 0 {
				status = ExecStatusPartiallyFilled
			} else {
				status = ExecStatusCanceled
			}
		}
	} else if stpTriggered && filled == 0 {
		status = ExecStatusSTPCanceled
		errCode = ErrCodeSTPRejected
	}

	return ExecReport{
		WalSeq:          cmd.WalSeq,
		OrderID:         cmd.OrderID,
		FilledQty1e8:    filled,
		RemainingQty1e8: remaining,
		Fills:           fills,
		CanceledIDs:     stpCanceled,
		Status:          status,
		ErrorCode:       errCode,
	}
}

func (b *Bridge) isAggressiveCrossing(ob *orderBook, side uint8, price1e8 int64) bool {
	if side == SideCodeBuy && len(ob.asks) > 0 {
		return price1e8 >= ob.asks[0].price1e8
	}
	if side == SideCodeSell && len(ob.bids) > 0 {
		return price1e8 <= ob.bids[0].price1e8
	}
	return false
}

// UpdateMarkPrice updates a market's mark price and resets its circuit breaker.
func (b *Bridge) UpdateMarkPrice(marketID string, price1e8 int64) error {
	code, err := MarketIDToCode(marketID)
	if err != nil {
		return err
	}
	_, err = b.Submit(OrderCmd{
		MarketID: code,
		Opcode:   OpUpdateMarkPrice,
		Price1e8: price1e8,
	})
	return err
}

// ResetCircuitBreaker clears a tripped circuit breaker for a market.
func (b *Bridge) ResetCircuitBreaker(marketID string) error {
	code, err := MarketIDToCode(marketID)
	if err != nil {
		return err
	}
	b.stateMu.Lock()
	if ob, ok := b.books[code]; ok {
		ob.circuitBreakerTripped = false
	}
	b.stateMu.Unlock()
	b.metricsReg.SetCircuitBreaker(marketID, false)
	return nil
}

// Depth returns the top-N bid and ask price levels for a market.
func (b *Bridge) Depth(marketID string, maxLevels int) (L2DepthSnapshot, error) {
	code, err := MarketIDToCode(marketID)
	if err != nil {
		return L2DepthSnapshot{}, err
	}
	if maxLevels <= 0 {
		maxLevels = 20
	}
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()

	ob := b.books[code]
	bidsN := min(maxLevels, len(ob.bids))
	asksN := min(maxLevels, len(ob.asks))
	bids := make([]L2Level, bidsN)
	for i := 0; i < bidsN; i++ {
		bids[i] = L2Level{Price1e8: ob.bids[i].price1e8, Qty1e8: ob.bids[i].totalQty}
	}
	asks := make([]L2Level, asksN)
	for i := 0; i < asksN; i++ {
		asks[i] = L2Level{Price1e8: ob.asks[i].price1e8, Qty1e8: ob.asks[i].totalQty}
	}
	return L2DepthSnapshot{
		MarketID:              marketID,
		WalSeq:                b.walSeq,
		MarkPrice1e8:          ob.markPrice1e8,
		CircuitBreakerTripped: ob.circuitBreakerTripped,
		Bids:                  bids,
		Asks:                  asks,
	}, nil
}

// StateHash computes the canonical SHA-256 digest of all resting orders per ADR-005 §3.3.
func (b *Bridge) StateHash() [32]byte {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()

	h := sha256.New()
	var buf [41]byte
	for _, code := range []uint16{MarketCodeETHUSDC, MarketCodeBTCUSDC} {
		ob := b.books[code]
		for _, lvl := range ob.bids {
			for _, ord := range lvl.orders {
				writeCanonicalOrder(buf[:], ord)
				_, _ = h.Write(buf[:])
			}
		}
		for _, lvl := range ob.asks {
			for _, ord := range lvl.orders {
				writeCanonicalOrder(buf[:], ord)
				_, _ = h.Write(buf[:])
			}
		}
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func writeCanonicalOrder(dst []byte, ord *OrderNode) {
	binary.LittleEndian.PutUint64(dst[0:8], ord.OrderID)
	binary.LittleEndian.PutUint64(dst[8:16], ord.AccountIDHigh)
	binary.LittleEndian.PutUint64(dst[16:24], ord.AccountIDLow)
	binary.LittleEndian.PutUint64(dst[24:32], uint64(ord.Price1e8))
	binary.LittleEndian.PutUint64(dst[32:40], uint64(ord.Remaining1e8))
	dst[40] = ord.Side
}

type engineSnapshot struct {
	WalSeq    uint64      `json:"wal_seq"`
	NextTrade uint64      `json:"next_trade"`
	MarkETH   int64       `json:"mark_eth"`
	MarkBTC   int64       `json:"mark_btc"`
	Orders    []OrderNode `json:"orders"`
}

// SaveSnapshot writes a deterministic snapshot file snapshot_<wal_seq>.snap into snapshotDir.
func (b *Bridge) SaveSnapshot(snapshotDir string) (string, error) {
	if snapshotDir == "" {
		snapshotDir = b.cfg.SnapshotDir
	}
	if err := os.MkdirAll(snapshotDir, 0o750); err != nil {
		return "", err
	}

	b.stateMu.RLock()
	snap := engineSnapshot{
		WalSeq:    b.walSeq,
		NextTrade: b.nextTrade,
		MarkETH:   b.books[MarketCodeETHUSDC].markPrice1e8,
		MarkBTC:   b.books[MarketCodeBTCUSDC].markPrice1e8,
	}
	for _, code := range []uint16{MarketCodeETHUSDC, MarketCodeBTCUSDC} {
		ob := b.books[code]
		for _, lvl := range ob.bids {
			for _, o := range lvl.orders {
				snap.Orders = append(snap.Orders, *o)
			}
		}
		for _, lvl := range ob.asks {
			for _, o := range lvl.orders {
				snap.Orders = append(snap.Orders, *o)
			}
		}
	}
	b.stateMu.RUnlock()

	raw, err := json.Marshal(snap)
	if err != nil {
		return "", err
	}
	path := filepath.Join(snapshotDir, fmt.Sprintf("snapshot_%d.snap", snap.WalSeq))
	if err := os.WriteFile(path, raw, 0o640); err != nil { // #nosec G306
		return "", err
	}
	return path, nil
}

// LoadLatestSnapshot restores the order book from the newest snapshot_<seq>.snap in snapshotDir.
func (b *Bridge) LoadLatestSnapshot(snapshotDir string) (uint64, error) {
	if snapshotDir == "" {
		snapshotDir = b.cfg.SnapshotDir
	}
	entries, err := os.ReadDir(snapshotDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var bestSeq uint64
	var bestPath string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "snapshot_") && strings.HasSuffix(name, ".snap") {
			numStr := strings.TrimSuffix(strings.TrimPrefix(name, "snapshot_"), ".snap")
			if seq, err := strconv.ParseUint(numStr, 10, 64); err == nil && seq >= bestSeq {
				bestSeq = seq
				bestPath = filepath.Join(snapshotDir, name)
			}
		}
	}
	if bestPath == "" {
		return 0, nil
	}
	raw, err := os.ReadFile(bestPath) // #nosec G304
	if err != nil {
		return 0, err
	}
	var snap engineSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return 0, err
	}

	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	b.walSeq = snap.WalSeq
	b.nextTrade = snap.NextTrade
	b.books[MarketCodeETHUSDC] = newOrderBook(MarketCodeETHUSDC, snap.MarkETH)
	b.books[MarketCodeBTCUSDC] = newOrderBook(MarketCodeBTCUSDC, snap.MarkBTC)
	for i := range snap.Orders {
		n := snap.Orders[i]
		if ob, ok := b.books[n.MarketID]; ok {
			cp := n
			ob.addOrder(&cp)
		}
	}
	return snap.WalSeq, nil
}

// ReplayFromWAL loads the latest snapshot (if snapshotDir != "") and replays all WAL records with seq > snapshotSeq.
func ReplayFromWAL(walPath, snapshotDir string, cfg EngineConfig) (*Bridge, int, error) {
	cfg.DisableWAL = true
	b, err := NewBridge(cfg, metrics.NewRegistry())
	if err != nil {
		return nil, 0, err
	}
	snapSeq, err := b.LoadLatestSnapshot(snapshotDir)
	if err != nil {
		_ = b.Close()
		return nil, 0, err
	}
	records, _, err := ReadWAL(walPath)
	if err != nil {
		_ = b.Close()
		return nil, 0, err
	}
	replayed := 0
	for _, rec := range records {
		if rec.WalSeq <= snapSeq {
			continue
		}
		side, orderType, tif, stp := UnpackFlags(rec.Flags)
		_, _ = b.Submit(OrderCmd{
			WalSeq:        rec.WalSeq,
			OrderID:       rec.OrderID,
			AccountIDLow:  rec.AccountIDLow,
			AccountIDHigh: rec.AccountIDHigh,
			Price1e8:      rec.Price1e8,
			Qty1e8:        rec.Qty1e8,
			MarketID:      rec.MarketID,
			Opcode:        rec.Opcode,
			Side:          side,
			OrderType:     orderType,
			TimeInForce:   tif,
			STPMode:       stp,
		})
		replayed++
	}
	return b, replayed, nil
}
