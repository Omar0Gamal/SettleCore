package main

import (
	"context"
	"errors"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/settlecore/settlecore/services/internal/config"
	"github.com/settlecore/settlecore/services/internal/evm"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/metrics"
	"github.com/settlecore/settlecore/services/internal/reconciler"
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

	ledgerStore := ledger.NewPostgresStore(pool)
	snapStore := reconciler.NewPostgresSnapshotStore(pool)
	checker := reconciler.NewInvariantChecker(ledgerStore, rpcPool, metrics.Default)

	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Default.Handler())
	metricsSrv := &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("reconciler metrics server error", zap.Error(err))
		}
	}()

	invTicker := time.NewTicker(time.Duration(cfg.ReconcilerIntervalSec) * time.Second)
	merkleTicker := time.NewTicker(time.Duration(cfg.MerkleSnapshotIntervalSec) * time.Second)
	defer invTicker.Stop()
	defer merkleTicker.Stop()

	logger.Info("reconciler started")
	_, _ = reconciler.GenerateAndSaveSnapshot(ctx, ledgerStore, checker, snapStore, func(_ context.Context, root common.Hash, h int64) (string, error) {
		logger.Info("generated initial merkle root", zap.String("root", root.Hex()), zap.Int64("block", h))
		return "", nil
	})

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = metricsSrv.Shutdown(shutdownCtx)
			return
		case <-invTicker.C:
			if rep, err := checker.CheckOnce(ctx); err != nil {
				logger.Error("invariant check failed", zap.Error(err))
			} else if !rep.InvariantOK {
				logger.Error("RESERVES INVARIANT VIOLATED", zap.Int64("block", rep.BlockNumber))
			}
		case <-merkleTicker.C:
			_, _ = reconciler.GenerateAndSaveSnapshot(ctx, ledgerStore, checker, snapStore, nil)
		}
	}
}
