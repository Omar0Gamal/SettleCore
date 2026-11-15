package config

import (
	"os"
	"strconv"
)

// Config holds runtime configuration loaded from environment variables with defaults matching .env.example.
type Config struct {
	// PostgreSQL 18
	DatabaseURL string

	// Engine & WAL
	EngineWALDir            string
	EngineSnapshotDir       string
	EngineWALFsyncMode      string
	EngineMaxRestingOrders  int
	EngineCircuitBreakerBps int64

	// Fees (in basis points, 1 bps = 0.01%)
	MakerFeeBps int64
	TakerFeeBps int64

	// EVM & Custody
	EVMChainID               int64
	EVMPrimaryRPCURL         string
	EVMFallbackRPCURL        string
	EVMConfirmationsRequired int64
	EVMReorgWindowBlocks     int64
	EscrowVaultAddress       string
	USDCTokenAddress         string
	WBTCTokenAddress         string
	USDTTokenAddress         string

	// Keys (local Anvil defaults only; override in production)
	DeployerPrivateKey   string
	HotSignerPrivateKey  string
	ColdSignerPrivateKey string
	RelayerPrivateKey    string

	// Gateway, Market Maker & Telemetry
	GatewayHTTPAddr           string
	GatewayMetricsAddr        string
	MetricsAddr               string
	ChaosAPIEnabled           bool
	GatewayHTTPURL            string
	GatewayWSURL              string
	MarketMakerBinanceWS      string
	MarketMakerSpreadBps      int64
	ReconcilerIntervalSec     int
	MerkleSnapshotIntervalSec int
}

// Load reads environment variables and applies sane defaults matching .env.example.
func Load() Config {
	return Config{
		DatabaseURL: getEnv("DATABASE_URL", "postgres://settlecore:settlecore_local_dev@localhost:5432/settlecore?sslmode=disable"),

		EngineWALDir:            getEnv("ENGINE_WAL_DIR", "./data/wal"),
		EngineSnapshotDir:       getEnv("ENGINE_SNAPSHOT_DIR", "./data/snapshots"),
		EngineWALFsyncMode:      getEnv("ENGINE_WAL_FSYNC_MODE", "buffered"),
		EngineMaxRestingOrders:  getEnvInt("ENGINE_MAX_RESTING_ORDERS", 1048576),
		EngineCircuitBreakerBps: getEnvInt64("ENGINE_CIRCUIT_BREAKER_BPS", 1000),

		MakerFeeBps: getEnvInt64("MAKER_FEE_BPS", 2),
		TakerFeeBps: getEnvInt64("TAKER_FEE_BPS", 10),

		EVMChainID:               getEnvInt64("EVM_CHAIN_ID", 31337),
		EVMPrimaryRPCURL:         getEnv("EVM_PRIMARY_RPC_URL", "http://localhost:8545"),
		EVMFallbackRPCURL:        getEnv("EVM_FALLBACK_RPC_URL", "http://localhost:8546"),
		EVMConfirmationsRequired: getEnvInt64("EVM_CONFIRMATIONS_REQUIRED", 3),
		EVMReorgWindowBlocks:     getEnvInt64("EVM_REORG_WINDOW_BLOCKS", 64),
		EscrowVaultAddress:       getEnv("ESCROW_VAULT_ADDRESS", "0x5FbDB2315678afecb367f032d93F642f64180aa3"),
		USDCTokenAddress:         getEnv("USDC_TOKEN_ADDRESS", "0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512"),
		WBTCTokenAddress:         getEnv("WBTC_TOKEN_ADDRESS", "0x9fE46736679d2D9a65F0992F2272dE9f3c7fa6e0"),
		USDTTokenAddress:         getEnv("USDT_TOKEN_ADDRESS", "0xCf7Ed3AccA5a467e9e704C703E8D87F634fB0Fc9"),

		DeployerPrivateKey:   getEnv("DEPLOYER_PRIVATE_KEY", "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"),
		HotSignerPrivateKey:  getEnv("HOT_SIGNER_PRIVATE_KEY", "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"),
		ColdSignerPrivateKey: getEnv("COLD_SIGNER_PRIVATE_KEY", "0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"),
		RelayerPrivateKey:    getEnv("RELAYER_PRIVATE_KEY", "0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6"),

		GatewayHTTPAddr:           getEnv("GATEWAY_HTTP_ADDR", ":8080"),
		GatewayMetricsAddr:        getEnv("GATEWAY_METRICS_ADDR", ":9091"),
		MetricsAddr:               getEnv("METRICS_ADDR", ":9092"),
		ChaosAPIEnabled:           getEnvBool("CHAOS_API_ENABLED", true),
		GatewayHTTPURL:            getEnv("GATEWAY_HTTP_URL", "http://localhost:8080"),
		GatewayWSURL:              getEnv("GATEWAY_WS_URL", "ws://localhost:8080/v1/ws"),
		MarketMakerBinanceWS:      getEnv("MARKET_MAKER_BINANCE_WS", "wss://stream.binance.com:9443/ws"),
		MarketMakerSpreadBps:      getEnvInt64("MARKET_MAKER_SPREAD_BPS", 8),
		ReconcilerIntervalSec:     getEnvInt("RECONCILER_INTERVAL_SEC", 60),
		MerkleSnapshotIntervalSec: getEnvInt("MERKLE_SNAPSHOT_INTERVAL_SEC", 3600),
	}
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
