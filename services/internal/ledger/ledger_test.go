package ledger

import (
	"context"
	"errors"
	"math/big"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

func TestDecimalFixedPointConversions(t *testing.T) {
	// ETH: 1.5 ETH -> 150_000_000 ticks -> 1.5e18 wei
	ethTicks, err := HumanToCLOBInt64(AssetETH, decimal.RequireFromString("1.5"))
	if err != nil || ethTicks != 150_000_000 {
		t.Fatalf("expected 150000000 ticks, got %d err=%v", ethTicks, err)
	}
	ethWei, err := CLOBInt64ToWei(AssetETH, ethTicks)
	if err != nil || ethWei.String() != "1500000000000000000" {
		t.Fatalf("unexpected eth wei %v err=%v", ethWei, err)
	}

	// Sub-satoshi ETH dust preservation on WeiToCLOBInt64
	rawWei, _ := new(big.Int).SetString("1500000000000000123", 10)
	ticks, dust, err := WeiToCLOBInt64(AssetETH, rawWei)
	if err != nil || ticks != 150_000_000 || dust.String() != "123" {
		t.Fatalf("unexpected WeiToCLOBInt64: ticks=%d dust=%v err=%v", ticks, dust, err)
	}

	// USDC: 6-decimal lot step (100 ticks) enforced
	if _, err := HumanToCLOBInt64(AssetUSDC, decimal.RequireFromString("3125.123456")); err != nil {
		t.Fatalf("valid 6-decimal USDC rejected: %v", err)
	}
	if _, err := HumanToCLOBInt64(AssetUSDC, decimal.RequireFromString("3125.1234567")); err == nil {
		t.Fatalf("expected 7-decimal USDC to fail 6-decimal lot step")
	}

	// Hard ingress bounds [1, 10^15]
	if _, err := HumanToCLOBInt64(AssetWBTC, decimal.RequireFromString("10000001")); err == nil {
		t.Fatalf("expected > 10^15 ticks to fail")
	}
	if _, err := HumanToCLOBInt64(AssetWBTC, decimal.Zero); err == nil {
		t.Fatalf("expected zero to fail")
	}

	// Notional & ceiling fee without int64 overflow (e.g. 10^13 * 10^13 / 10^8)
	notional, err := ComputeQuoteNotional1e8(3100*CLOBScale, 2*CLOBScale)
	if err != nil || notional != 6200*CLOBScale {
		t.Fatalf("unexpected notional %d err=%v", notional, err)
	}
	fee, err := ComputeFeeCeil1e8(notional, 10) // 10 bps of 6200 = 6.20
	if err != nil || fee != 620_000_000 {
		t.Fatalf("unexpected fee %d err=%v", fee, err)
	}
}

func assertJournalBalanced(t *testing.T, store Store, txID uuid.UUID) {
	t.Helper()
	entries, err := store.GetLedgerEntriesByTx(context.Background(), txID)
	if err != nil {
		t.Fatalf("GetLedgerEntriesByTx: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected >= 2 legs in journal tx %s, got %d", txID, len(entries))
	}
	nets := make(map[string]decimal.Decimal)
	for _, e := range entries {
		if e.Direction == DirectionCredit {
			nets[e.Asset] = nets[e.Asset].Add(e.Amount)
		} else {
			nets[e.Asset] = nets[e.Asset].Sub(e.Amount)
		}
	}
	for asset, net := range nets {
		if !net.IsZero() {
			t.Fatalf("journal tx %s unbalanced for %s: net=%s", txID, asset, net)
		}
	}
}

func TestTwoPhaseOrderAndDoubleEntryConservation(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	buyer := uuid.New()
	seller := uuid.New()

	depTx1, err := store.CreditDeposit(ctx, buyer, AssetUSDC, decimal.NewFromInt(10000), "dep:1")
	if err != nil {
		t.Fatalf("CreditDeposit buyer: %v", err)
	}
	assertJournalBalanced(t, store, depTx1)

	depTx2, err := store.CreditDeposit(ctx, seller, AssetETH, decimal.NewFromInt(5), "dep:2")
	if err != nil {
		t.Fatalf("CreditDeposit seller: %v", err)
	}
	assertJournalBalanced(t, store, depTx2)

	// Seller places ASK: 2 ETH @ 3095 USDC
	sellQty := decimal.NewFromInt(2)
	sellPx := decimal.NewFromInt(3095)
	_, sellRes, err := ComputeOrderReserveAmount(MarketETHUSDC, SideSell, sellPx, sellQty, 10, 2)
	if err != nil {
		t.Fatalf("ComputeOrderReserveAmount sell: %v", err)
	}
	sellOrd, _, err := store.ReserveOrder(ctx, ReserveOrderReq{
		IdempotencyKey: uuid.New(),
		UserID:         seller,
		MarketID:       MarketETHUSDC,
		Side:           SideSell,
		OrderType:      OrderTypeLimit,
		TimeInForce:    TIFGTC,
		Price:          sellPx,
		Quantity:       sellQty,
		ReserveAmount:  sellRes,
	})
	if err != nil {
		t.Fatalf("ReserveOrder sell: %v", err)
	}

	// Buyer places BID: 2 ETH @ 3100 USDC (reserves 6200 + 6.20 fee = 6206.20 USDC)
	buyIdem := uuid.New()
	buyQty := decimal.NewFromInt(2)
	buyPx := decimal.NewFromInt(3100)
	_, buyRes, err := ComputeOrderReserveAmount(MarketETHUSDC, SideBuy, buyPx, buyQty, 10, 2)
	if err != nil {
		t.Fatalf("ComputeOrderReserveAmount buy: %v", err)
	}
	buyOrd, hit, err := store.ReserveOrder(ctx, ReserveOrderReq{
		IdempotencyKey: buyIdem,
		UserID:         buyer,
		MarketID:       MarketETHUSDC,
		Side:           SideBuy,
		OrderType:      OrderTypeLimit,
		TimeInForce:    TIFGTC,
		Price:          buyPx,
		Quantity:       buyQty,
		ReserveAmount:  buyRes,
	})
	if err != nil || hit {
		t.Fatalf("ReserveOrder buy: err=%v hit=%v", err, hit)
	}

	// Idempotency replay returns same order; conflict with different price returns ErrIdempotencyConflict.
	buyOrd2, hit2, err := store.ReserveOrder(ctx, ReserveOrderReq{
		IdempotencyKey: buyIdem,
		UserID:         buyer,
		MarketID:       MarketETHUSDC,
		Side:           SideBuy,
		OrderType:      OrderTypeLimit,
		TimeInForce:    TIFGTC,
		Price:          buyPx,
		Quantity:       buyQty,
		ReserveAmount:  buyRes,
	})
	if err != nil || !hit2 || buyOrd2.OrderID != buyOrd.OrderID {
		t.Fatalf("idempotency replay failed: err=%v hit=%v", err, hit2)
	}
	if _, _, err := store.ReserveOrder(ctx, ReserveOrderReq{
		IdempotencyKey: buyIdem,
		UserID:         buyer,
		MarketID:       MarketETHUSDC,
		Side:           SideBuy,
		OrderType:      OrderTypeLimit,
		TimeInForce:    TIFGTC,
		Price:          decimal.NewFromInt(3101),
		Quantity:       buyQty,
		ReserveAmount:  buyRes,
	}); err != ErrIdempotencyConflict {
		t.Fatalf("expected ErrIdempotencyConflict, got %v", err)
	}

	// Batch settlement at 3095 USDC (buyer gets price improvement refund!)
	notional := decimal.NewFromInt(6190) // 2 * 3095
	makerFee := decimal.RequireFromString("1.238")
	takerFee := decimal.RequireFromString("6.19")
	applied, err := store.ApplySettlementBatch(ctx, SettlementBatch{
		MarketID:  MarketETHUSDC,
		MaxWalSeq: 10,
		Fills: []SettlementFill{
			{
				TradeID:       1,
				WalSeq:        10,
				MarketID:      MarketETHUSDC,
				MakerOrderID:  sellOrd.OrderID,
				TakerOrderID:  buyOrd.OrderID,
				MakerUserID:   seller,
				TakerUserID:   buyer,
				TakerSide:     SideBuy,
				ExecPrice:     sellPx,
				FillQty:       buyQty,
				QuoteNotional: notional,
				MakerFee:      makerFee,
				TakerFee:      takerFee,
				MakerTerminal: true,
				TakerTerminal: true,
			},
		},
	})
	if err != nil || applied != 1 {
		t.Fatalf("ApplySettlementBatch: applied=%d err=%v", applied, err)
	}

	// Check buyer balances: ETH = 2 available, USDC locked = 0, USDC available = 10000 - 6190 - 6.19 = 3803.81
	buyerBals, _ := store.ListUserBalances(ctx, buyer)
	for _, b := range buyerBals {
		if b.Asset == AssetETH && !b.Available.Equal(decimal.NewFromInt(2)) {
			t.Fatalf("expected buyer 2 ETH, got %s", b.Available)
		}
		if b.Asset == AssetUSDC {
			if !b.Locked.IsZero() {
				t.Fatalf("expected buyer 0 locked USDC after price-improvement refund, got %s", b.Locked)
			}
			want := decimal.RequireFromString("3803.81")
			if !b.Available.Equal(want) {
				t.Fatalf("expected buyer %s USDC available, got %s", want, b.Available)
			}
		}
	}

	// Global solvency conservation: sum(user liabilities + fee treasury) == deposited totals (10000 USDC, 5 ETH)
	totals, _, err := store.SnapshotLiabilities(ctx)
	if err != nil {
		t.Fatalf("SnapshotLiabilities: %v", err)
	}
	if !totals[AssetUSDC].UserTotal.Add(totals[AssetUSDC].FeeTotal).Equal(decimal.NewFromInt(10000)) {
		t.Fatalf("USDC conservation violated: user=%s fee=%s", totals[AssetUSDC].UserTotal, totals[AssetUSDC].FeeTotal)
	}
	if !totals[AssetETH].UserTotal.Add(totals[AssetETH].FeeTotal).Equal(decimal.NewFromInt(5)) {
		t.Fatalf("ETH conservation violated: user=%s fee=%s", totals[AssetETH].UserTotal, totals[AssetETH].FeeTotal)
	}
}

func TestMemoryStoreRejectsUnbalancedJournal(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	u := uuid.New()
	_ = store.EnsureUserAccounts(ctx, u)
	accID := DeterministicAccountID(u, AssetUSDC, AccountTypeUser)

	// Unbalanced 2-leg transaction (100 DEBIT vs 90 CREDIT) must fail with ErrUnbalancedJournal.
	tx1 := uuid.New()
	err := store.PostJournal(ctx, tx1, "unbalanced:1", []JournalLeg{
		{AccountID: accID, Asset: AssetUSDC, EntryType: EntryTypeDeposit, Direction: DirectionDebit, Amount: decimal.NewFromInt(100), BalanceAfter: decimal.Zero},
		{AccountID: accID, Asset: AssetUSDC, EntryType: EntryTypeDeposit, Direction: DirectionCredit, Amount: decimal.NewFromInt(90), BalanceAfter: decimal.Zero},
	})
	if !errors.Is(err, ErrUnbalancedJournal) {
		t.Fatalf("expected ErrUnbalancedJournal for unbalanced 2-leg tx, got %v", err)
	}
	if entries, _ := store.GetLedgerEntriesByTx(ctx, tx1); len(entries) != 0 {
		t.Fatalf("expected 0 committed entries on rejected tx, got %d", len(entries))
	}

	// Single-leg transaction must fail with ErrUnbalancedJournal.
	tx2 := uuid.New()
	err = store.PostJournal(ctx, tx2, "unbalanced:2", []JournalLeg{
		{AccountID: accID, Asset: AssetUSDC, EntryType: EntryTypeDeposit, Direction: DirectionCredit, Amount: decimal.NewFromInt(50), BalanceAfter: decimal.NewFromInt(50)},
	})
	if !errors.Is(err, ErrUnbalancedJournal) {
		t.Fatalf("expected ErrUnbalancedJournal for single-leg tx, got %v", err)
	}

	// Cross-asset unbalanced transaction must fail with ErrUnbalancedJournal.
	tx3 := uuid.New()
	err = store.PostJournal(ctx, tx3, "unbalanced:3", []JournalLeg{
		{AccountID: accID, Asset: AssetUSDC, EntryType: EntryTypeDeposit, Direction: DirectionDebit, Amount: decimal.NewFromInt(50), BalanceAfter: decimal.Zero},
		{AccountID: accID, Asset: AssetETH, EntryType: EntryTypeDeposit, Direction: DirectionCredit, Amount: decimal.NewFromInt(50), BalanceAfter: decimal.NewFromInt(50)},
	})
	if !errors.Is(err, ErrUnbalancedJournal) {
		t.Fatalf("expected ErrUnbalancedJournal for cross-asset unbalanced tx, got %v", err)
	}
}

// RunStoreConformanceSuite exercises the full transactional Store contract identically
// across MemoryStore and PostgresStore implementations.
func RunStoreConformanceSuite(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	buyer := uuid.New()
	seller := uuid.New()
	if err := store.EnsureUserAccounts(ctx, buyer); err != nil {
		t.Fatalf("EnsureUserAccounts buyer: %v", err)
	}
	if err := store.EnsureUserAccounts(ctx, seller); err != nil {
		t.Fatalf("EnsureUserAccounts seller: %v", err)
	}

	// 1. Initial zero balances across all 4 custody assets.
	bals, err := store.ListUserBalances(ctx, buyer)
	if err != nil {
		t.Fatalf("ListUserBalances: %v", err)
	}
	if len(bals) != len(AllAssets) {
		t.Fatalf("expected %d asset accounts, got %d", len(AllAssets), len(bals))
	}
	for _, b := range bals {
		if !b.Available.IsZero() || !b.Locked.IsZero() {
			t.Fatalf("expected zero initial balance for %s, got avail=%s locked=%s", b.Asset, b.Available, b.Locked)
		}
	}

	// 2. Deposits + double-entry journal verification.
	depTx1, err := store.CreditDeposit(ctx, buyer, AssetUSDC, decimal.NewFromInt(12000), "conf:dep:buyer")
	if err != nil {
		t.Fatalf("CreditDeposit buyer: %v", err)
	}
	assertJournalBalanced(t, store, depTx1)

	depTx2, err := store.CreditDeposit(ctx, seller, AssetETH, decimal.NewFromInt(4), "conf:dep:seller")
	if err != nil {
		t.Fatalf("CreditDeposit seller: %v", err)
	}
	assertJournalBalanced(t, store, depTx2)

	// 3. Reorg deposit reversal + underflow guard.
	revTx, err := store.ReverseDeposit(ctx, buyer, AssetUSDC, decimal.NewFromInt(2000), "conf:reorg:buyer")
	if err != nil {
		t.Fatalf("ReverseDeposit: %v", err)
	}
	assertJournalBalanced(t, store, revTx)
	if _, err := store.ReverseDeposit(ctx, buyer, AssetUSDC, decimal.NewFromInt(50000), "conf:reorg:underflow"); !errors.Is(err, ErrReorgUnderflow) {
		t.Fatalf("expected ErrReorgUnderflow, got %v", err)
	}

	// 4. Pre-trade ReserveOrder + idempotency hit + conflict + insufficient balance.
	_, sellRes, _ := ComputeOrderReserveAmount(MarketETHUSDC, SideSell, decimal.NewFromInt(3000), decimal.NewFromInt(2), 10, 2)
	sellOrd, hit, err := store.ReserveOrder(ctx, ReserveOrderReq{
		IdempotencyKey: uuid.New(),
		UserID:         seller,
		MarketID:       MarketETHUSDC,
		Side:           SideSell,
		OrderType:      OrderTypeLimit,
		TimeInForce:    TIFGTC,
		Price:          decimal.NewFromInt(3000),
		Quantity:       decimal.NewFromInt(2),
		ReserveAmount:  sellRes,
	})
	if err != nil || hit || sellOrd.Status != OrderStatusReserved {
		t.Fatalf("ReserveOrder sell failed: ord=%+v hit=%v err=%v", sellOrd, hit, err)
	}

	buyIdem := uuid.New()
	_, buyRes, _ := ComputeOrderReserveAmount(MarketETHUSDC, SideBuy, decimal.NewFromInt(3100), decimal.NewFromInt(1), 10, 2)
	buyOrd, hit, err := store.ReserveOrder(ctx, ReserveOrderReq{
		IdempotencyKey: buyIdem,
		UserID:         buyer,
		MarketID:       MarketETHUSDC,
		Side:           SideBuy,
		OrderType:      OrderTypeLimit,
		TimeInForce:    TIFGTC,
		Price:          decimal.NewFromInt(3100),
		Quantity:       decimal.NewFromInt(1),
		ReserveAmount:  buyRes,
	})
	if err != nil || hit {
		t.Fatalf("ReserveOrder buy failed: hit=%v err=%v", hit, err)
	}

	// Idempotent replay
	buyOrdReplay, hitReplay, err := store.ReserveOrder(ctx, ReserveOrderReq{
		IdempotencyKey: buyIdem,
		UserID:         buyer,
		MarketID:       MarketETHUSDC,
		Side:           SideBuy,
		OrderType:      OrderTypeLimit,
		TimeInForce:    TIFGTC,
		Price:          decimal.NewFromInt(3100),
		Quantity:       decimal.NewFromInt(1),
		ReserveAmount:  buyRes,
	})
	if err != nil || !hitReplay || buyOrdReplay.OrderID != buyOrd.OrderID {
		t.Fatalf("idempotency replay failed: hit=%v err=%v", hitReplay, err)
	}

	// Idempotency conflict
	if _, _, err := store.ReserveOrder(ctx, ReserveOrderReq{
		IdempotencyKey: buyIdem,
		UserID:         buyer,
		MarketID:       MarketETHUSDC,
		Side:           SideBuy,
		OrderType:      OrderTypeLimit,
		TimeInForce:    TIFGTC,
		Price:          decimal.NewFromInt(3105),
		Quantity:       decimal.NewFromInt(1),
		ReserveAmount:  buyRes,
	}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict, got %v", err)
	}

	// Insufficient balance
	if _, _, err := store.ReserveOrder(ctx, ReserveOrderReq{
		IdempotencyKey: uuid.New(),
		UserID:         buyer,
		MarketID:       MarketETHUSDC,
		Side:           SideBuy,
		OrderType:      OrderTypeLimit,
		TimeInForce:    TIFGTC,
		Price:          decimal.NewFromInt(3100),
		Quantity:       decimal.NewFromInt(100),
		ReserveAmount:  decimal.NewFromInt(310000),
	}); !errors.Is(err, ErrInsufficientAvailable) {
		t.Fatalf("expected ErrInsufficientAvailable, got %v", err)
	}

	// Reserved order list includes both orders before WAL assignment, and ReleaseOrderReservation aborts cleanly.
	orphanOrd, _, err := store.ReserveOrder(ctx, ReserveOrderReq{
		IdempotencyKey: uuid.New(),
		UserID:         buyer,
		MarketID:       MarketETHUSDC,
		Side:           SideBuy,
		OrderType:      OrderTypeLimit,
		TimeInForce:    TIFGTC,
		Price:          decimal.NewFromInt(100),
		Quantity:       decimal.NewFromInt(1),
		ReserveAmount:  decimal.RequireFromString("100.1"),
	})
	if err != nil {
		t.Fatalf("ReserveOrder orphan: %v", err)
	}
	reserved, err := store.ListReservedOrders(ctx)
	if err != nil || len(reserved) < 3 {
		t.Fatalf("expected >= 3 reserved orders, got %d err=%v", len(reserved), err)
	}
	releasedAmt, err := store.ReleaseOrderReservation(ctx, orphanOrd.OrderID, 0, OrderStatusAbortedUnsubmitted)
	if err != nil || !releasedAmt.Equal(decimal.RequireFromString("100.1")) {
		t.Fatalf("ReleaseOrderReservation: amt=%s err=%v", releasedAmt, err)
	}
	gotOrphan, err := store.GetOrder(ctx, orphanOrd.OrderID)
	if err != nil || gotOrphan.Status != OrderStatusAbortedUnsubmitted || !gotOrphan.LockedAmount.IsZero() {
		t.Fatalf("unexpected aborted order state: %+v err=%v", gotOrphan, err)
	}

	// 5. Batch settlement + checkpoint idempotency.
	if err := store.UpdateOrderStatus(ctx, sellOrd.OrderID, 1, OrderStatusOpen); err != nil {
		t.Fatalf("UpdateOrderStatus: %v", err)
	}
	batch := SettlementBatch{
		MarketID:  MarketETHUSDC,
		MaxWalSeq: 2,
		Fills: []SettlementFill{
			{
				TradeID:       101,
				WalSeq:        2,
				MarketID:      MarketETHUSDC,
				MakerOrderID:  sellOrd.OrderID,
				TakerOrderID:  buyOrd.OrderID,
				MakerUserID:   seller,
				TakerUserID:   buyer,
				TakerSide:     SideBuy,
				ExecPrice:     decimal.NewFromInt(3000),
				FillQty:       decimal.NewFromInt(1),
				QuoteNotional: decimal.NewFromInt(3000),
				MakerFee:      decimal.RequireFromString("0.6"),
				TakerFee:      decimal.RequireFromString("3"),
				MakerTerminal: false,
				TakerTerminal: true,
			},
		},
	}
	applied, err := store.ApplySettlementBatch(ctx, batch)
	if err != nil || applied != 1 {
		t.Fatalf("ApplySettlementBatch: applied=%d err=%v", applied, err)
	}
	if seq, err := store.GetLastSettledWalSeq(ctx, MarketETHUSDC); err != nil || seq != 2 {
		t.Fatalf("GetLastSettledWalSeq: seq=%d err=%v", seq, err)
	}
	// Replaying the same batch must be an idempotent no-op.
	appliedAgain, err := store.ApplySettlementBatch(ctx, batch)
	if err != nil || appliedAgain != 0 {
		t.Fatalf("expected idempotent batch skip (0 applied), got %d err=%v", appliedAgain, err)
	}

	// Cancel remaining 1 ETH on maker order via batch cancel.
	appliedCancel, err := store.ApplySettlementBatch(ctx, SettlementBatch{
		MarketID:  MarketETHUSDC,
		MaxWalSeq: 3,
		Cancels: []SettlementCancel{
			{
				OrderID:   sellOrd.OrderID,
				WalSeq:    3,
				MarketID:  MarketETHUSDC,
				UserID:    seller,
				NewStatus: OrderStatusCanceled,
			},
		},
	})
	if err != nil || appliedCancel != 1 {
		t.Fatalf("ApplySettlementBatch cancel: applied=%d err=%v", appliedCancel, err)
	}

	// 6. Withdrawal lifecycle (Queue + Finalize + Refund).
	w1ID := uuid.New()
	if _, _, err := store.QueueWithdrawal(ctx, WithdrawalRequest{
		WithdrawalID:     w1ID,
		IdempotencyKey:   uuid.New(),
		UserID:           buyer,
		RecipientAddress: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		Asset:            AssetUSDC,
		HumanAmount:      decimal.NewFromInt(1000),
		RawAmountWei:     big.NewInt(1_000_000_000),
		SigningTier:      "HOT",
		VoucherNonce:     1,
	}); err != nil {
		t.Fatalf("QueueWithdrawal w1: %v", err)
	}
	if err := store.FinalizeWithdrawal(ctx, w1ID, buyer, AssetUSDC, decimal.NewFromInt(1000)); err != nil {
		t.Fatalf("FinalizeWithdrawal: %v", err)
	}

	w2ID := uuid.New()
	if _, _, err := store.QueueWithdrawal(ctx, WithdrawalRequest{
		WithdrawalID:     w2ID,
		IdempotencyKey:   uuid.New(),
		UserID:           seller,
		RecipientAddress: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		Asset:            AssetETH,
		HumanAmount:      decimal.NewFromInt(1),
		RawAmountWei:     new(big.Int).Mul(big.NewInt(1), big.NewInt(1_000_000_000_000_000_000)),
		SigningTier:      "HOT",
		VoucherNonce:     2,
	}); err != nil {
		t.Fatalf("QueueWithdrawal w2: %v", err)
	}
	if err := store.RefundFailedWithdrawal(ctx, w2ID, seller, AssetETH, decimal.NewFromInt(1)); err != nil {
		t.Fatalf("RefundFailedWithdrawal: %v", err)
	}

	// 7. Global liabilities conservation:
	// Net USDC = 12000 - 2000 (reorg) - 1000 (withdrawal) = 9000
	// Net ETH = 4
	totals, _, err := store.SnapshotLiabilities(ctx)
	if err != nil {
		t.Fatalf("SnapshotLiabilities: %v", err)
	}
	if !totals[AssetUSDC].UserTotal.Add(totals[AssetUSDC].FeeTotal).Equal(decimal.NewFromInt(9000)) {
		t.Fatalf("conformance USDC conservation violated: user=%s fee=%s", totals[AssetUSDC].UserTotal, totals[AssetUSDC].FeeTotal)
	}
	if !totals[AssetETH].UserTotal.Add(totals[AssetETH].FeeTotal).Equal(decimal.NewFromInt(4)) {
		t.Fatalf("conformance ETH conservation violated: user=%s fee=%s", totals[AssetETH].UserTotal, totals[AssetETH].FeeTotal)
	}
}

func TestMemoryStoreConformance(t *testing.T) {
	store := NewMemoryStore()
	RunStoreConformanceSuite(t, store)
	if err := store.VerifyAllJournalsBalanced(); err != nil {
		t.Fatalf("VerifyAllJournalsBalanced: %v", err)
	}
}

func TestPostgresStoreConformance(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live PostgresStore conformance suite")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()
	RunStoreConformanceSuite(t, NewPostgresStore(pool))
}
