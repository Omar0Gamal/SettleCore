package main

import (
	"context"
	"errors"
	"math/big"
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

	hotKey, err := evm.ParseHexPrivateKey(cfg.HotSignerPrivateKey)
	if err != nil {
		logger.Fatal("invalid HOT_SIGNER_PRIVATE_KEY", zap.Error(err))
	}
	coldKey, err := evm.ParseHexPrivateKey(cfg.ColdSignerPrivateKey)
	if err != nil {
		logger.Fatal("invalid COLD_SIGNER_PRIVATE_KEY", zap.Error(err))
	}
	relKey, err := evm.ParseHexPrivateKey(cfg.RelayerPrivateKey)
	if err != nil {
		logger.Fatal("invalid RELAYER_PRIVATE_KEY", zap.Error(err))
	}

	ethCap, _ := new(big.Int).SetString("5000000000000000000", 10)
	ethDaily, _ := new(big.Int).SetString("25000000000000000000", 10)

	relayer := evm.NewRelayer(
		evm.RelayerConfig{
			ChainID:      cfg.EVMChainID,
			VaultAddress: common.HexToAddress(cfg.EscrowVaultAddress),
			TokenAddrs: map[string]common.Address{
				ledger.AssetETH:  {},
				ledger.AssetUSDC: common.HexToAddress(cfg.USDCTokenAddress),
				ledger.AssetWBTC: common.HexToAddress(cfg.WBTCTokenAddress),
				ledger.AssetUSDT: common.HexToAddress(cfg.USDTTokenAddress),
			},
			HotKey:         hotKey,
			ColdKey:        coldKey,
			RelayerKey:     relKey,
			StuckThreshold: 12 * time.Second,
			HotPerTxCaps: map[string]*big.Int{
				ledger.AssetETH:  ethCap,
				ledger.AssetUSDC: big.NewInt(10_000_000_000),
				ledger.AssetWBTC: big.NewInt(25_000_000),
				ledger.AssetUSDT: big.NewInt(10_000_000_000),
			},
			HotDailyCaps: map[string]*big.Int{
				ledger.AssetETH:  ethDaily,
				ledger.AssetUSDC: big.NewInt(100_000_000_000),
				ledger.AssetWBTC: big.NewInt(100_000_000),
				ledger.AssetUSDT: big.NewInt(100_000_000_000),
			},
		},
		rpcPool,
		evm.NewPostgresOutboxStore(pool),
		ledger.NewPostgresStore(pool),
		metrics.Default,
	)

	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Default.Handler())
	metricsSrv := &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("relayer metrics server error", zap.Error(err))
		}
	}()

	logger.Info("relayer started")
	_ = relayer.Run(ctx, 1*time.Second)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = metricsSrv.Shutdown(shutdownCtx)
}
