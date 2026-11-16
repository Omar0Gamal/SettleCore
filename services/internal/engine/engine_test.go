package engine

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/metrics"
)

func TestCLOBPriceTimeSTPAndCircuitBreaker(t *testing.T) {
	reg := metrics.NewRegistry()
	b, err := NewBridge(EngineConfig{
		DisableWAL:        true,
		CircuitBreakerBps: 1000,
	}, reg)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	defer func() { _ = b.Close() }()

	u1 := uuid.New()
	u2 := uuid.New()
	u1Low, u1High := UUIDToUint64Pair(u1)
	u2Low, u2High := UUIDToUint64Pair(u2)

	// Resting ask from u1: 1 ETH @ 3100
	rep, err := b.Submit(OrderCmd{
		OrderID: 1, AccountIDLow: u1Low, AccountIDHigh: u1High,
		Price1e8: 3100 * ledger.CLOBScale, Qty1e8: 1 * ledger.CLOBScale,
		MarketID: MarketCodeETHUSDC, Opcode: OpSubmitOrder,
		Side: SideCodeSell, OrderType: OrderTypeCodeLimit, TimeInForce: TIFCodeGTC,
	})
	if err != nil || rep.Status != ExecStatusAccepted {
		t.Fatalf("unexpected resting ask rep=%+v err=%v", rep, err)
	}

	// Self-Trade Prevention: u1 tries to buy @ 3100 with CANCEL_OLDEST -> cancels order 1 and rests order 2!
	rep, err = b.Submit(OrderCmd{
		OrderID: 2, AccountIDLow: u1Low, AccountIDHigh: u1High,
		Price1e8: 3100 * ledger.CLOBScale, Qty1e8: 1 * ledger.CLOBScale,
		MarketID: MarketCodeETHUSDC, Opcode: OpSubmitOrder,
		Side: SideCodeBuy, OrderType: OrderTypeCodeLimit, TimeInForce: TIFCodeGTC, STPMode: STPCodeCancelOldest,
	})
	if err != nil || len(rep.Fills) != 0 || len(rep.CanceledIDs) != 1 || rep.CanceledIDs[0] != 1 {
		t.Fatalf("expected STP CANCEL_OLDEST on order 1, got rep=%+v err=%v", rep, err)
	}

	// u2 crosses resting bid of order 2 -> matches!
	rep, err = b.Submit(OrderCmd{
		OrderID: 3, AccountIDLow: u2Low, AccountIDHigh: u2High,
		Price1e8: 3100 * ledger.CLOBScale, Qty1e8: 1 * ledger.CLOBScale,
		MarketID: MarketCodeETHUSDC, Opcode: OpSubmitOrder,
		Side: SideCodeSell, OrderType: OrderTypeCodeLimit, TimeInForce: TIFCodeGTC,
	})
	if err != nil || rep.Status != ExecStatusFilled || len(rep.Fills) != 1 {
		t.Fatalf("expected fill against u1 bid, got rep=%+v err=%v", rep, err)
	}

	// Circuit breaker: rest bid at -15% (2635 USDC) then sweep it -> must trip circuit breaker!
	_, _ = b.Submit(OrderCmd{
		OrderID: 4, AccountIDLow: u1Low, AccountIDHigh: u1High,
		Price1e8: 2635 * ledger.CLOBScale, Qty1e8: 1 * ledger.CLOBScale,
		MarketID: MarketCodeETHUSDC, Opcode: OpSubmitOrder,
		Side: SideCodeBuy, OrderType: OrderTypeCodeLimit, TimeInForce: TIFCodeGTC,
	})
	rep, err = b.Submit(OrderCmd{
		OrderID: 5, AccountIDLow: u2Low, AccountIDHigh: u2High,
		Price1e8: 2635 * ledger.CLOBScale, Qty1e8: 1 * ledger.CLOBScale,
		MarketID: MarketCodeETHUSDC, Opcode: OpSubmitOrder,
		Side: SideCodeSell, OrderType: OrderTypeCodeLimit, TimeInForce: TIFCodeIOC,
	})
	if err != ErrCircuitBreakerTripped || rep.Status != ExecStatusCBRejected {
		t.Fatalf("expected ErrCircuitBreakerTripped, got rep=%+v err=%v", rep, err)
	}

	// Cancels still work when circuit breaker is tripped!
	rep, err = b.Submit(OrderCmd{
		OrderID: 4, MarketID: MarketCodeETHUSDC, Opcode: OpCancelOrder,
	})
	if err != nil || rep.Status != ExecStatusCanceled {
		t.Fatalf("expected cancel to succeed during CB halt, got rep=%+v err=%v", rep, err)
	}
}

func TestWALReplayAndSnapshotDeterminism(t *testing.T) {
	tmp := t.TempDir()
	walDir := filepath.Join(tmp, "wal")
	snapDir := filepath.Join(tmp, "snap")

	b, err := NewBridge(EngineConfig{
		WALDir:            walDir,
		SnapshotDir:       snapDir,
		WALFsyncMode:      "fsync",
		CircuitBreakerBps: 1000,
	}, metrics.NewRegistry())
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}

	u1Low, u1High := UUIDToUint64Pair(uuid.New())
	u2Low, u2High := UUIDToUint64Pair(uuid.New())

	_, _ = b.Submit(OrderCmd{
		OrderID: 10, AccountIDLow: u1Low, AccountIDHigh: u1High,
		Price1e8: 3000 * ledger.CLOBScale, Qty1e8: 2 * ledger.CLOBScale,
		MarketID: MarketCodeETHUSDC, Opcode: OpSubmitOrder,
		Side: SideCodeBuy, OrderType: OrderTypeCodeLimit, TimeInForce: TIFCodeGTC,
	})
	if _, err := b.SaveSnapshot(snapDir); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	_, _ = b.Submit(OrderCmd{
		OrderID: 11, AccountIDLow: u2Low, AccountIDHigh: u2High,
		Price1e8: 3200 * ledger.CLOBScale, Qty1e8: 1 * ledger.CLOBScale,
		MarketID: MarketCodeETHUSDC, Opcode: OpSubmitOrder,
		Side: SideCodeSell, OrderType: OrderTypeCodeLimit, TimeInForce: TIFCodeGTC,
	})
	_, _ = b.Submit(OrderCmd{
		OrderID: 12, AccountIDLow: u1Low, AccountIDHigh: u1High,
		Price1e8: 2990 * ledger.CLOBScale, Qty1e8: 3 * ledger.CLOBScale,
		MarketID: MarketCodeETHUSDC, Opcode: OpSubmitOrder,
		Side: SideCodeBuy, OrderType: OrderTypeCodeLimit, TimeInForce: TIFCodeGTC,
	})

	liveHash := b.StateHash()
	_ = b.Close()

	replayed, count, err := ReplayFromWAL(filepath.Join(walDir, "engine.wal"), snapDir, EngineConfig{})
	if err != nil {
		t.Fatalf("ReplayFromWAL: %v", err)
	}
	defer func() { _ = replayed.Close() }()

	if count != 2 {
		t.Fatalf("expected 2 tail WAL records replayed after snapshot, got %d", count)
	}
	if replayed.StateHash() != liveHash {
		t.Fatalf("replay SHA-256 mismatch: got %x want %x", replayed.StateHash(), liveHash)
	}
}
