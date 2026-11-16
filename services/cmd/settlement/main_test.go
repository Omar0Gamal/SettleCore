package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/settlecore/settlecore/services/internal/config"
	"github.com/settlecore/settlecore/services/internal/engine"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/shopspring/decimal"
)

func TestSettleOnceConsumesEventLogDirectly(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	walPath := filepath.Join(tmp, "engine.wal")
	evlogPath := walPath + ".evlog"

	store := ledger.NewMemoryStore()
	maker := uuid.New()
	taker := uuid.New()
	_, _ = store.CreditDeposit(ctx, maker, ledger.AssetETH, decimal.NewFromInt(5), "d:m")
	_, _ = store.CreditDeposit(ctx, taker, ledger.AssetUSDC, decimal.NewFromInt(10000), "d:t")

	_, mRes, _ := ledger.ComputeOrderReserveAmount(ledger.MarketETHUSDC, ledger.SideSell, decimal.NewFromInt(3100), decimal.NewFromInt(2), 10, 2)
	mOrd, _, err := store.ReserveOrder(ctx, ledger.ReserveOrderReq{
		IdempotencyKey: uuid.New(), UserID: maker, MarketID: ledger.MarketETHUSDC,
		Side: ledger.SideSell, OrderType: ledger.OrderTypeLimit, TimeInForce: ledger.TIFGTC,
		Price: decimal.NewFromInt(3100), Quantity: decimal.NewFromInt(2), ReserveAmount: mRes,
	})
	if err != nil {
		t.Fatalf("ReserveOrder maker: %v", err)
	}

	_, tRes, _ := ledger.ComputeOrderReserveAmount(ledger.MarketETHUSDC, ledger.SideBuy, decimal.NewFromInt(3100), decimal.NewFromInt(2), 10, 2)
	tOrd, _, err := store.ReserveOrder(ctx, ledger.ReserveOrderReq{
		IdempotencyKey: uuid.New(), UserID: taker, MarketID: ledger.MarketETHUSDC,
		Side: ledger.SideBuy, OrderType: ledger.OrderTypeLimit, TimeInForce: ledger.TIFGTC,
		Price: decimal.NewFromInt(3100), Quantity: decimal.NewFromInt(2), ReserveAmount: tRes,
	})
	if err != nil {
		t.Fatalf("ReserveOrder taker: %v", err)
	}

	// Write ONLY the .evlog (no engine.wal) to prove settlement consumes .evlog without matching.
	if err := engine.WriteEventLog(evlogPath, []engine.WalEventRecord{
		{
			EventType:    engine.EventTypeOrderAccepted,
			MarketID:     engine.MarketCodeETHUSDC,
			Status:       engine.ExecStatusAccepted,
			Flags:        engine.EventFlagSideSell,
			WalSeq:       1,
			OrderID:      uint64(mOrd.OrderID),
			ExecPrice1e8: 3100 * ledger.CLOBScale,
			FillQty1e8:   2 * ledger.CLOBScale,
		},
		{
			EventType:    engine.EventTypeTradeFill,
			MarketID:     engine.MarketCodeETHUSDC,
			Status:       engine.ExecStatusFilled,
			Flags:        engine.EventFlagMakerTerminal | engine.EventFlagTakerTerminal,
			WalSeq:       2,
			OrderID:      uint64(tOrd.OrderID),
			MakerOrderID: uint64(mOrd.OrderID),
			TradeID:      77,
			ExecPrice1e8: 3100 * ledger.CLOBScale,
			FillQty1e8:   2 * ledger.CLOBScale,
		},
	}); err != nil {
		t.Fatalf("WriteEventLog: %v", err)
	}

	cfg := config.Load()
	if err := settleOnce(ctx, walPath, cfg, store); err != nil {
		t.Fatalf("settleOnce: %v", err)
	}

	seq, _ := store.GetLastSettledWalSeq(ctx, ledger.MarketETHUSDC)
	if seq != 2 {
		t.Fatalf("expected settled wal_seq=2, got %d", seq)
	}
	trades, _ := store.ListRecentTrades(ctx, ledger.MarketETHUSDC, 10)
	if len(trades) != 1 || trades[0].TradeID != 77 {
		t.Fatalf("expected 1 settled trade id=77, got %+v", trades)
	}
	if err := store.VerifyAllJournalsBalanced(); err != nil {
		t.Fatalf("VerifyAllJournalsBalanced: %v", err)
	}
}
