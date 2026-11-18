package gateway

import (
	"context"
	"net/http"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/settlecore/settlecore/services/internal/config"
	"github.com/settlecore/settlecore/services/internal/engine"
	"github.com/settlecore/settlecore/services/internal/evm"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/metrics"
	"github.com/settlecore/settlecore/services/internal/reconciler"
)

// ChaosHooks allows integrating live Anvil/Relayer chaos triggers into HTTP chaos endpoints.
type ChaosHooks struct {
	OnReorg    func(ctx context.Context, depth int) error
	OnGasSpike func(ctx context.Context, multiplier int64) error
}

// Server exposes the Gin REST API, Prometheus /metrics, Chaos controls, and WebSocket hub.
type Server struct {
	cfg           config.Config
	router        *gin.Engine
	ledger        ledger.Store
	engine        *engine.Bridge
	wsHub         *WSHub
	rpcPool       *evm.RPCPool
	relayer       *evm.Relayer
	outbox        evm.OutboxStore
	snapStore     reconciler.SnapshotStore
	metricsReg    *metrics.Registry
	chaosHook     *ChaosHooks
	orderVerifier *OrderEIP712Verifier
	orderNonceMu  sync.Mutex
	orderNonces   map[common.Address]uint64
}

// Dependencies holds the components wired into the Gateway HTTP/WS server.
type Dependencies struct {
	Config     config.Config
	Ledger     ledger.Store
	Engine     *engine.Bridge
	RPCPool    *evm.RPCPool
	Relayer    *evm.Relayer
	Outbox     evm.OutboxStore
	SnapStore  reconciler.SnapshotStore
	MetricsReg *metrics.Registry
	ChaosHooks *ChaosHooks
}

// NewServer constructs and configures the Gateway Gin router and WebSocket hub.
func NewServer(deps Dependencies) *Server {
	if deps.MetricsReg == nil {
		deps.MetricsReg = metrics.Default
	}
	if deps.SnapStore == nil {
		deps.SnapStore = reconciler.NewMemorySnapshotStore()
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())

	chainID := deps.Config.EVMChainID
	if chainID <= 0 {
		chainID = 31337
	}
	s := &Server{
		cfg:           deps.Config,
		router:        r,
		ledger:        deps.Ledger,
		engine:        deps.Engine,
		rpcPool:       deps.RPCPool,
		relayer:       deps.Relayer,
		outbox:        deps.Outbox,
		snapStore:     deps.SnapStore,
		metricsReg:    deps.MetricsReg,
		chaosHook:     deps.ChaosHooks,
		orderVerifier: NewOrderEIP712Verifier(chainID, common.HexToAddress(deps.Config.EscrowVaultAddress)),
		orderNonces:   make(map[common.Address]uint64),
	}

	s.wsHub = NewWSHub(func(ctx context.Context, userID uuid.UUID) {
		accLow, accHigh := engine.UUIDToUint64Pair(userID)
		for _, code := range []uint16{engine.MarketCodeETHUSDC, engine.MarketCodeBTCUSDC} {
			mkt, _ := engine.CodeToMarketID(code)
			rep, err := s.engine.Submit(engine.OrderCmd{
				AccountIDLow:  accLow,
				AccountIDHigh: accHigh,
				MarketID:      code,
				Opcode:        engine.OpCancelAllAccount,
			})
			if err != nil {
				continue
			}
			for _, oid := range rep.CanceledIDs {
				_, _ = s.ledger.ReleaseOrderReservation(ctx, int64(oid), int64(rep.WalSeq), ledger.OrderStatusCanceled)
			}
			if len(rep.CanceledIDs) > 0 {
				s.broadcastMarketUpdates(mkt, "", rep)
			}
		}
	})

	s.registerRoutes()
	return s
}

// Router returns the underlying *gin.Engine (for httptest in unit/integration tests).
func (s *Server) Router() *gin.Engine {
	return s.router
}

// WSHub returns the active WebSocket hub.
func (s *Server) WSHub() *WSHub {
	return s.wsHub
}

func (s *Server) registerRoutes() {
	s.router.GET("/healthz", s.handleHealthz)
	s.router.GET("/metrics", gin.WrapH(s.metricsReg.Handler()))
	s.router.GET("/v1/ws", func(c *gin.Context) {
		s.wsHub.ServeWS(c.Writer, c.Request)
	})

	v1 := s.router.Group("/v1")
	{
		v1.POST("/orders", s.handleSubmitOrder)
		v1.DELETE("/orders/:id", s.handleCancelOrder)
		v1.GET("/orders", s.handleListOrders)
		v1.GET("/market/depth", s.handleMarketDepth)
		v1.GET("/market/trades", s.handleMarketTrades)
		v1.GET("/accounts/balances", s.handleGetBalances)
		v1.POST("/withdrawals", s.handleRequestWithdrawal)
		v1.GET("/solvency/proof", s.handleSolvencyProof)

		if s.cfg.ChaosAPIEnabled {
			chaos := v1.Group("/chaos")
			chaos.POST("/reorg", s.handleChaosReorg)
			chaos.POST("/rpc-failover", s.handleChaosRPCFailover)
			chaos.POST("/gas-spike", s.handleChaosGasSpike)
			chaos.POST("/flash-crash", s.handleChaosFlashCrash)
			chaos.POST("/gateway-crash", s.handleChaosGatewayCrash)
			chaos.POST("/reset", s.handleChaosReset)
		}
	}
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" || origin == "http://localhost:3000" || origin == "http://127.0.0.1:3000" {
			if origin == "" {
				origin = "http://localhost:3000"
			}
			c.Header("Access-Control-Allow-Origin", origin)
		} else {
			c.Header("Access-Control-Allow-Origin", "http://localhost:3000")
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-SettleCore-User-Id, Idempotency-Key")
		c.Header("Access-Control-Allow-Credentials", "true")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
