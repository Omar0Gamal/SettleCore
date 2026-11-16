package main

import (
	"context"
	"errors"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/settlecore/settlecore/services/internal/config"
	"github.com/settlecore/settlecore/services/internal/evm"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/metrics"
	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("failed to connect postgres", zap.Error(err))
	}
	defer pool.Close()

	rpcPool, err := evm.NewEthRPCPool(
		cfg.EVMPrimaryRPCURL,
		cfg.EVMFallbackRPCURL,
		cfg.EscrowVaultAddress,
		cfg.USDCTokenAddress,
		cfg.WBTCTokenAddress,
		cfg.USDTTokenAddress,
		metrics.Default,
	)
	if err != nil {
		logger.Fatal("failed to initialize RPC pool", zap.Error(err))
	}

	idxStore := evm.NewPostgresIndexStore(pool)
	ledgerStore := ledger.NewPostgresStore(pool)
	watcher := evm.NewWatcher(rpcPool, idxStore, ledgerStore, cfg.EVMConfirmationsRequired, cfg.EVMReorgWindowBlocks, metrics.Default)

	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Default.Handler())
	metricsSrv := &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("watcher metrics server error", zap.Error(err))
		}
	}()

	logger.Info("watcher started", zap.Int64("confirmations", cfg.EVMConfirmationsRequired))
	_ = watcher.Run(ctx, 1*time.Second)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = metricsSrv.Shutdown(shutdownCtx)
}
