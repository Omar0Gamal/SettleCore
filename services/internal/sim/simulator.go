package sim

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/settlecore/settlecore/services/internal/engine"
	"github.com/settlecore/settlecore/services/internal/evm"
	"github.com/settlecore/settlecore/services/internal/gateway"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/metrics"
	"github.com/settlecore/settlecore/services/internal/reconciler"
	"github.com/shopspring/decimal"
)

// SimReport summarizes a completed deterministic fault-injection simulation run.
type SimReport struct {
	Seed                   uint64   `json:"seed"`
	StepsExecuted          int      `json:"steps_executed"`
	Deposits               int      `json:"deposits"`
	OrdersSubmitted        int      `json:"orders_submitted"`
	OrdersMatched          int      `json:"orders_matched"`
	OrdersCanceled         int      `json:"orders_canceled"`
	Withdrawals            int      `json:"withdrawals"`
	ReorgsInjected         int      `json:"reorgs_injected"`
	RPCBlackoutsInjected   int      `json:"rpc_blackouts_injected"`
	GasSpikesInjected      int      `json:"gas_spikes_injected"`
	FlashCrashesInjected   int      `json:"flash_crashes_injected"`
	GatewayCrashesInjected int      `json:"gateway_crashes_injected"`
	FinalStateHash         [32]byte `json:"final_state_hash"`
}

type simFaultConfig struct {
	InjectBug string // "", "unbalanced_balance", "skip_reorg_reversal", "corrupt_wal_replay"
}

type simChain struct {
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

func newSimChain(name string) *simChain {
	return &simChain{
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

func (c *simChain) Name() string { return c.name }

func (c *simChain) setFailAll(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failAll = v
}

func (c *simChain) HeadNumber(_ context.Context) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failAll {
		return 0, errors.New("simulated rpc blackout")
	}
	return c.head, nil
}

func (c *simChain) BlockByNumber(_ context.Context, num int64) (evm.BlockHeader, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failAll {
		return evm.BlockHeader{}, errors.New("simulated rpc blackout")
	}
	b, ok := c.blocks[num]
	if !ok {
		return evm.BlockHeader{}, evm.ErrBlockNotFound
	}
	return b, nil
}

func (c *simChain) DepositLogs(_ context.Context, num int64) ([]evm.DepositLog, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failAll {
		return nil, errors.New("simulated rpc blackout")
	}
	src := c.logs[num]
	out := make([]evm.DepositLog, len(src))
	copy(out, src)
	return out, nil
}

func (c *simChain) SuggestGasFees(_ context.Context) (*big.Int, *big.Int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failAll {
		return nil, nil, errors.New("simulated rpc blackout")
	}
	return new(big.Int).Set(c.baseFee), new(big.Int).Set(c.tip), nil
}

func (c *simChain) PendingNonce(_ context.Context, _ common.Address) (uint64, error) {
	return 0, nil
}

func (c *simChain) BroadcastTx(_ context.Context, sub evm.TxSubmission) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failAll {
		return "", errors.New("simulated rpc blackout")
	}
	c.txSeq++
	return fmt.Sprintf("0xsim_%d_n%d", c.txSeq, sub.Nonce), nil
}

func (c *simChain) TxReceipt(_ context.Context, txHash string) (evm.TxReceipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.receipts[txHash]; ok {
		return r, nil
	}
	return evm.TxReceipt{TxHash: txHash, Confirmed: false}, nil
}

func (c *simChain) VaultBalance(_ context.Context, asset string) (*big.Int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if b, ok := c.vaultBal[asset]; ok {
		return new(big.Int).Set(b), nil
	}
	return big.NewInt(0), nil
}

func simErr(seed uint64, step int, inv, detail string) error {
	return fmt.Errorf("[seed=0x%x step=%d invariant=%s] %s | repro: go test ./internal/sim -run TestDeterministicSimulation -seed=0x%x",
		seed, step, inv, detail, seed)
}

// RunSeed executes a deterministic, seed-driven fault-injection simulation for `steps` transitions,
// asserting all 6 formal invariants (I-1 through I-6) after every single step.
func RunSeed(seed uint64, steps int) (*SimReport, error) {
	return runSeedWithFault(seed, steps, simFaultConfig{})
}

func runSeedWithFault(seed uint64, steps int, fault simFaultConfig) (*SimReport, error) {
	if steps <= 0 {
		steps = 200
	}
	ctx := context.Background()
	tmpDir, err := os.MkdirTemp("", "settlecore-sim-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	walDir := filepath.Join(tmpDir, "wal")
	snapDir := filepath.Join(tmpDir, "snap")
	reg := metrics.NewRegistry()

	store := ledger.NewMemoryStore()
	idxStore := evm.NewMemoryIndexStore()
	outbox := evm.NewMemoryOutboxStore()

	primary := newSimChain("rpc-proxy-1")
	fallback := newSimChain("rpc-proxy-2")
	rpcPool := evm.NewRPCPool([]evm.ChainProvider{primary, fallback}, reg)
	watcher := evm.NewWatcher(rpcPool, idxStore, store, 3, 64, reg)

	hotKey, _ := evm.ParseHexPrivateKey("0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d")
	coldKey, _ := evm.ParseHexPrivateKey("0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a")
	relKey, _ := evm.ParseHexPrivateKey("0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6")
	relayer := evm.NewRelayer(evm.RelayerConfig{
		ChainID:        31337,
		VaultAddress:   common.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3"),
		TokenAddrs:     map[string]common.Address{ledger.AssetUSDC: common.HexToAddress("0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512")},
		HotKey:         hotKey,
		ColdKey:        coldKey,
		RelayerKey:     relKey,
		StuckThreshold: 1 * time.Millisecond,
	}, rpcPool, outbox, store, reg)

	eng, err := engine.NewBridge(engine.EngineConfig{
		WALDir:            walDir,
		SnapshotDir:       snapDir,
		WALFsyncMode:      "buffered",
		MaxRestingOrders:  50_000,
		CircuitBreakerBps: 1000,
	}, reg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = eng.Close() }()

	shadowEng, err := engine.NewBridge(engine.EngineConfig{
		DisableWAL:        true,
		MaxRestingOrders:  50_000,
		CircuitBreakerBps: 1000,
	}, metrics.NewRegistry())
	if err != nil {
		return nil, err
	}
	defer func() { _ = shadowEng.Close() }()
	var shadowLastWalSeq uint64

	checker := reconciler.NewInvariantChecker(store, rpcPool, reg)

	// 6 deterministic trader accounts.
	users := make([]uuid.UUID, 6)
	for i := range users {
		users[i] = uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("sim-user-%d-%d", seed, i)))
		_ = store.EnsureUserAccounts(ctx, users[i])
	}

	// Bootstrap blocks 0..3 with initial confirmed liquidity across all 4 assets.
	seedDepositBlock := func(ch *simChain) {
		ch.blocks[0] = evm.BlockHeader{Number: 0, Hash: "0x0000", ParentHash: "0x0000"}
		ch.blocks[1] = evm.BlockHeader{Number: 1, Hash: "0x0001", ParentHash: "0x0000"}
		ch.blocks[2] = evm.BlockHeader{Number: 2, Hash: "0x0002", ParentHash: "0x0001"}
		ch.blocks[3] = evm.BlockHeader{Number: 3, Hash: "0x0003", ParentHash: "0x0002"}
		ch.head = 3

		var logs []evm.DepositLog
		for i, u := range users {
			usdcWei, _ := ledger.HumanToWei(ledger.AssetUSDC, decimal.NewFromInt(25000))
			ethWei, _ := ledger.HumanToWei(ledger.AssetETH, decimal.NewFromInt(10))
			wbtcWei, _ := ledger.HumanToWei(ledger.AssetWBTC, decimal.NewFromInt(1))
			usdtWei, _ := ledger.HumanToWei(ledger.AssetUSDT, decimal.NewFromInt(5000))
			logs = append(logs,
				evm.DepositLog{TxHash: fmt.Sprintf("0xinit_usdc_%d", i), LogIndex: int32(i*4 + 0), BlockNumber: 1, BlockHash: "0x0001", Depositor: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8", UserID: u, Asset: ledger.AssetUSDC, RawAmountWei: usdcWei},
				evm.DepositLog{TxHash: fmt.Sprintf("0xinit_eth_%d", i), LogIndex: int32(i*4 + 1), BlockNumber: 1, BlockHash: "0x0001", Depositor: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8", UserID: u, Asset: ledger.AssetETH, RawAmountWei: ethWei},
				evm.DepositLog{TxHash: fmt.Sprintf("0xinit_wbtc_%d", i), LogIndex: int32(i*4 + 2), BlockNumber: 1, BlockHash: "0x0001", Depositor: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8", UserID: u, Asset: ledger.AssetWBTC, RawAmountWei: wbtcWei},
				evm.DepositLog{TxHash: fmt.Sprintf("0xinit_usdt_%d", i), LogIndex: int32(i*4 + 3), BlockNumber: 1, BlockHash: "0x0001", Depositor: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8", UserID: u, Asset: ledger.AssetUSDT, RawAmountWei: usdtWei},
			)
			ch.vaultBal[ledger.AssetUSDC].Add(ch.vaultBal[ledger.AssetUSDC], usdcWei)
			ch.vaultBal[ledger.AssetETH].Add(ch.vaultBal[ledger.AssetETH], ethWei)
			ch.vaultBal[ledger.AssetWBTC].Add(ch.vaultBal[ledger.AssetWBTC], wbtcWei)
			ch.vaultBal[ledger.AssetUSDT].Add(ch.vaultBal[ledger.AssetUSDT], usdtWei)
		}
		ch.logs[1] = logs
	}
	seedDepositBlock(primary)
	seedDepositBlock(fallback)
	if err := watcher.PollOnce(ctx); err != nil {
		return nil, simErr(seed, 0, "INIT", err.Error())
	}

	rng := rand.New(rand.NewSource(int64(seed))) // #nosec G404
	report := &SimReport{Seed: seed}

	type liveOrd struct {
		orderID  int64
		marketID string
		userID   uuid.UUID
	}
	var liveOrders []liveOrd
	var lastVoucherNonce int64
	lastSettledSeq := map[string]int64{ledger.MarketETHUSDC: 0, ledger.MarketBTCUSDC: 0}
	markETH := int64(3100) * ledger.CLOBScale
	markBTC := int64(95000) * ledger.CLOBScale
	walPath := filepath.Join(walDir, "engine.wal")
	nextReorgForkID := 1

	mineEmptyBlock := func() int64 {
		primary.mu.Lock()
		fallback.mu.Lock()
		defer primary.mu.Unlock()
		defer fallback.mu.Unlock()
		next := primary.head + 1
		prevHash := primary.blocks[next-1].Hash
		newHash := fmt.Sprintf("0xblk_%d_%d", next, nextReorgForkID)
		blk := evm.BlockHeader{Number: next, Hash: newHash, ParentHash: prevHash}
		primary.blocks[next] = blk
		fallback.blocks[next] = blk
		primary.head = next
		fallback.head = next
		return next
	}

	for step := 1; step <= steps; step++ {
		action := rng.Intn(100)

		switch {
		case action < 12:
			// Action: Mine block with deposit & advance confirmations via watcher.
			u := users[rng.Intn(len(users))]
			asset := ledger.AllAssets[rng.Intn(len(ledger.AllAssets))]
			amt := decimal.NewFromInt(int64(rng.Intn(50)+1) * 10)
			if asset == ledger.AssetETH || asset == ledger.AssetWBTC {
				amt = decimal.NewFromInt(int64(rng.Intn(3) + 1))
			}
			wei, _ := ledger.HumanToWei(asset, amt)
			h := mineEmptyBlock()
			primary.mu.Lock()
			fallback.mu.Lock()
			blkHash := primary.blocks[h].Hash
			depLog := evm.DepositLog{
				TxHash:       fmt.Sprintf("0xdep_%d_%d", seed, step),
				LogIndex:     0,
				BlockNumber:  h,
				BlockHash:    blkHash,
				Depositor:    "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC",
				UserID:       u,
				Asset:        asset,
				RawAmountWei: wei,
			}
			primary.logs[h] = []evm.DepositLog{depLog}
			fallback.logs[h] = []evm.DepositLog{depLog}
			primary.vaultBal[asset].Add(primary.vaultBal[asset], wei)
			fallback.vaultBal[asset].Add(fallback.vaultBal[asset], wei)
			primary.mu.Unlock()
			fallback.mu.Unlock()

			// Mine 2 more blocks so deposit reaches 3 confirmations.
			mineEmptyBlock()
			mineEmptyBlock()
			if err := watcher.PollOnce(ctx); err != nil {
				return nil, simErr(seed, step, "WATCHER_DEPOSIT", err.Error())
			}
			report.Deposits++

		case action < 62:
			// Action: Two-phase Limit/Market order submission + settlement.
			u := users[rng.Intn(len(users))]
			mkt := ledger.MarketETHUSDC
			mark := markETH
			if rng.Intn(4) == 0 {
				mkt = ledger.MarketBTCUSDC
				mark = markBTC
			}
			mktCode, _ := engine.MarketIDToCode(mkt)
			side := ledger.SideBuy
			sideCode := engine.SideCodeBuy
			if rng.Intn(2) == 1 {
				side = ledger.SideSell
				sideCode = engine.SideCodeSell
			}
			ot := ledger.OrderTypeLimit
			otCode := engine.OrderTypeCodeLimit
			tif := ledger.TIFGTC
			tifCode := engine.TIFCodeGTC
			switch rng.Intn(5) {
			case 3:
				tif = ledger.TIFIOC
				tifCode = engine.TIFCodeIOC
			case 4:
				tif = ledger.TIFFOK
				tifCode = engine.TIFCodeFOK
			}
			if rng.Intn(6) == 0 {
				ot = ledger.OrderTypeMarket
				otCode = engine.OrderTypeCodeMarket
				if tif == ledger.TIFGTC {
					tif = ledger.TIFIOC
					tifCode = engine.TIFCodeIOC
				}
			}

			bps := int64(rng.Intn(801) - 400) // within +/- 4%
			px1e8 := mark * (10_000 + bps) / 10_000
			if ot == ledger.OrderTypeMarket && side == ledger.SideBuy {
				// MARKET BUY reserves at +10% circuit-breaker collar so any collar-valid fill is covered.
				px1e8 = mark * 11_000 / 10_000
			}
			px1e8 = (px1e8 / ledger.SixDecimalLotStep) * ledger.SixDecimalLotStep
			if px1e8 < ledger.SixDecimalLotStep {
				px1e8 = ledger.SixDecimalLotStep
			}
			qty1e8 := int64(rng.Intn(20)+1) * (ledger.CLOBScale / 20) // 0.05 .. 1.00
			priceDec := ledger.CLOBInt64ToHuman(px1e8)
			qtyDec := ledger.CLOBInt64ToHuman(qty1e8)

			_, resAmt, err := ledger.ComputeOrderReserveAmount(mkt, side, priceDec, qtyDec, 10, 2)
			if err != nil {
				return nil, simErr(seed, step, "I-3", err.Error())
			}

			ord, _, rErr := store.ReserveOrder(ctx, ledger.ReserveOrderReq{
				IdempotencyKey: uuid.New(),
				UserID:         u,
				MarketID:       mkt,
				Side:           side,
				OrderType:      ot,
				TimeInForce:    tif,
				Price:          priceDec,
				Quantity:       qtyDec,
				ReserveAmount:  resAmt,
			})
			if rErr == nil {
				report.OrdersSubmitted++
				accLow, accHigh := engine.UUIDToUint64Pair(u)
				rep, engErr := eng.Submit(engine.OrderCmd{
					OrderID:       uint64(ord.OrderID),
					AccountIDLow:  accLow,
					AccountIDHigh: accHigh,
					Price1e8:      px1e8,
					Qty1e8:        qty1e8,
					TimestampNs:   uint64(step) * 1000,
					MarketID:      mktCode,
					Opcode:        engine.OpSubmitOrder,
					Side:          sideCode,
					OrderType:     otCode,
					TimeInForce:   tifCode,
					STPMode:       uint8(rng.Intn(3)),
				})
				if engErr != nil || rep.ErrorCode == engine.ErrCodeFOKUnfilled || (rep.Status == engine.ExecStatusCanceled && rep.FilledQty1e8 == 0) {
					_, _ = store.ReleaseOrderReservation(ctx, ord.OrderID, int64(rep.WalSeq), ledger.OrderStatusRejectedReleased)
				} else {
					if rep.Status == engine.ExecStatusAccepted {
						_ = store.UpdateOrderStatus(ctx, ord.OrderID, int64(rep.WalSeq), ledger.OrderStatusOpen)
						liveOrders = append(liveOrders, liveOrd{orderID: ord.OrderID, marketID: mkt, userID: u})
					} else {
						if rep.Status == engine.ExecStatusPartiallyFilled && ot == ledger.OrderTypeLimit && tif == ledger.TIFGTC {
							liveOrders = append(liveOrders, liveOrd{orderID: ord.OrderID, marketID: mkt, userID: u})
						}
						if err := settleSimReport(ctx, store, mkt, side, rep); err != nil {
							return nil, simErr(seed, step, "SETTLE_REPORT", err.Error())
						}
						report.OrdersMatched += len(rep.Fills)
					}
				}
			}

		case action < 74 && len(liveOrders) > 0:
			// Action: Cancel an open resting order.
			idx := rng.Intn(len(liveOrders))
			target := liveOrders[idx]
			liveOrders = append(liveOrders[:idx], liveOrders[idx+1:]...)

			ord, err := store.GetOrder(ctx, target.orderID)
			if err == nil && (ord.Status == ledger.OrderStatusOpen || ord.Status == ledger.OrderStatusPartiallyFilled || ord.Status == ledger.OrderStatusReserved) {
				code, _ := engine.MarketIDToCode(target.marketID)
				rep, _ := eng.Submit(engine.OrderCmd{
					OrderID:  uint64(target.orderID),
					MarketID: code,
					Opcode:   engine.OpCancelOrder,
				})
				if _, relErr := store.ReleaseOrderReservation(ctx, target.orderID, int64(rep.WalSeq), ledger.OrderStatusCanceled); relErr == nil {
					report.OrdersCanceled++
				}
			}

		case action < 84:
			// Action: Queue withdrawal -> broadcast -> confirm receipt -> finalize.
			u := users[rng.Intn(len(users))]
			amt := decimal.NewFromInt(int64(rng.Intn(200) + 10))
			wei, _ := ledger.HumanToWei(ledger.AssetUSDC, amt)
			lastVoucherNonce++
			wID := uuid.New()
			wRec, _, qErr := store.QueueWithdrawal(ctx, ledger.WithdrawalRequest{
				WithdrawalID:     wID,
				IdempotencyKey:   uuid.New(),
				UserID:           u,
				RecipientAddress: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:            ledger.AssetUSDC,
				HumanAmount:      amt,
				RawAmountWei:     wei,
				SigningTier:      evm.SigningTierHot,
				VoucherNonce:     lastVoucherNonce,
			})
			if qErr == nil {
				outbox.Enqueue(*wRec)
				if _, pErr := relayer.ProcessQueuedOnce(ctx); pErr != nil {
					return nil, simErr(seed, step, "RELAYER_PROCESS", pErr.Error())
				}
				rec, ok := outbox.Get(wID)
				if ok && rec.LastTxHash != "" {
					h := mineEmptyBlock()
					primary.mu.Lock()
					fallback.mu.Lock()
					rcpt := evm.TxReceipt{TxHash: rec.LastTxHash, BlockNumber: h, Confirmed: true, Succeeded: true}
					primary.receipts[rec.LastTxHash] = rcpt
					fallback.receipts[rec.LastTxHash] = rcpt
					primary.vaultBal[ledger.AssetUSDC].Sub(primary.vaultBal[ledger.AssetUSDC], wei)
					fallback.vaultBal[ledger.AssetUSDC].Sub(fallback.vaultBal[ledger.AssetUSDC], wei)
					primary.mu.Unlock()
					fallback.mu.Unlock()
					if _, fErr := relayer.PollReceiptsOnce(ctx); fErr != nil {
						return nil, simErr(seed, step, "RELAYER_FINALIZE", fErr.Error())
					}
					report.Withdrawals++
				}
			}

		case action < 88:
			// Fault 1: 1-5 block chain reorg (mine depth blocks with deposit, then fork to divergent branch).
			depth := rng.Intn(5) + 1
			if err := watcher.PollOnce(ctx); err != nil {
				return nil, simErr(seed, step, "REORG_PRE_POLL", err.Error())
			}
			primary.mu.Lock()
			forkBase := primary.head
			primary.mu.Unlock()

			u := users[rng.Intn(len(users))]
			depAmt := decimal.NewFromInt(100)
			depWei, _ := ledger.HumanToWei(ledger.AssetUSDC, depAmt)
			nextReorgForkID++
			forkTag := nextReorgForkID

			// Old canonical branch of length `depth` with a deposit at forkBase+1.
			for d := 1; d <= depth; d++ {
				h := mineEmptyBlock()
				if d == 1 {
					primary.mu.Lock()
					fallback.mu.Lock()
					depLog := evm.DepositLog{
						TxHash:       fmt.Sprintf("0xreorg_%d_%d", seed, step),
						LogIndex:     0,
						BlockNumber:  h,
						BlockHash:    primary.blocks[h].Hash,
						Depositor:    "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC",
						UserID:       u,
						Asset:        ledger.AssetUSDC,
						RawAmountWei: depWei,
					}
					primary.logs[h] = []evm.DepositLog{depLog}
					fallback.logs[h] = []evm.DepositLog{depLog}
					primary.vaultBal[ledger.AssetUSDC].Add(primary.vaultBal[ledger.AssetUSDC], depWei)
					fallback.vaultBal[ledger.AssetUSDC].Add(fallback.vaultBal[ledger.AssetUSDC], depWei)
					primary.mu.Unlock()
					fallback.mu.Unlock()
				}
			}
			if err := watcher.PollOnce(ctx); err != nil {
				return nil, simErr(seed, step, "REORG_BRANCH_POLL", err.Error())
			}

			// Now fork at forkBase with a longer divergent chain (depth+1 blocks) without the orphaned deposit!
			if fault.InjectBug != "skip_reorg_reversal" {
				primary.mu.Lock()
				fallback.mu.Lock()
				prevHash := primary.blocks[forkBase].Hash
				for d := 1; d <= depth+1; d++ {
					h := forkBase + int64(d)
					newHash := fmt.Sprintf("0xfork_%d_%d_%d", forkTag, step, h)
					blk := evm.BlockHeader{Number: h, Hash: newHash, ParentHash: prevHash}
					primary.blocks[h] = blk
					fallback.blocks[h] = blk
					primary.logs[h] = nil
					fallback.logs[h] = nil
					prevHash = newHash
				}
				primary.head = forkBase + int64(depth+1)
				fallback.head = forkBase + int64(depth+1)
				primary.vaultBal[ledger.AssetUSDC].Sub(primary.vaultBal[ledger.AssetUSDC], depWei)
				fallback.vaultBal[ledger.AssetUSDC].Sub(fallback.vaultBal[ledger.AssetUSDC], depWei)
				primary.mu.Unlock()
				fallback.mu.Unlock()

				if err := watcher.PollOnce(ctx); err != nil {
					return nil, simErr(seed, step, "REORG_ROLLBACK", err.Error())
				}
			} else {
				// Deliberately skip reversing ledger while reverting chain vault balance -> triggers I-2 Solvency catch!
				primary.mu.Lock()
				fallback.mu.Lock()
				primary.vaultBal[ledger.AssetUSDC].Sub(primary.vaultBal[ledger.AssetUSDC], depWei)
				fallback.vaultBal[ledger.AssetUSDC].Sub(fallback.vaultBal[ledger.AssetUSDC], depWei)
				primary.mu.Unlock()
				fallback.mu.Unlock()
			}
			report.ReorgsInjected++

		case action < 91:
			// Fault 2: Primary RPC blackout & sub-500ms failover + recovery.
			primary.setFailAll(true)
			rpcPool.KillPrimary()
			mineEmptyBlock()
			if err := watcher.PollOnce(ctx); err != nil {
				return nil, simErr(seed, step, "RPC_FAILOVER", err.Error())
			}
			primary.setFailAll(false)
			rpcPool.RestorePrimary()
			report.RPCBlackoutsInjected++

		case action < 94:
			// Fault 3: 10x baseFee spike & EIP-1559 gas bump on pending withdrawal.
			u := users[rng.Intn(len(users))]
			amt := decimal.NewFromInt(25)
			wei, _ := ledger.HumanToWei(ledger.AssetUSDC, amt)
			lastVoucherNonce++
			wID := uuid.New()
			wRec, _, qErr := store.QueueWithdrawal(ctx, ledger.WithdrawalRequest{
				WithdrawalID:     wID,
				IdempotencyKey:   uuid.New(),
				UserID:           u,
				RecipientAddress: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:            ledger.AssetUSDC,
				HumanAmount:      amt,
				RawAmountWei:     wei,
				SigningTier:      evm.SigningTierHot,
				VoucherNonce:     lastVoucherNonce,
			})
			if qErr == nil {
				outbox.Enqueue(*wRec)
				if _, pErr := relayer.ProcessQueuedOnce(ctx); pErr != nil {
					return nil, simErr(seed, step, "GAS_SPIKE_PROCESS", pErr.Error())
				}
				primary.mu.Lock()
				fallback.mu.Lock()
				oldBase := new(big.Int).Set(primary.baseFee)
				spiked := new(big.Int).Mul(oldBase, big.NewInt(10))
				primary.baseFee = spiked
				fallback.baseFee = new(big.Int).Set(spiked)
				primary.mu.Unlock()
				fallback.mu.Unlock()

				time.Sleep(2 * time.Millisecond)
				if _, bErr := relayer.BumpStuckOnce(ctx); bErr != nil {
					return nil, simErr(seed, step, "GAS_SPIKE_BUMP", bErr.Error())
				}
				rec, _ := outbox.Get(wID)
				h := mineEmptyBlock()
				primary.mu.Lock()
				fallback.mu.Lock()
				rcpt := evm.TxReceipt{TxHash: rec.LastTxHash, BlockNumber: h, Confirmed: true, Succeeded: true}
				primary.receipts[rec.LastTxHash] = rcpt
				fallback.receipts[rec.LastTxHash] = rcpt
				primary.vaultBal[ledger.AssetUSDC].Sub(primary.vaultBal[ledger.AssetUSDC], wei)
				fallback.vaultBal[ledger.AssetUSDC].Sub(fallback.vaultBal[ledger.AssetUSDC], wei)
				primary.baseFee = oldBase
				fallback.baseFee = new(big.Int).Set(oldBase)
				primary.mu.Unlock()
				fallback.mu.Unlock()
				if _, fErr := relayer.PollReceiptsOnce(ctx); fErr != nil {
					return nil, simErr(seed, step, "GAS_SPIKE_FINALIZE", fErr.Error())
				}
				report.GasSpikesInjected++
			}

		case action < 97:
			// Fault 4: +/- 15% flash crash tripping the circuit breaker + mark price recovery.
			crashSide := uint8(engine.SideCodeSell)
			crashPx := markETH * 8500 / 10_000
			if rng.Intn(2) == 1 {
				crashSide = engine.SideCodeBuy
				crashPx = markETH * 11500 / 10_000
			}
			crashPx = (crashPx / ledger.SixDecimalLotStep) * ledger.SixDecimalLotStep
			u := users[rng.Intn(len(users))]
			accLow, accHigh := engine.UUIDToUint64Pair(u)

			// Seed opposite resting bid/ask at crashPx so the sweep crosses >10% collar.
			oppSide := uint8(engine.SideCodeBuy)
			if crashSide == engine.SideCodeBuy {
				oppSide = engine.SideCodeSell
			}
			oppOrderID := uint64(store.NextOrderID())
			_, _ = eng.Submit(engine.OrderCmd{
				OrderID:       oppOrderID,
				AccountIDLow:  accLow,
				AccountIDHigh: accHigh,
				Price1e8:      crashPx,
				Qty1e8:        ledger.CLOBScale / 10,
				MarketID:      engine.MarketCodeETHUSDC,
				Opcode:        engine.OpSubmitOrder,
				Side:          oppSide,
				OrderType:     engine.OrderTypeCodeLimit,
				TimeInForce:   engine.TIFCodeGTC,
			})
			_, _ = eng.Submit(engine.OrderCmd{
				OrderID:       uint64(store.NextOrderID()),
				AccountIDLow:  accLow + 1,
				AccountIDHigh: accHigh,
				Price1e8:      crashPx,
				Qty1e8:        ledger.CLOBScale / 10,
				MarketID:      engine.MarketCodeETHUSDC,
				Opcode:        engine.OpSubmitOrder,
				Side:          crashSide,
				OrderType:     engine.OrderTypeCodeLimit,
				TimeInForce:   engine.TIFCodeIOC,
			})
			// Cancel the synthetic probe order (cancels succeed even while CB is tripped) so no unbacked order rests.
			_, _ = eng.Submit(engine.OrderCmd{
				OrderID:  oppOrderID,
				MarketID: engine.MarketCodeETHUSDC,
				Opcode:   engine.OpCancelOrder,
			})
			_ = eng.UpdateMarkPrice(ledger.MarketETHUSDC, markETH)
			report.FlashCrashesInjected++

		default:
			// Fault 5: Mid-flight gateway crash between pre-trade DB Reserve and WAL append.
			u := users[rng.Intn(len(users))]
			pxDec := decimal.NewFromInt(3000)
			qtyDec := decimal.RequireFromString("0.1")
			_, resAmt, _ := ledger.ComputeOrderReserveAmount(ledger.MarketETHUSDC, ledger.SideBuy, pxDec, qtyDec, 10, 2)
			if _, _, rErr := store.ReserveOrder(ctx, ledger.ReserveOrderReq{
				IdempotencyKey: uuid.New(),
				UserID:         u,
				MarketID:       ledger.MarketETHUSDC,
				Side:           ledger.SideBuy,
				OrderType:      ledger.OrderTypeLimit,
				TimeInForce:    ledger.TIFGTC,
				Price:          pxDec,
				Quantity:       qtyDec,
				ReserveAmount:  resAmt,
			}); rErr == nil {
				// Crash happens before eng.Submit!
				if err := eng.SyncWAL(); err != nil {
					return nil, simErr(seed, step, "I-6", err.Error())
				}
				recRep, recErr := gateway.ReconcileOrphanedReservations(ctx, store, walPath)
				if recErr != nil || recRep.OrphansRolledBack == 0 {
					return nil, simErr(seed, step, "GATEWAY_CRASH_RECOVERY", fmt.Sprintf("err=%v rolled_back=%d", recErr, recRep.OrphansRolledBack))
				}
				report.GatewayCrashesInjected++
			}
		}

		// Optional injected bug for negative testing (proving the harness catches bugs immediately).
		if fault.InjectBug == "unbalanced_balance" && step == 5 {
			_, _ = store.CreditDeposit(ctx, users[0], ledger.AssetUSDC, decimal.NewFromInt(1), "injected:unbacked")
		}

		// =========================================================================
		// ASSERT ALL 6 FORMAL INVARIANTS (I-1 .. I-6) AFTER EVERY SINGLE STEP
		// =========================================================================

		// I-1: Double-Entry Conservation & Non-Negative Balances.
		if err := store.VerifyAllJournalsBalanced(); err != nil {
			return nil, simErr(seed, step, "I-1", err.Error())
		}
		for _, u := range users {
			bals, bErr := store.ListUserBalances(ctx, u)
			if bErr != nil {
				return nil, simErr(seed, step, "I-1", bErr.Error())
			}
			for _, b := range bals {
				if b.Available.IsNegative() || b.Locked.IsNegative() {
					return nil, simErr(seed, step, "I-1", fmt.Sprintf("negative balance user=%s asset=%s avail=%s locked=%s", u, b.Asset, b.Available, b.Locked))
				}
				// I-3: Fixed-Point Precision Bounds on balances (exact 1e8 CLOB fixed-point ticks, zero sub-1e8 drift).
				if !b.Available.Shift(ledger.CLOBDecimals).IsInteger() {
					return nil, simErr(seed, step, "I-3", fmt.Sprintf("available precision exceeds 1e8 CLOB ticks %s %s", b.Asset, b.Available))
				}
				if !b.Locked.Shift(ledger.CLOBDecimals).IsInteger() {
					return nil, simErr(seed, step, "I-3", fmt.Sprintf("locked precision exceeds 1e8 CLOB ticks %s %s", b.Asset, b.Locked))
				}
			}
		}

		// I-2: Vault Solvency (OnChainVault >= UserLiabilities + FeeTreasury).
		invRep, invErr := checker.CheckOnce(ctx)
		if invErr != nil {
			return nil, simErr(seed, step, "I-2", invErr.Error())
		}
		if !invRep.InvariantOK {
			return nil, simErr(seed, step, "I-2", fmt.Sprintf("vault insolvency detected: %+v", invRep.Assets))
		}
		for _, asset := range ledger.AllAssets {
			as := invRep.Assets[asset]
			if !as.OnChainVault.Equal(as.TotalRequired) {
				return nil, simErr(seed, step, "I-2", fmt.Sprintf("asset %s vault=%s != liabilities+fees=%s", asset, as.OnChainVault, as.TotalRequired))
			}
		}

		// I-3: Order book price/qty fixed-point precision bounds.
		for _, mkt := range []string{ledger.MarketETHUSDC, ledger.MarketBTCUSDC} {
			depth, dErr := eng.Depth(mkt, 100)
			if dErr != nil {
				return nil, simErr(seed, step, "I-3", dErr.Error())
			}
			for _, lvl := range append(depth.Bids, depth.Asks...) {
				if lvl.Price1e8 < ledger.MinCLOBValue || lvl.Price1e8 > ledger.MaxCLOBValue || lvl.Price1e8%ledger.SixDecimalLotStep != 0 {
					return nil, simErr(seed, step, "I-3", fmt.Sprintf("invalid level price %d on %s", lvl.Price1e8, mkt))
				}
				if lvl.Qty1e8 < ledger.MinCLOBValue || lvl.Qty1e8 > ledger.MaxCLOBValue {
					return nil, simErr(seed, step, "I-3", fmt.Sprintf("invalid level qty %d on %s", lvl.Qty1e8, mkt))
				}
			}
		}

		// I-4: Canonical Block Parent-Hash Lineage.
		latestBlk, bErr := idxStore.GetLatestCanonicalBlock(ctx)
		if bErr != nil || latestBlk == nil {
			return nil, simErr(seed, step, "I-4", fmt.Sprintf("missing canonical head: %v", bErr))
		}
		for h := int64(1); h <= latestBlk.BlockNumber; h++ {
			cur, _ := idxStore.GetCanonicalBlock(ctx, h)
			prev, _ := idxStore.GetCanonicalBlock(ctx, h-1)
			if cur == nil || prev == nil {
				return nil, simErr(seed, step, "I-4", fmt.Sprintf("gap in canonical blocks at height %d", h))
			}
			if cur.ParentHash != prev.BlockHash {
				return nil, simErr(seed, step, "I-4", fmt.Sprintf("parent hash mismatch at height %d: got %s want %s", h, cur.ParentHash, prev.BlockHash))
			}
		}

		// I-5: Nonce & Settled-WAL-Seq Monotonicity.
		maxVNonce, _ := outbox.GetMaxVoucherNonce(ctx)
		if maxVNonce > lastVoucherNonce {
			return nil, simErr(seed, step, "I-5", fmt.Sprintf("outbox voucher nonce %d > tracked %d", maxVNonce, lastVoucherNonce))
		}
		for _, mkt := range []string{ledger.MarketETHUSDC, ledger.MarketBTCUSDC} {
			seq, _ := store.GetLastSettledWalSeq(ctx, mkt)
			if seq < lastSettledSeq[mkt] {
				return nil, simErr(seed, step, "I-5", fmt.Sprintf("settled wal_seq regressed on %s: %d < %d", mkt, seq, lastSettledSeq[mkt]))
			}
			lastSettledSeq[mkt] = seq
		}

		// I-6: Deterministic WAL Replay SHA-256 StateHash() Equality.
		if err := eng.SyncWAL(); err != nil {
			return nil, simErr(seed, step, "I-6", err.Error())
		}
		walRecs, torn, wErr := engine.ReadWAL(walPath)
		if wErr != nil || torn {
			return nil, simErr(seed, step, "I-6", fmt.Sprintf("ReadWAL error: err=%v torn=%v", wErr, torn))
		}
		for _, rec := range walRecs {
			if rec.WalSeq <= shadowLastWalSeq {
				continue
			}
			side, ot, tif, stp := engine.UnpackFlags(rec.Flags)
			_, _ = shadowEng.Submit(engine.OrderCmd{
				WalSeq:        rec.WalSeq,
				OrderID:       rec.OrderID,
				AccountIDLow:  rec.AccountIDLow,
				AccountIDHigh: rec.AccountIDHigh,
				Price1e8:      rec.Price1e8,
				Qty1e8:        rec.Qty1e8,
				MarketID:      rec.MarketID,
				Opcode:        rec.Opcode,
				Side:          side,
				OrderType:     ot,
				TimeInForce:   tif,
				STPMode:       stp,
			})
			shadowLastWalSeq = rec.WalSeq
		}
		if fault.InjectBug == "corrupt_wal_replay" && step == 10 {
			_, _ = shadowEng.Submit(engine.OrderCmd{
				WalSeq:        999999,
				OrderID:       999999,
				AccountIDLow:  1,
				AccountIDHigh: 1,
				Price1e8:      3050 * ledger.CLOBScale,
				Qty1e8:        1 * ledger.CLOBScale,
				MarketID:      engine.MarketCodeETHUSDC,
				Opcode:        engine.OpSubmitOrder,
				Side:          engine.SideCodeBuy,
				OrderType:     engine.OrderTypeCodeLimit,
				TimeInForce:   engine.TIFCodeGTC,
			})
		}
		if shadowEng.StateHash() != eng.StateHash() {
			return nil, simErr(seed, step, "I-6", fmt.Sprintf("WAL replay SHA-256 mismatch: shadow=%x live=%x", shadowEng.StateHash(), eng.StateHash()))
		}
		if step%50 == 0 || step == steps {
			cold, _, cErr := engine.ReplayFromWAL(walPath, "", engine.EngineConfig{
				MaxRestingOrders:  50_000,
				CircuitBreakerBps: 1000,
			})
			if cErr != nil {
				return nil, simErr(seed, step, "I-6", fmt.Sprintf("cold ReplayFromWAL: %v", cErr))
			}
			coldHash := cold.StateHash()
			_ = cold.Close()
			if coldHash != eng.StateHash() {
				return nil, simErr(seed, step, "I-6", fmt.Sprintf("cold WAL replay SHA-256 mismatch: cold=%x live=%x", coldHash, eng.StateHash()))
			}
		}

		report.StepsExecuted = step
	}

	report.FinalStateHash = eng.StateHash()
	return report, nil
}

func settleSimReport(ctx context.Context, store ledger.Store, marketID, takerSide string, rep engine.ExecReport) error {
	if len(rep.Fills) == 0 && len(rep.CanceledIDs) == 0 {
		return nil
	}
	fills := make([]ledger.SettlementFill, 0, len(rep.Fills))
	for _, f := range rep.Fills {
		makerUID := engine.Uint64PairToUUID(f.MakerAccountLow, f.MakerAccountHigh)
		takerOrd, err := store.GetOrder(ctx, int64(f.TakerOrderID))
		if err != nil {
			continue
		}
		makerFee1e8, _ := ledger.ComputeFeeCeil1e8(f.QuoteNotional1e8, 2)
		takerFee1e8, _ := ledger.ComputeFeeCeil1e8(f.QuoteNotional1e8, 10)
		fills = append(fills, ledger.SettlementFill{
			TradeID:       int64(f.TradeID),
			WalSeq:        int64(rep.WalSeq),
			MarketID:      marketID,
			MakerOrderID:  int64(f.MakerOrderID),
			TakerOrderID:  int64(f.TakerOrderID),
			MakerUserID:   makerUID,
			TakerUserID:   takerOrd.UserID,
			TakerSide:     takerSide,
			ExecPrice:     ledger.CLOBInt64ToHuman(f.ExecPrice1e8),
			FillQty:       ledger.CLOBInt64ToHuman(f.FillQty1e8),
			QuoteNotional: ledger.CLOBInt64ToHuman(f.QuoteNotional1e8),
			MakerFee:      ledger.CLOBInt64ToHuman(makerFee1e8),
			TakerFee:      ledger.CLOBInt64ToHuman(takerFee1e8),
			MakerTerminal: f.MakerRemaining == 0,
			TakerTerminal: rep.Status == engine.ExecStatusFilled || rep.Status == engine.ExecStatusCanceled,
		})
	}
	cancels := make([]ledger.SettlementCancel, 0, len(rep.CanceledIDs))
	for _, cid := range rep.CanceledIDs {
		ord, err := store.GetOrder(ctx, int64(cid))
		if err != nil {
			continue
		}
		cancels = append(cancels, ledger.SettlementCancel{
			OrderID:   int64(cid),
			WalSeq:    int64(rep.WalSeq),
			MarketID:  marketID,
			UserID:    ord.UserID,
			NewStatus: ledger.OrderStatusCanceled,
		})
	}
	if len(fills) == 0 && len(cancels) == 0 {
		return nil
	}
	_, err := store.ApplySettlementBatch(ctx, ledger.SettlementBatch{
		MarketID:  marketID,
		MaxWalSeq: int64(rep.WalSeq),
		Fills:     fills,
		Cancels:   cancels,
	})
	return err
}
