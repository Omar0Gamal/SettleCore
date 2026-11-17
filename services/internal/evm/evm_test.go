package evm

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/metrics"
	"github.com/shopspring/decimal"
)

type mockChainProvider struct {
	mu       sync.Mutex
	name     string
	failAll  bool
	blocks   map[int64]BlockHeader
	logs     map[int64][]DepositLog
	head     int64
	baseFee  *big.Int
	tip      *big.Int
	nonce    uint64
	receipts map[string]TxReceipt
	txCount  int
	lastSub  *TxSubmission
}

func newMockChain(name string) *mockChainProvider {
	return &mockChainProvider{
		name:     name,
		blocks:   make(map[int64]BlockHeader),
		logs:     make(map[int64][]DepositLog),
		baseFee:  big.NewInt(10_000_000_000),
		tip:      big.NewInt(1_500_000_000),
		receipts: make(map[string]TxReceipt),
	}
}

func (m *mockChainProvider) Name() string { return m.name }

func (m *mockChainProvider) HeadNumber(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failAll {
		return 0, errors.New("rpc down")
	}
	return m.head, nil
}

func (m *mockChainProvider) BlockByNumber(_ context.Context, num int64) (BlockHeader, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failAll {
		return BlockHeader{}, errors.New("rpc down")
	}
	b, ok := m.blocks[num]
	if !ok {
		return BlockHeader{}, ErrBlockNotFound
	}
	return b, nil
}

func (m *mockChainProvider) DepositLogs(_ context.Context, num int64) ([]DepositLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failAll {
		return nil, errors.New("rpc down")
	}
	return m.logs[num], nil
}

func (m *mockChainProvider) SuggestGasFees(_ context.Context) (*big.Int, *big.Int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failAll {
		return nil, nil, errors.New("rpc down")
	}
	return new(big.Int).Set(m.baseFee), new(big.Int).Set(m.tip), nil
}

func (m *mockChainProvider) PendingNonce(_ context.Context, _ common.Address) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nonce, nil
}

func (m *mockChainProvider) BroadcastTx(_ context.Context, sub TxSubmission) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failAll {
		return "", errors.New("rpc down")
	}
	m.txCount++
	cp := sub
	m.lastSub = &cp
	return fmt.Sprintf("0xtx_%d_nonce_%d", m.txCount, sub.Nonce), nil
}

func (m *mockChainProvider) TxReceipt(_ context.Context, txHash string) (TxReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.receipts[txHash]; ok {
		return r, nil
	}
	return TxReceipt{TxHash: txHash, Confirmed: false}, nil
}

func (m *mockChainProvider) VaultBalance(_ context.Context, _ string) (*big.Int, error) {
	return big.NewInt(0), nil
}

func TestRPCPoolFailoverAnd3BlockReorgRollback(t *testing.T) {
	ctx := context.Background()
	reg := metrics.NewRegistry()
	p1 := newMockChain("rpc-proxy-1")
	p2 := newMockChain("rpc-proxy-2")

	// Shared chain blocks 0..103 (deposit of 10,000 USDC at block 101)
	userID := uuid.New()
	setupBlocks := func(m *mockChainProvider) {
		m.blocks[0] = BlockHeader{Number: 0, Hash: "0x00", ParentHash: "0x00"}
		m.blocks[100] = BlockHeader{Number: 100, Hash: "0xA0", ParentHash: "0x99"}
		m.blocks[101] = BlockHeader{Number: 101, Hash: "0xA1", ParentHash: "0xA0"}
		m.blocks[102] = BlockHeader{Number: 102, Hash: "0xA2", ParentHash: "0xA1"}
		m.blocks[103] = BlockHeader{Number: 103, Hash: "0xA3", ParentHash: "0xA2"}
		m.head = 103
		m.logs[101] = []DepositLog{{
			TxHash:       "0xdep1",
			LogIndex:     0,
			BlockNumber:  101,
			BlockHash:    "0xA1",
			Depositor:    "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
			UserID:       userID,
			Asset:        ledger.AssetUSDC,
			RawAmountWei: big.NewInt(10_000_000_000), // 10,000 USDC
		}}
	}
	setupBlocks(p1)
	setupBlocks(p2)

	pool := NewRPCPool([]ChainProvider{p1, p2}, reg)
	idxStore := NewMemoryIndexStore()
	ledgerStore := ledger.NewMemoryStore()

	// Seed block 100 as starting canonical block
	_ = idxStore.InsertCanonicalBlock(ctx, p1.blocks[100])
	w := NewWatcher(pool, idxStore, ledgerStore, 3, 64, reg)

	// Poll 1: indexes 101..103 -> 3 confirmations reached -> credits 10,000 USDC
	if err := w.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce initial: %v", err)
	}
	bals, _ := ledgerStore.ListUserBalances(ctx, userID)
	for _, b := range bals {
		if b.Asset == ledger.AssetUSDC && !b.Available.Equal(decimal.NewFromInt(10000)) {
			t.Fatalf("expected 10000 USDC credited, got %s", b.Available)
		}
	}

	// Kill primary RPC and verify failover to rpc-proxy-2 in < 500ms
	start := time.Now()
	p1.mu.Lock()
	p1.failAll = true
	p1.mu.Unlock()
	pool.Probe(ctx)
	if pool.ActiveName() != "rpc-proxy-2" || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("expected sub-500ms failover to rpc-proxy-2, got %s in %v", pool.ActiveName(), time.Since(start))
	}

	// Simulate 3-block reorg on rpc-proxy-2 replacing blocks 101..103 with 101'..104'
	p2.mu.Lock()
	p2.blocks[101] = BlockHeader{Number: 101, Hash: "0xB1", ParentHash: "0xA0"}
	p2.blocks[102] = BlockHeader{Number: 102, Hash: "0xB2", ParentHash: "0xB1"}
	p2.blocks[103] = BlockHeader{Number: 103, Hash: "0xB3", ParentHash: "0xB2"}
	p2.blocks[104] = BlockHeader{Number: 104, Hash: "0xB4", ParentHash: "0xB3"}
	p2.logs[101] = nil
	p2.head = 104
	p2.mu.Unlock()

	if err := w.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce reorg: %v", err)
	}

	// Verify deposit reversed back to 0 USDC, deposit marked ORPHANED, reorg_rollbacks_total == 1
	bals, _ = ledgerStore.ListUserBalances(ctx, userID)
	for _, b := range bals {
		if b.Asset == ledger.AssetUSDC && !b.Available.IsZero() {
			t.Fatalf("expected 0 USDC after reorg reversal, got %s", b.Available)
		}
	}
	deps, _ := idxStore.ListRecentDeposits(ctx, userID, 10)
	if len(deps) != 1 || deps[0].Status != DepositOrphaned {
		t.Fatalf("expected deposit ORPHANED, got %+v", deps)
	}
	if reg.Snapshot().ReorgRollbacksTotal != 1 {
		t.Fatalf("expected reorg_rollbacks_total=1, got %d", reg.Snapshot().ReorgRollbacksTotal)
	}
	if !reg.Snapshot().ReservesInvariantOK {
		t.Fatalf("expected reserves_invariant_ok=true")
	}
}

func TestEIP712SigningAndGasBumper(t *testing.T) {
	ctx := context.Background()
	reg := metrics.NewRegistry()
	chain := newMockChain("rpc-proxy-1")
	pool := NewRPCPool([]ChainProvider{chain}, reg)

	hotKey, _ := ParseHexPrivateKey("0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d")
	coldKey, _ := ParseHexPrivateKey("0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a")
	relKey, _ := ParseHexPrivateKey("0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6")

	vaultAddr := common.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	signer := NewEIP712Signer(31337, vaultAddr)
	voucher := WithdrawalVoucher{
		WithdrawalID: [32]byte{1, 2, 3},
		Recipient:    common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8"),
		Token:        common.HexToAddress("0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512"),
		Amount:       big.NewInt(5_000_000_000),
		Nonce:        big.NewInt(10042),
		Deadline:     big.NewInt(2_000_000_000),
	}
	sig, err := signer.SignVoucher(hotKey, voucher)
	if err != nil {
		t.Fatalf("SignVoucher: %v", err)
	}
	recAddr, err := signer.RecoverVoucherSigner(voucher, sig)
	if err != nil || recAddr != crypto.PubkeyToAddress(hotKey.PublicKey) {
		t.Fatalf("EIP-712 signer recovery mismatch: got %s err=%v", recAddr, err)
	}

	// Tier classification check
	if tier := ClassifyTier(big.NewInt(15_000_000_000), big.NewInt(10_000_000_000), big.NewInt(100_000_000_000), big.NewInt(0)); tier != SigningTierColdCosigned {
		t.Fatalf("expected COLD_COSIGNED above per-tx cap, got %s", tier)
	}

	// Outbox + gas bumping test
	ledgerStore := ledger.NewMemoryStore()
	outbox := NewMemoryOutboxStore()
	userID := uuid.New()
	_, _ = ledgerStore.CreditDeposit(ctx, userID, ledger.AssetUSDC, decimal.NewFromInt(50000), "dep:w")
	wRec, _, err := ledgerStore.QueueWithdrawal(ctx, ledger.WithdrawalRequest{
		WithdrawalID:     uuid.New(),
		IdempotencyKey:   uuid.New(),
		UserID:           userID,
		RecipientAddress: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		Asset:            ledger.AssetUSDC,
		HumanAmount:      decimal.NewFromInt(15000),
		RawAmountWei:     big.NewInt(15_000_000_000),
		SigningTier:      SigningTierColdCosigned,
		VoucherNonce:     10042,
	})
	if err != nil {
		t.Fatalf("QueueWithdrawal: %v", err)
	}
	outbox.Enqueue(*wRec)

	relayer := NewRelayer(RelayerConfig{
		ChainID:      31337,
		VaultAddress: vaultAddr,
		TokenAddrs: map[string]common.Address{
			ledger.AssetUSDC: common.HexToAddress("0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512"),
		},
		HotKey:         hotKey,
		ColdKey:        coldKey,
		RelayerKey:     relKey,
		StuckThreshold: 1 * time.Millisecond,
	}, pool, outbox, ledgerStore, reg)

	if n, err := relayer.ProcessQueuedOnce(ctx); err != nil || n != 1 {
		t.Fatalf("ProcessQueuedOnce: n=%d err=%v", n, err)
	}
	time.Sleep(5 * time.Millisecond)

	// 10x base fee spike -> BumpStuckOnce must replace at SAME EOA nonce=0 with +15% / 2*baseFee
	chain.mu.Lock()
	chain.baseFee = big.NewInt(150_000_000_000)
	chain.mu.Unlock()

	if bumped, err := relayer.BumpStuckOnce(ctx); err != nil || bumped != 1 {
		t.Fatalf("BumpStuckOnce: bumped=%d err=%v", bumped, err)
	}
	stored, _ := outbox.Get(wRec.WithdrawalID)
	if stored.GasBumpCount != 1 || *stored.RelayerEOANonce != 0 {
		t.Fatalf("expected gas_bump_count=1 and nonce=0, got %+v", stored)
	}
	if reg.Snapshot().StuckTxGasBumps != 1 {
		t.Fatalf("expected stuck_tx_gas_bumps=1, got %d", reg.Snapshot().StuckTxGasBumps)
	}

	// Confirm receipt -> finalizes withdrawal in ledger (locked -> 0)
	chain.mu.Lock()
	chain.receipts[stored.LastTxHash] = TxReceipt{TxHash: stored.LastTxHash, BlockNumber: 200, Confirmed: true, Succeeded: true}
	chain.mu.Unlock()

	if fin, err := relayer.PollReceiptsOnce(ctx); err != nil || fin != 1 {
		t.Fatalf("PollReceiptsOnce: fin=%d err=%v", fin, err)
	}
	bals, _ := ledgerStore.ListUserBalances(ctx, userID)
	for _, b := range bals {
		if b.Asset == ledger.AssetUSDC && (!b.Locked.IsZero() || !b.Available.Equal(decimal.NewFromInt(35000))) {
			t.Fatalf("unexpected USDC balance after withdrawal confirm: avail=%s locked=%s", b.Available, b.Locked)
		}
	}
}
