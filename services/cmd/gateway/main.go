package main

import (
	"context"
	"errors"
	"net/http"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/settlecore/settlecore/services/internal/config"
	"github.com/settlecore/settlecore/services/internal/engine"
	"github.com/settlecore/settlecore/services/internal/evm"
	"github.com/settlecore/settlecore/services/internal/gateway"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/metrics"
	"github.com/settlecore/settlecore/services/internal/reconciler"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var store ledger.Store
	var snapStore reconciler.SnapshotStore
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err == nil {
		if pingErr := pool.Ping(ctx); pingErr == nil {
			store = ledger.NewPostgresStore(pool)
			snapStore = reconciler.NewPostgresSnapshotStore(pool)
			logger.Info("connected to PostgreSQL 18 ledger")
			defer pool.Close()
		} else {
			pool.Close()
			logger.Warn("postgres unreachable, falling back to in-memory ledger store", zap.Error(pingErr))
			store = ledger.NewMemoryStore()
			snapStore = reconciler.NewMemorySnapshotStore()
		}
	} else {
		store = ledger.NewMemoryStore()
		snapStore = reconciler.NewMemorySnapshotStore()
	}

	eng, err := engine.NewBridge(engine.EngineConfig{
		WALDir:            cfg.EngineWALDir,
		SnapshotDir:       cfg.EngineSnapshotDir,
		WALFsyncMode:      cfg.EngineWALFsyncMode,
		MaxRestingOrders:  cfg.EngineMaxRestingOrders,
		CircuitBreakerBps: cfg.EngineCircuitBreakerBps,
	}, metrics.Default)
	if err != nil {
		logger.Fatal("failed to start engine bridge", zap.Error(err))
	}
	defer func() { _ = eng.Close() }()

	if _, err := eng.LoadLatestSnapshot(cfg.EngineSnapshotDir); err != nil {
		logger.Warn("failed to load snapshot", zap.Error(err))
	}

	// Startup crash recovery: roll back any RESERVED orders absent from WAL.
	if recReport, recErr := gateway.ReconcileOrphanedReservations(ctx, store, filepath.Join(cfg.EngineWALDir, "engine.wal")); recErr != nil {
		logger.Warn("startup orphan reservation reconciliation error", zap.Error(recErr))
	} else if recReport.OrphansRolledBack > 0 {
		logger.Info("recovered orphaned pre-WAL reservations",
			zap.Int("orphans_rolled_back", recReport.OrphansRolledBack),
			zap.String("released_total", recReport.ReleasedTotal),
		)
	}

	// Seed demo trader & market maker balances when running in standalone memory mode.
	if _, isMem := store.(*ledger.MemoryStore); isMem {
		_, _ = store.CreditDeposit(ctx, ledger.DefaultTraderUserID, ledger.AssetETH, decimal.NewFromInt(20), "seed:trader:eth")
		_, _ = store.CreditDeposit(ctx, ledger.DefaultTraderUserID, ledger.AssetUSDC, decimal.NewFromInt(100000), "seed:trader:usdc")
		_, _ = store.CreditDeposit(ctx, ledger.DefaultTraderUserID, ledger.AssetWBTC, decimal.NewFromInt(2), "seed:trader:wbtc")
		_, _ = store.CreditDeposit(ctx, ledger.DefaultTraderUserID, ledger.AssetUSDT, decimal.NewFromInt(25000), "seed:trader:usdt")
		_, _ = store.CreditDeposit(ctx, ledger.MarketMakerUserID, ledger.AssetETH, decimal.NewFromInt(200), "seed:mm:eth")
		_, _ = store.CreditDeposit(ctx, ledger.MarketMakerUserID, ledger.AssetUSDC, decimal.NewFromInt(1000000), "seed:mm:usdc")
		_, _ = store.CreditDeposit(ctx, ledger.MarketMakerUserID, ledger.AssetWBTC, decimal.NewFromInt(10), "seed:mm:wbtc")
	}

	rpcPool, _ := evm.NewEthRPCPool(
		cfg.EVMPrimaryRPCURL,
		cfg.EVMFallbackRPCURL,
		cfg.EscrowVaultAddress,
		cfg.USDCTokenAddress,
		cfg.WBTCTokenAddress,
		cfg.USDTTokenAddress,
		metrics.Default,
	)

	hotKey, _ := evm.ParseHexPrivateKey(cfg.HotSignerPrivateKey)
	coldKey, _ := evm.ParseHexPrivateKey(cfg.ColdSignerPrivateKey)
	relKey, _ := evm.ParseHexPrivateKey(cfg.RelayerPrivateKey)
	outbox := evm.NewMemoryOutboxStore()
	relayer := evm.NewRelayer(evm.RelayerConfig{
		ChainID:      cfg.EVMChainID,
		VaultAddress: common.HexToAddress(cfg.EscrowVaultAddress),
		TokenAddrs: map[string]common.Address{
			ledger.AssetETH:  {},
			ledger.AssetUSDC: common.HexToAddress(cfg.USDCTokenAddress),
			ledger.AssetWBTC: common.HexToAddress(cfg.WBTCTokenAddress),
			ledger.AssetUSDT: common.HexToAddress(cfg.USDTTokenAddress),
		},
		HotKey:     hotKey,
		ColdKey:    coldKey,
		RelayerKey: relKey,
	}, rpcPool, outbox, store, metrics.Default)

	srv := gateway.NewServer(gateway.Dependencies{
		Config:     cfg,
		Ledger:     store,
		Engine:     eng,
		RPCPool:    rpcPool,
		Relayer:    relayer,
		Outbox:     outbox,
		SnapStore:  snapStore,
		MetricsReg: metrics.Default,
	})

	// Telemetry broadcaster loop.
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				snap := metrics.Default.Snapshot()
				srv.WSHub().Broadcast("telemetry", map[string]any{
					"channel":                "telemetry",
					"clob_p50_us":            snap.P50Us,
					"clob_p99_us":            snap.P99Us,
					"clob_p999_us":           snap.P999Us,
					"active_rpc":             snap.ActiveRPC,
					"indexer_block_lag":      snap.IndexerBlockLag,
					"reorg_rollbacks_total":  snap.ReorgRollbacksTotal,
					"stuck_tx_gas_bumps":     snap.StuckTxGasBumps,
					"reserves_invariant_ok":  snap.ReservesInvariantOK,
					"circuit_breaker_status": snap.CircuitBreakerStatus,
				})
			}
		}
	}()

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", metrics.Default.Handler())
	metricsSrv := &http.Server{
		Addr:              cfg.GatewayMetricsAddr,
		Handler:           metricsMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server error", zap.Error(err))
		}
	}()

	httpSrv := &http.Server{
		Addr:              cfg.GatewayHTTPAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Info("gateway listening", zap.String("addr", cfg.GatewayHTTPAddr))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("gateway HTTP error", zap.Error(err))
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	_ = metricsSrv.Shutdown(shutdownCtx)
}
