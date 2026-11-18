package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/settlecore/settlecore/services/internal/config"
	"github.com/settlecore/settlecore/services/internal/engine"
	"github.com/settlecore/settlecore/services/internal/evm"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/metrics"
	"github.com/settlecore/settlecore/services/internal/reconciler"
)

type e2eChain struct {
	mu       sync.Mutex
	name     string
	head     int64
	blocks   map[int64]evm.BlockHeader
	logs     map[int64][]evm.DepositLog
	baseFee  *big.Int
	tip      *big.Int
	receipts map[string]evm.TxReceipt
	txSeq    int
	vaultBal map[string]*big.Int
	failAll  bool
}

func newE2EChain(name string) *e2eChain {
	return &e2eChain{
		name:     name,
		blocks:   make(map[int64]evm.BlockHeader),
		logs:     make(map[int64][]evm.DepositLog),
		baseFee:  big.NewInt(10_000_000_000),
		tip:      big.NewInt(1_500_000_000),
		receipts: make(map[string]evm.TxReceipt),
		vaultBal: map[string]*big.Int{
			ledger.AssetETH:  big.NewInt(0),
			ledger.AssetUSDC: big.NewInt(0),
			ledger.AssetWBTC: big.NewInt(0),
			ledger.AssetUSDT: big.NewInt(0),
		},
	}
}

func (c *e2eChain) Name() string { return c.name }

func (c *e2eChain) HeadNumber(_ context.Context) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failAll {
		return 0, errors.New("rpc unavailable")
	}
	return c.head, nil
}

func (c *e2eChain) BlockByNumber(_ context.Context, num int64) (evm.BlockHeader, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failAll {
		return evm.BlockHeader{}, errors.New("rpc unavailable")
	}
	b, ok := c.blocks[num]
	if !ok {
		return evm.BlockHeader{}, evm.ErrBlockNotFound
	}
	return b, nil
}

func (c *e2eChain) DepositLogs(_ context.Context, num int64) ([]evm.DepositLog, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failAll {
		return nil, errors.New("rpc unavailable")
	}
	return c.logs[num], nil
}

func (c *e2eChain) SuggestGasFees(_ context.Context) (*big.Int, *big.Int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failAll {
		return nil, nil, errors.New("rpc unavailable")
	}
	return new(big.Int).Set(c.baseFee), new(big.Int).Set(c.tip), nil
}

func (c *e2eChain) PendingNonce(_ context.Context, _ common.Address) (uint64, error) {
	return 0, nil
}

func (c *e2eChain) BroadcastTx(_ context.Context, sub evm.TxSubmission) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failAll {
		return "", errors.New("rpc unavailable")
	}
	c.txSeq++
	return fmt.Sprintf("0xrelayer_%d_n%d", c.txSeq, sub.Nonce), nil
}

func (c *e2eChain) TxReceipt(_ context.Context, txHash string) (evm.TxReceipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.receipts[txHash]; ok {
		return r, nil
	}
	return evm.TxReceipt{TxHash: txHash, Confirmed: false}, nil
}

func (c *e2eChain) VaultBalance(_ context.Context, asset string) (*big.Int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if b, ok := c.vaultBal[asset]; ok {
		return new(big.Int).Set(b), nil
	}
	return big.NewInt(0), nil
}

func TestEndToEndChaosDrillsAndPrometheusMetrics(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	reg := metrics.NewRegistry()

	store := ledger.NewMemoryStore()
	eng, err := engine.NewBridge(engine.EngineConfig{
		WALDir:            filepath.Join(tmp, "wal"),
		SnapshotDir:       filepath.Join(tmp, "snap"),
		WALFsyncMode:      "fsync",
		CircuitBreakerBps: 1000,
	}, reg)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	defer func() { _ = eng.Close() }()

	primary := newE2EChain("rpc-proxy-1")
	fallback := newE2EChain("rpc-proxy-2")
	trader := ledger.DefaultTraderUserID
	maker := ledger.MarketMakerUserID

	// Seed initial canonical chain blocks 0..2 (maker 10 ETH, trader 50,000 USDC).
	seedChain := func(ch *e2eChain) {
		ch.blocks[0] = evm.BlockHeader{Number: 0, Hash: "0x00", ParentHash: "0x00"}
		ch.blocks[1] = evm.BlockHeader{Number: 1, Hash: "0x01", ParentHash: "0x00"}
		ch.blocks[2] = evm.BlockHeader{Number: 2, Hash: "0x02", ParentHash: "0x01"}
		ch.blocks[3] = evm.BlockHeader{Number: 3, Hash: "0x03", ParentHash: "0x02"}
		ch.head = 3
		ch.logs[1] = []evm.DepositLog{
			{
				TxHash:       "0xseed_maker_eth",
				LogIndex:     0,
				BlockNumber:  1,
				BlockHash:    "0x01",
				Depositor:    "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				UserID:       maker,
				Asset:        ledger.AssetETH,
				RawAmountWei: new(big.Int).Mul(big.NewInt(10), big.NewInt(1_000_000_000_000_000_000)),
			},
			{
				TxHash:       "0xseed_trader_usdc",
				LogIndex:     1,
				BlockNumber:  1,
				BlockHash:    "0x01",
				Depositor:    "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC",
				UserID:       trader,
				Asset:        ledger.AssetUSDC,
				RawAmountWei: big.NewInt(50_000_000_000),
			},
		}
		ch.vaultBal[ledger.AssetETH] = new(big.Int).Mul(big.NewInt(10), big.NewInt(1_000_000_000_000_000_000))
		ch.vaultBal[ledger.AssetUSDC] = big.NewInt(50_000_000_000)
	}
	seedChain(primary)
	seedChain(fallback)

	rpcPool := evm.NewRPCPool([]evm.ChainProvider{primary, fallback}, reg)
	idxStore := evm.NewMemoryIndexStore()
	watcher := evm.NewWatcher(rpcPool, idxStore, store, 3, 64, reg)
	if err := watcher.PollOnce(ctx); err != nil {
		t.Fatalf("initial PollOnce: %v", err)
	}

	hotKey, _ := evm.ParseHexPrivateKey("0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d")
	coldKey, _ := evm.ParseHexPrivateKey("0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a")
	relKey, _ := evm.ParseHexPrivateKey("0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6")
	outbox := evm.NewMemoryOutboxStore()
	relayer := evm.NewRelayer(evm.RelayerConfig{
		ChainID:        31337,
		VaultAddress:   common.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3"),
		TokenAddrs:     map[string]common.Address{ledger.AssetUSDC: common.HexToAddress("0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512")},
		HotKey:         hotKey,
		ColdKey:        coldKey,
		RelayerKey:     relKey,
		StuckThreshold: 1 * time.Millisecond,
	}, rpcPool, outbox, store, reg)

	snapStore := reconciler.NewMemorySnapshotStore()
	checker := reconciler.NewInvariantChecker(store, rpcPool, reg)

	cfg := config.Load()
	cfg.ChaosAPIEnabled = true

	// Wire live ChaosHooks into Watcher & Relayer.
	hooks := &ChaosHooks{
		OnReorg: func(c context.Context, depth int) error {
			// Mine deposit at block 4 -> confirm at block 6 -> revert blocks 4..6 and mine divergent 4'..7'!
			for _, ch := range []*e2eChain{primary, fallback} {
				ch.mu.Lock()
				ch.blocks[4] = evm.BlockHeader{Number: 4, Hash: "0x04", ParentHash: "0x03"}
				ch.blocks[5] = evm.BlockHeader{Number: 5, Hash: "0x05", ParentHash: "0x04"}
				ch.blocks[6] = evm.BlockHeader{Number: 6, Hash: "0x06", ParentHash: "0x05"}
				ch.logs[4] = []evm.DepositLog{{
					TxHash: "0xreorg_dep", LogIndex: 0, BlockNumber: 4, BlockHash: "0x04",
					Depositor: "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC",
					UserID: trader, Asset: ledger.AssetUSDC, RawAmountWei: big.NewInt(5_000_000_000),
				}}
				ch.head = 6
				ch.mu.Unlock()
			}
			if err := watcher.PollOnce(c); err != nil {
				return err
			}
			for _, ch := range []*e2eChain{primary, fallback} {
				ch.mu.Lock()
				ch.blocks[4] = evm.BlockHeader{Number: 4, Hash: "0xF4", ParentHash: "0x03"}
				ch.blocks[5] = evm.BlockHeader{Number: 5, Hash: "0xF5", ParentHash: "0xF4"}
				ch.blocks[6] = evm.BlockHeader{Number: 6, Hash: "0xF6", ParentHash: "0xF5"}
				ch.blocks[7] = evm.BlockHeader{Number: 7, Hash: "0xF7", ParentHash: "0xF6"}
				ch.logs[4] = nil
				ch.head = 7
				ch.mu.Unlock()
			}
			return watcher.PollOnce(c)
		},
		OnGasSpike: func(c context.Context, multiplier int64) error {
			for _, ch := range []*e2eChain{primary, fallback} {
				ch.mu.Lock()
				ch.baseFee = new(big.Int).Mul(big.NewInt(10_000_000_000), big.NewInt(multiplier))
				ch.mu.Unlock()
			}
			time.Sleep(5 * time.Millisecond)
			_, err := relayer.BumpStuckOnce(c)
			return err
		},
	}

	srv := NewServer(Dependencies{
		Config:     cfg,
		Ledger:     store,
		Engine:     eng,
		RPCPool:    rpcPool,
		Relayer:    relayer,
		Outbox:     outbox,
		SnapStore:  snapStore,
		MetricsReg: reg,
		ChaosHooks: hooks,
	})
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	postJSON := func(path, uid, body string, wantCode int) []byte {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if uid != "" {
			req.Header.Set("X-SettleCore-User-Id", uid)
		}
		req.Header.Set("Idempotency-Key", uuid.NewString())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != wantCode {
			t.Fatalf("POST %s status=%d want=%d body=%s", path, resp.StatusCode, wantCode, string(raw))
		}
		return raw
	}

	// 1. Order Submission, CLOB Match, Batch Settlement & Resting Order
	postJSON("/v1/orders", maker.String(), `{"market_id":"ETH/USDC","side":"SELL","order_type":"LIMIT","time_in_force":"GTC","price":"3100.00","quantity":"2.00000000"}`, http.StatusCreated)
	postJSON("/v1/orders", trader.String(), `{"market_id":"ETH/USDC","side":"BUY","order_type":"LIMIT","time_in_force":"GTC","price":"3100.00","quantity":"1.00000000"}`, http.StatusCreated)

	// 2. Drill 1: 3-Block Reorg Rollback
	postJSON("/v1/chaos/reorg", "", `{"depth":3}`, http.StatusOK)

	// 3. Drill 2: Primary RPC Kill & Sub-500ms Failover + Restore
	postJSON("/v1/chaos/rpc-failover", "", `{"action":"kill"}`, http.StatusOK)
	if rpcPool.ActiveName() != "rpc-proxy-2" {
		t.Fatalf("expected rpc-proxy-2 after kill, got %s", rpcPool.ActiveName())
	}
	postJSON("/v1/chaos/rpc-failover", "", `{"action":"restore"}`, http.StatusOK)
	if rpcPool.ActiveName() != "rpc-proxy-1" {
		t.Fatalf("expected rpc-proxy-1 after restore, got %s", rpcPool.ActiveName())
	}

	// 4. Drill 3: Withdrawal Queue -> Submit -> 10x Gas Spike -> EIP-1559 Replacement Bump -> Confirm
	wRespBytes := postJSON("/v1/withdrawals", trader.String(), `{"asset":"USDC","amount":"5000.000000","recipient_address":"0x70997970C51812dc3A010C7d01b50e0d17dc79C8"}`, http.StatusAccepted)
	var wResp struct {
		WithdrawalID string `json:"withdrawal_id"`
	}
	_ = json.Unmarshal(wRespBytes, &wResp)
	if n, err := relayer.ProcessQueuedOnce(ctx); err != nil || n != 1 {
		t.Fatalf("ProcessQueuedOnce: n=%d err=%v", n, err)
	}
	postJSON("/v1/chaos/gas-spike", "", `{"base_fee_multiplier":10}`, http.StatusOK)

	wid := uuid.MustParse(wResp.WithdrawalID)
	wRec, ok := outbox.Get(wid)
	if !ok || wRec.GasBumpCount != 1 {
		t.Fatalf("expected gas_bump_count=1, got %+v", wRec)
	}
	for _, ch := range []*e2eChain{primary, fallback} {
		ch.mu.Lock()
		ch.receipts[wRec.LastTxHash] = evm.TxReceipt{TxHash: wRec.LastTxHash, BlockNumber: 8, Confirmed: true, Succeeded: true}
		ch.vaultBal[ledger.AssetUSDC] = big.NewInt(45_000_000_000) // Vault sent 5,000 USDC
		ch.mu.Unlock()
	}
	if fin, err := relayer.PollReceiptsOnce(ctx); err != nil || fin != 1 {
		t.Fatalf("PollReceiptsOnce: fin=%d err=%v", fin, err)
	}

	// 5. Drill 4: -15% Flash Crash Circuit Breaker
	postJSON("/v1/chaos/flash-crash", "", `{"pair":"ETH/USDC","drop_bps":1500}`, http.StatusOK)

	// 5b. Drill 5: Kill Gateway Mid-Flight (Pre-Trade DB Reserve -> Crash before WAL -> Recovery Rollback)
	postJSON("/v1/chaos/gateway-crash", "", `{}`, http.StatusOK)

	// 6. Merkle Proof-of-Reserves & Solvency Invariant Check
	if _, err := reconciler.GenerateAndSaveSnapshot(ctx, store, checker, snapStore, nil); err != nil {
		t.Fatalf("GenerateAndSaveSnapshot: %v", err)
	}
	pReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v1/solvency/proof", nil)
	pReq.Header.Set("X-SettleCore-User-Id", trader.String())
	pResp, err := http.DefaultClient.Do(pReq)
	if err != nil || pResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/solvency/proof: %v %v", pResp, err)
	}
	var proof reconciler.SolvencyProofResponse
	_ = json.NewDecoder(pResp.Body).Decode(&proof)
	_ = pResp.Body.Close()
	if !proof.InvariantOK || proof.UserLeaf == nil || !reconciler.VerifyProof(proof.MerkleRoot, *proof.UserLeaf, proof.MerklePath) {
		t.Fatalf("Merkle PoR verification failed: %+v", proof)
	}

	// 7. Scrape Prometheus /metrics and assert all required chaos and solvency metrics
	mReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/metrics", nil)
	mResp, err := http.DefaultClient.Do(mReq)
	if err != nil || mResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics: %v %v", mResp, err)
	}
	mBodyBytes, _ := io.ReadAll(mResp.Body)
	_ = mResp.Body.Close()
	mBody := string(mBodyBytes)

	requiredMetrics := []string{
		"order_match_latency_us_count 4",
		"indexer_block_lag 0",
		"reorg_rollbacks_total 1",
		"stuck_tx_gas_bumps 1",
		"reserves_invariant_ok 1",
		`circuit_breaker_tripped{market="ETH/USDC"} 1`,
	}
	for _, want := range requiredMetrics {
		if !strings.Contains(mBody, want) {
			t.Fatalf("Prometheus /metrics missing %q\nBody:\n%s", want, mBody)
		}
	}

	// 8. Verify WAL Replay SHA-256 matches live engine state hash
	liveHash := eng.StateHash()
	replayed, _, err := engine.ReplayFromWAL(filepath.Join(tmp, "wal", "engine.wal"), filepath.Join(tmp, "snap"), engine.EngineConfig{})
	if err != nil {
		t.Fatalf("ReplayFromWAL: %v", err)
	}
	defer func() { _ = replayed.Close() }()
	if replayed.StateHash() != liveHash {
		t.Fatalf("WAL replay SHA-256 mismatch: got %x want %x", replayed.StateHash(), liveHash)
	}
	_ = bytes.NewReader(nil)
}
