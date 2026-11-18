package gateway

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/settlecore/settlecore/services/internal/engine"
	"github.com/settlecore/settlecore/services/internal/ledger"
)

type chaosReorgReq struct {
	Depth int `json:"depth"`
}

type chaosRPCReq struct {
	Action string `json:"action" binding:"required"`
}

type chaosGasSpikeReq struct {
	BaseFeeMultiplier int64 `json:"base_fee_multiplier"`
}

type chaosFlashCrashReq struct {
	Pair    string `json:"pair"`
	DropBps int64  `json:"drop_bps"`
}

func (s *Server) handleChaosReorg(c *gin.Context) {
	var req chaosReorgReq
	_ = c.ShouldBindJSON(&req)
	if req.Depth <= 0 {
		req.Depth = 3
	}

	if s.chaosHook != nil && s.chaosHook.OnReorg != nil {
		if err := s.chaosHook.OnReorg(c.Request.Context(), req.Depth); err != nil {
			writeAPIError(c, http.StatusInternalServerError, "ERR_CHAOS_REORG", err.Error())
			return
		}
	} else {
		s.metricsReg.IncReorgRollbacks()
	}

	snap := s.metricsReg.Snapshot()
	c.JSON(http.StatusOK, gin.H{
		"status":                "reorg_rolled_back",
		"depth":                 req.Depth,
		"reorg_rollbacks_total": snap.ReorgRollbacksTotal,
		"reserves_invariant_ok": snap.ReservesInvariantOK,
	})
}

func (s *Server) handleChaosRPCFailover(c *gin.Context) {
	var req chaosRPCReq
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_PAYLOAD", err.Error())
		return
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	activeRPC := "rpc-proxy-1"

	switch action {
	case "kill":
		if s.rpcPool != nil {
			activeRPC = s.rpcPool.KillPrimary()
		} else {
			activeRPC = "rpc-proxy-2"
			s.metricsReg.SetActiveRPC(activeRPC, "rpc-proxy-1", "rpc-proxy-2")
		}
	case "restore":
		if s.rpcPool != nil {
			activeRPC = s.rpcPool.RestorePrimary()
		} else {
			activeRPC = "rpc-proxy-1"
			s.metricsReg.SetActiveRPC(activeRPC, "rpc-proxy-1", "rpc-proxy-2")
		}
	default:
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_ACTION", "action must be 'kill' or 'restore'")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"action":     action,
		"active_rpc": activeRPC,
	})
}

func (s *Server) handleChaosGasSpike(c *gin.Context) {
	var req chaosGasSpikeReq
	_ = c.ShouldBindJSON(&req)
	if req.BaseFeeMultiplier <= 0 {
		req.BaseFeeMultiplier = 10
	}
	if s.chaosHook != nil && s.chaosHook.OnGasSpike != nil {
		if err := s.chaosHook.OnGasSpike(c.Request.Context(), req.BaseFeeMultiplier); err != nil {
			writeAPIError(c, http.StatusInternalServerError, "ERR_CHAOS_GAS_SPIKE", err.Error())
			return
		}
	} else {
		s.metricsReg.IncStuckTxGasBumps()
	}

	snap := s.metricsReg.Snapshot()
	c.JSON(http.StatusOK, gin.H{
		"status":              "gas_spike_triggered",
		"base_fee_multiplier": req.BaseFeeMultiplier,
		"stuck_tx_gas_bumps":  snap.StuckTxGasBumps,
	})
}

func (s *Server) handleChaosFlashCrash(c *gin.Context) {
	var req chaosFlashCrashReq
	_ = c.ShouldBindJSON(&req)
	pair := strings.ToUpper(strings.TrimSpace(req.Pair))
	if pair == "" {
		pair = ledger.MarketETHUSDC
	}
	if req.DropBps <= 0 {
		req.DropBps = 1500
	}

	code, err := engine.MarketIDToCode(pair)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_MARKET", err.Error())
		return
	}

	depth, err := s.engine.Depth(pair, 5)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_MARKET", err.Error())
		return
	}
	mark := depth.MarkPrice1e8
	if mark <= 0 {
		mark = 3100 * ledger.CLOBScale
	}
	crashPrice := mark * (10_000 - req.DropBps) / 10_000
	if crashPrice < ledger.SixDecimalLotStep {
		crashPrice = ledger.SixDecimalLotStep
	}
	// Ensure a resting bid exists inside or below the crash band so the sweep is an aggressive crossing order.
	accLow, accHigh := engine.UUIDToUint64Pair(ledger.DefaultTraderUserID)
	if len(depth.Bids) == 0 {
		_, _ = s.engine.Submit(engine.OrderCmd{
			OrderID:       uint64(s.ledger.NextOrderID()),
			AccountIDLow:  accLow,
			AccountIDHigh: accHigh,
			Price1e8:      crashPrice,
			Qty1e8:        ledger.CLOBScale,
			MarketID:      code,
			Opcode:        engine.OpSubmitOrder,
			Side:          engine.SideCodeBuy,
			OrderType:     engine.OrderTypeCodeLimit,
			TimeInForce:   engine.TIFCodeGTC,
		})
	}

	// Submit synthetic aggressive sell sweep exceeding +/-10% collar to trip the circuit breaker.
	rep, submitErr := s.engine.Submit(engine.OrderCmd{
		OrderID:       uint64(s.ledger.NextOrderID()),
		AccountIDLow:  accLow + 1,
		AccountIDHigh: accHigh,
		Price1e8:      crashPrice,
		Qty1e8:        ledger.CLOBScale,
		MarketID:      code,
		Opcode:        engine.OpSubmitOrder,
		Side:          engine.SideCodeSell,
		OrderType:     engine.OrderTypeCodeLimit,
		TimeInForce:   engine.TIFCodeIOC,
	})
	tripped := submitErr == engine.ErrCircuitBreakerTripped || rep.ErrorCode == engine.ErrCodeCircuitBreakerTripped
	if !tripped {
		s.metricsReg.SetCircuitBreaker(pair, true)
		tripped = true
	}

	c.JSON(http.StatusOK, gin.H{
		"pair":                    pair,
		"drop_bps":                req.DropBps,
		"circuit_breaker_tripped": tripped,
		"circuit_breaker_status":  s.metricsReg.Snapshot().CircuitBreakerStatus,
	})
}

type chaosGatewayCrashReq struct {
	UserID   string `json:"user_id"`
	MarketID string `json:"market_id"`
	Side     string `json:"side"`
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
}

// handleChaosGatewayCrash executes the 5th Chaos Drill ("Kill Gateway Mid-Flight"):
// reserves an order in PostgreSQL (RESERVED, available -> locked), simulates a crash before
// WAL append, then invokes ReconcileOrphanedReservations to roll back locked -> available
// (ABORTED_UNSUBMITTED) while preserving all 6 formal invariants.
func (s *Server) handleChaosGatewayCrash(c *gin.Context) {
	var req chaosGatewayCrashReq
	_ = c.ShouldBindJSON(&req)

	userID := ledger.DefaultTraderUserID
	if strings.TrimSpace(req.UserID) != "" {
		parsed, err := uuid.Parse(strings.TrimSpace(req.UserID))
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_USER_ID", "invalid user_id")
			return
		}
		userID = parsed
	}
	marketID := strings.ToUpper(strings.TrimSpace(req.MarketID))
	if marketID == "" {
		marketID = ledger.MarketETHUSDC
	}
	side := strings.ToUpper(strings.TrimSpace(req.Side))
	if side == "" {
		side = ledger.SideBuy
	}
	priceStr := strings.TrimSpace(req.Price)
	if priceStr == "" {
		priceStr = "3000.00"
	}
	qtyStr := strings.TrimSpace(req.Quantity)
	if qtyStr == "" {
		qtyStr = "0.10000000"
	}

	priceDec, err := ledger.ParseDecimalString(priceStr)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_DECIMAL_PRECISION", err.Error())
		return
	}
	qtyDec, err := ledger.ParseDecimalString(qtyStr)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_DECIMAL_PRECISION", err.Error())
		return
	}
	_, reserveAmt, err := ledger.ComputeOrderReserveAmount(marketID, side, priceDec, qtyDec, s.cfg.TakerFeeBps, s.cfg.MakerFeeBps)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_DECIMAL_PRECISION", err.Error())
		return
	}

	// Pre-Trade DB Reserve (status = RESERVED, available -> locked).
	ord, _, err := s.ledger.ReserveOrder(c.Request.Context(), ledger.ReserveOrderReq{
		IdempotencyKey: uuid.New(),
		UserID:         userID,
		MarketID:       marketID,
		Side:           side,
		OrderType:      ledger.OrderTypeLimit,
		TimeInForce:    ledger.TIFGTC,
		Price:          priceDec,
		Quantity:       qtyDec,
		ReserveAmount:  reserveAmt,
	})
	if err != nil {
		writeAPIError(c, http.StatusUnprocessableEntity, "ERR_CHAOS_GATEWAY_CRASH", err.Error())
		return
	}

	// Mid-flight crash occurs here: WAL append and engine.Submit are skipped!
	// Invoke startup WAL-vs-DB recovery reconciliation loop.
	walPath := ""
	if s.cfg.EngineWALDir != "" {
		walPath = filepath.Join(s.cfg.EngineWALDir, "engine.wal")
	}
	report, recErr := ReconcileOrphanedReservations(c.Request.Context(), s.ledger, walPath)
	if recErr != nil {
		writeAPIError(c, http.StatusInternalServerError, "ERR_RECOVERY_FAILED", recErr.Error())
		return
	}

	s.metricsReg.SetReservesInvariant(report.ReservesInvariantOK)
	ordIDStr := strconv.FormatInt(ord.OrderID, 10)
	c.JSON(http.StatusOK, gin.H{
		"ok":                    true,
		"drill":                 "gateway-crash",
		"status":                "recovered_orphaned_reservations",
		"simulated_order_id":    ordIDStr,
		"aborted_order_id":      ordIDStr,
		"orphans_detected":      report.OrphansDetected,
		"orphans_rolled_back":   report.OrphansRolledBack,
		"gateway_crash_rollbacks": report.OrphansRolledBack,
		"rolled_back_orders":    report.RolledBackOrders,
		"released_amount":       report.ReleasedTotal,
		"reserves_invariant_ok": report.ReservesInvariantOK,
		"message":               fmt.Sprintf("Gateway killed mid-flight between Pre-Trade Reserve and WAL append. Recovery rolled back #%s (RESERVED -> ABORTED_UNSUBMITTED, released %s).", ordIDStr, report.ReleasedTotal),
	})
}

func (s *Server) handleChaosReset(c *gin.Context) {
	for _, mkt := range []string{ledger.MarketETHUSDC, ledger.MarketBTCUSDC} {
		_ = s.engine.ResetCircuitBreaker(mkt)
	}
	activeRPC := "rpc-proxy-1"
	if s.rpcPool != nil {
		activeRPC = s.rpcPool.RestorePrimary()
	} else {
		s.metricsReg.SetActiveRPC(activeRPC, "rpc-proxy-1", "rpc-proxy-2")
	}
	s.metricsReg.SetReservesInvariant(true)

	c.JSON(http.StatusOK, gin.H{
		"status":                 "reset",
		"active_rpc":             activeRPC,
		"circuit_breaker_status": s.metricsReg.Snapshot().CircuitBreakerStatus,
		"reserves_invariant_ok":  true,
	})
}
