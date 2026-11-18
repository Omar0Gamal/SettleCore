package main

import (
	"context"
	"errors"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/settlecore/settlecore/services/internal/config"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/marketmaker"
	"github.com/settlecore/settlecore/services/internal/metrics"
	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Default.Handler())
	metricsSrv := &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("market-maker metrics server error", zap.Error(err))
		}
	}()

	ticks := make(chan marketmaker.PriceTick, 64)
	feed := marketmaker.NewFeed(cfg.MarketMakerBinanceWS, 1*time.Second)
	quoter := marketmaker.NewQuoter(
		cfg.GatewayHTTPURL,
		cfg.GatewayWSURL,
		ledger.MarketMakerUserID,
		cfg.MarketMakerSpreadBps,
		5,
	)

	go func() {
		_ = feed.Run(ctx, ticks)
	}()

	logger.Info("market-maker started", zap.String("gateway", cfg.GatewayHTTPURL))
	_ = quoter.Run(ctx, ticks)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = metricsSrv.Shutdown(shutdownCtx)
}
