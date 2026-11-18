package gateway

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/settlecore/settlecore/services/internal/engine"
	"github.com/settlecore/settlecore/services/internal/evm"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/reconciler"
	"github.com/shopspring/decimal"
)

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeAPIError(c *gin.Context, status int, code, msg string) {
	c.JSON(status, apiError{Code: code, Message: msg})
}

func parseUserID(c *gin.Context) (uuid.UUID, bool) {
	raw := strings.TrimSpace(c.GetHeader("X-SettleCore-User-Id"))
	if raw == "" {
		raw = strings.TrimSpace(c.Query("user_id"))
	}
	if raw == "" {
		return ledger.DefaultTraderUserID, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_USER_ID", "X-SettleCore-User-Id must be a valid UUIDv4")
		return uuid.Nil, false
	}
	return id, true
}

func parseIdempotencyKey(c *gin.Context) (uuid.UUID, bool) {
	raw := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if raw == "" {
		return uuid.New(), true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_IDEMPOTENCY_KEY", "Idempotency-Key must be a valid UUIDv4")
		return uuid.Nil, false
	}
	return id, true
}

// SubmitOrderRequest describes the JSON body for POST /v1/orders with optional EIP-712 signature fields.
type SubmitOrderRequest struct {
	MarketID      string  `json:"market_id" binding:"required"`
	Side          string  `json:"side" binding:"required"`
	OrderType     string  `json:"order_type" binding:"required"`
	TimeInForce   string  `json:"time_in_force" binding:"required"`
	STPMode       string  `json:"stp_mode"`
	Price         string  `json:"price"`
	Quantity      string  `json:"quantity" binding:"required"`
	Signature     string  `json:"signature,omitempty"`
	SignerAddress string  `json:"signer_address,omitempty"`
	OrderNonce    *uint64 `json:"order_nonce,omitempty"`
	Deadline      *int64  `json:"deadline,omitempty"`
}

type fillResp struct {
	TradeID       string `json:"trade_id"`
	MakerOrderID  string `json:"maker_order_id"`
	Price         string `json:"price"`
	Quantity      string `json:"quantity"`
	QuoteNotional string `json:"quote_notional"`
}

// OrderLifecycleTrace captures microsecond pipeline timestamps across Pre-Trade Reserve -> WAL -> CLOB -> Ledger.
type OrderLifecycleTrace struct {
	ReservedAtNs    int64  `json:"reserved_at_ns"`
	WalAppendedAtNs int64  `json:"wal_appended_at_ns"`
	MatchedAtNs     int64  `json:"matched_at_ns"`
	MatchLatencyNs  int64  `json:"match_latency_ns"`
	WalSeq          uint64 `json:"wal_seq"`
	JournalTxID     string `json:"journal_tx_id"`
	EIP712Verified  bool   `json:"eip712_verified"`
}

// SubmitOrderResponse describes the JSON response for POST /v1/orders.
type SubmitOrderResponse struct {
	OrderID           string               `json:"order_id"`
	IdempotencyKey    string               `json:"idempotency_key"`
	WalSeq            uint64               `json:"wal_seq"`
	MarketID          string               `json:"market_id"`
	Status            string               `json:"status"`
	FilledQuantity    string               `json:"filled_quantity"`
	RemainingQuantity string               `json:"remaining_quantity"`
	MatchLatencyNs    int64                `json:"match_latency_ns"`
	Fills             []fillResp           `json:"fills"`
	Lifecycle         *OrderLifecycleTrace `json:"lifecycle,omitempty"`
}

type journalRefLookup interface {
	GetLatestJournalTxByReference(ctx context.Context, refID string) (uuid.UUID, error)
}

var _ = filepath.Join

func (s *Server) handleHealthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleSubmitOrder(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	idemKey, ok := parseIdempotencyKey(c)
	if !ok {
		return
	}

	var req SubmitOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_PAYLOAD", err.Error())
		return
	}

	marketID := strings.ToUpper(strings.TrimSpace(req.MarketID))
	side := strings.ToUpper(strings.TrimSpace(req.Side))
	orderType := strings.ToUpper(strings.TrimSpace(req.OrderType))
	tif := strings.ToUpper(strings.TrimSpace(req.TimeInForce))
	stpMode := strings.ToUpper(strings.TrimSpace(req.STPMode))
	if stpMode == "" {
		stpMode = "CANCEL_NEWEST"
	}

	marketCode, err := engine.MarketIDToCode(marketID)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_MARKET", err.Error())
		return
	}

	qtyDec, err := ledger.ParseDecimalString(req.Quantity)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_DECIMAL_PRECISION", err.Error())
		return
	}
	qty1e8, err := ledger.QtyToCLOBInt64(marketID, qtyDec)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_DECIMAL_PRECISION", err.Error())
		return
	}

	var priceDec decimal.Decimal
	var price1e8 int64
	if orderType == ledger.OrderTypeMarket {
		// For MARKET BUY reserve, anchor at mark price + 10% collar if price omitted or below collar ceiling.
		depth, _ := s.engine.Depth(marketID, 1)
		mark := depth.MarkPrice1e8
		if mark <= 0 {
			mark = 3100 * ledger.CLOBScale
		}
		collarCeiling := ((mark * 110 / 100) / ledger.SixDecimalLotStep) * ledger.SixDecimalLotStep
		if strings.TrimSpace(req.Price) == "" {
			price1e8 = collarCeiling
			priceDec = ledger.CLOBInt64ToHuman(price1e8)
		} else {
			priceDec, err = ledger.ParseDecimalString(req.Price)
			if err != nil {
				writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_DECIMAL_PRECISION", err.Error())
				return
			}
			price1e8, err = ledger.PriceToCLOBInt64(marketID, priceDec)
			if err != nil {
				writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_DECIMAL_PRECISION", err.Error())
				return
			}
			if side == ledger.SideBuy && price1e8 < collarCeiling {
				price1e8 = collarCeiling
				priceDec = ledger.CLOBInt64ToHuman(price1e8)
			}
		}
	} else {
		priceDec, err = ledger.ParseDecimalString(req.Price)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_DECIMAL_PRECISION", err.Error())
			return
		}
		price1e8, err = ledger.PriceToCLOBInt64(marketID, priceDec)
		if err != nil {
			writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_DECIMAL_PRECISION", err.Error())
			return
		}
	}

	var sideCode uint8 = engine.SideCodeBuy
	if side == ledger.SideSell {
		sideCode = engine.SideCodeSell
	}
	var tifCode uint8 = engine.TIFCodeGTC
	switch tif {
	case ledger.TIFIOC:
		tifCode = engine.TIFCodeIOC
	case ledger.TIFFOK:
		tifCode = engine.TIFCodeFOK
	}

	// Optional EIP-712 Order Signature Verification (Upgrade H).
	eip712Verified := false
	if strings.TrimSpace(req.Signature) != "" || strings.TrimSpace(req.SignerAddress) != "" {
		if strings.TrimSpace(req.Signature) == "" || strings.TrimSpace(req.SignerAddress) == "" {
			writeAPIError(c, http.StatusUnauthorized, "ERR_INVALID_ORDER_SIGNATURE", "both signature and signer_address are required for signed orders")
			return
		}
		if !common.IsHexAddress(strings.TrimSpace(req.SignerAddress)) {
			writeAPIError(c, http.StatusUnauthorized, "ERR_INVALID_ORDER_SIGNATURE", "invalid signer_address")
			return
		}
		expectedSigner := common.HexToAddress(strings.TrimSpace(req.SignerAddress))
		if req.OrderNonce == nil {
			writeAPIError(c, http.StatusUnauthorized, "ERR_INVALID_ORDER_SIGNATURE", "order_nonce is required for signed orders")
			return
		}
		var deadline int64
		if req.Deadline != nil {
			deadline = *req.Deadline
		}
		if deadline > 0 && time.Now().Unix() > deadline {
			writeAPIError(c, http.StatusUnauthorized, "ERR_ORDER_EXPIRED", "signed order deadline has expired")
			return
		}

		payload := SignedOrderPayload{
			Market:   marketID,
			Side:     sideCode,
			Price1e8: price1e8,
			Qty1e8:   qty1e8,
			TIF:      tifCode,
			Nonce:    *req.OrderNonce,
			Deadline: deadline,
		}
		recovered, sigErr := s.orderVerifier.RecoverOrderSigner(payload, req.Signature)
		if sigErr != nil || recovered != expectedSigner {
			writeAPIError(c, http.StatusUnauthorized, "ERR_INVALID_ORDER_SIGNATURE", "EIP-712 order signature verification failed")
			return
		}

		s.orderNonceMu.Lock()
		lastNonce, seen := s.orderNonces[recovered]
		if seen && *req.OrderNonce <= lastNonce {
			s.orderNonceMu.Unlock()
			writeAPIError(c, http.StatusUnauthorized, "ERR_ORDER_NONCE_REPLAY", "order_nonce must be strictly monotonic")
			return
		}
		s.orderNonces[recovered] = *req.OrderNonce
		s.orderNonceMu.Unlock()
		eip712Verified = true
	}

	_, reserveAmt, err := ledger.ComputeOrderReserveAmount(marketID, side, priceDec, qtyDec, s.cfg.TakerFeeBps, s.cfg.MakerFeeBps)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_DECIMAL_PRECISION", err.Error())
		return
	}

	reserveCtx, cancel := context.WithTimeout(c.Request.Context(), 250*time.Millisecond)
	defer cancel()

	ord, idemHit, err := s.ledger.ReserveOrder(reserveCtx, ledger.ReserveOrderReq{
		IdempotencyKey: idemKey,
		UserID:         userID,
		MarketID:       marketID,
		Side:           side,
		OrderType:      orderType,
		TimeInForce:    tif,
		Price:          priceDec,
		Quantity:       qtyDec,
		ReserveAmount:  reserveAmt,
	})
	reservedAtNs := time.Now().UnixNano()
	if err != nil {
		switch {
		case errors.Is(err, ledger.ErrIdempotencyConflict):
			writeAPIError(c, http.StatusConflict, "ERR_IDEMPOTENCY_CONFLICT", err.Error())
		case errors.Is(err, ledger.ErrInsufficientAvailable):
			writeAPIError(c, http.StatusUnprocessableEntity, "ERR_INSUFFICIENT_AVAILABLE_BALANCE", err.Error())
		default:
			writeAPIError(c, http.StatusInternalServerError, "ERR_LEDGER_RESERVE", err.Error())
		}
		return
	}

	journalTxID := ""
	if lookup, ok := s.ledger.(journalRefLookup); ok {
		if jtx, jErr := lookup.GetLatestJournalTxByReference(c.Request.Context(), fmt.Sprintf("order:%d", ord.OrderID)); jErr == nil && jtx != uuid.Nil {
			journalTxID = jtx.String()
		}
	}

	if idemHit {
		rem := ord.Quantity.Sub(ord.FilledQuantity)
		c.JSON(http.StatusCreated, SubmitOrderResponse{
			OrderID:           strconv.FormatInt(ord.OrderID, 10),
			IdempotencyKey:    ord.IdempotencyKey.String(),
			WalSeq:            uint64(ord.WalSeq),
			MarketID:          ord.MarketID,
			Status:            ord.Status,
			FilledQuantity:    ord.FilledQuantity.StringFixed(8),
			RemainingQuantity: rem.StringFixed(8),
			Fills:             []fillResp{},
			Lifecycle: &OrderLifecycleTrace{
				ReservedAtNs:    ord.CreatedAt.UnixNano(),
				WalAppendedAtNs: ord.UpdatedAt.UnixNano(),
				MatchedAtNs:     ord.UpdatedAt.UnixNano(),
				MatchLatencyNs:  0,
				WalSeq:          uint64(ord.WalSeq),
				JournalTxID:     journalTxID,
				EIP712Verified:  eip712Verified,
			},
		})
		return
	}

	var otCode uint8 = engine.OrderTypeCodeLimit
	if orderType == ledger.OrderTypeMarket {
		otCode = engine.OrderTypeCodeMarket
	}
	var stpCode uint8 = engine.STPCodeCancelNewest
	switch stpMode {
	case "CANCEL_OLDEST":
		stpCode = engine.STPCodeCancelOldest
	case "CANCEL_BOTH":
		stpCode = engine.STPCodeCancelBoth
	}

	accLow, accHigh := engine.UUIDToUint64Pair(userID)
	walAppendedAtNs := time.Now().UnixNano()
	if walAppendedAtNs < reservedAtNs {
		walAppendedAtNs = reservedAtNs
	}
	rep, engErr := s.engine.Submit(engine.OrderCmd{
		OrderID:       uint64(ord.OrderID),
		AccountIDLow:  accLow,
		AccountIDHigh: accHigh,
		Price1e8:      price1e8,
		Qty1e8:        qty1e8,
		TimestampNs:   uint64(walAppendedAtNs),
		MarketID:      marketCode,
		Opcode:        engine.OpSubmitOrder,
		Side:          sideCode,
		OrderType:     otCode,
		TimeInForce:   tifCode,
		STPMode:       stpCode,
	})
	matchedAtNs := time.Now().UnixNano()
	if matchedAtNs < walAppendedAtNs {
		matchedAtNs = walAppendedAtNs
	}
	if engErr != nil {
		// Release pre-trade database reservation immediately on rejection.
		_, _ = s.ledger.ReleaseOrderReservation(c.Request.Context(), ord.OrderID, int64(rep.WalSeq), ledger.OrderStatusRejectedReleased)
		switch {
		case errors.Is(engErr, engine.ErrEngineBackpressure):
			writeAPIError(c, http.StatusTooManyRequests, "ERR_ENGINE_BACKPRESSURE", engErr.Error())
		case errors.Is(engErr, engine.ErrCircuitBreakerTripped):
			writeAPIError(c, http.StatusUnprocessableEntity, "ERR_CIRCUIT_BREAKER_TRIPPED", engErr.Error())
		case errors.Is(engErr, engine.ErrOrderPoolExhausted):
			writeAPIError(c, http.StatusServiceUnavailable, "ERR_ORDER_POOL_EXHAUSTED", engErr.Error())
		case errors.Is(engErr, engine.ErrSelfTradePrevented):
			writeAPIError(c, http.StatusUnprocessableEntity, "ERR_SELF_TRADE_PREVENTED", engErr.Error())
		default:
			writeAPIError(c, http.StatusInternalServerError, "ERR_ENGINE_SUBMIT", engErr.Error())
		}
		return
	}

	// Handle FOK unfilled or IOC/GTC status and apply synchronous batch settlement when configured.
	if rep.ErrorCode == engine.ErrCodeFOKUnfilled || (rep.Status == engine.ExecStatusCanceled && rep.FilledQty1e8 == 0) {
		_, _ = s.ledger.ReleaseOrderReservation(c.Request.Context(), ord.OrderID, int64(rep.WalSeq), ledger.OrderStatusRejectedReleased)
	} else {
		_ = s.ledger.UpdateOrderStatus(c.Request.Context(), ord.OrderID, int64(rep.WalSeq), mapExecStatus(rep.Status))
	}

	if err := s.settleExecReport(c.Request.Context(), marketID, side, rep); err != nil {
		writeAPIError(c, http.StatusInternalServerError, "ERR_SETTLEMENT", err.Error())
		return
	}

	fills := make([]fillResp, 0, len(rep.Fills))
	for _, f := range rep.Fills {
		fills = append(fills, fillResp{
			TradeID:       strconv.FormatUint(f.TradeID, 10),
			MakerOrderID:  strconv.FormatUint(f.MakerOrderID, 10),
			Price:         ledger.CLOBInt64ToHuman(f.ExecPrice1e8).StringFixed(8),
			Quantity:      ledger.CLOBInt64ToHuman(f.FillQty1e8).StringFixed(8),
			QuoteNotional: ledger.CLOBInt64ToHuman(f.QuoteNotional1e8).StringFixed(8),
		})
	}

	if len(rep.Fills) > 0 {
		if lookup, ok := s.ledger.(journalRefLookup); ok {
			if jtx, jErr := lookup.GetLatestJournalTxByReference(c.Request.Context(), fmt.Sprintf("trade:%d", rep.Fills[0].TradeID)); jErr == nil && jtx != uuid.Nil {
				journalTxID = jtx.String()
			}
		}
	}

	s.broadcastMarketUpdates(marketID, side, rep)

	c.JSON(http.StatusCreated, SubmitOrderResponse{
		OrderID:           strconv.FormatInt(ord.OrderID, 10),
		IdempotencyKey:    ord.IdempotencyKey.String(),
		WalSeq:            rep.WalSeq,
		MarketID:          marketID,
		Status:            mapExecStatus(rep.Status),
		FilledQuantity:    ledger.CLOBInt64ToHuman(rep.FilledQty1e8).StringFixed(8),
		RemainingQuantity: ledger.CLOBInt64ToHuman(rep.RemainingQty1e8).StringFixed(8),
		MatchLatencyNs:    rep.LatencyNs,
		Fills:             fills,
		Lifecycle: &OrderLifecycleTrace{
			ReservedAtNs:    reservedAtNs,
			WalAppendedAtNs: walAppendedAtNs,
			MatchedAtNs:     matchedAtNs,
			MatchLatencyNs:  rep.LatencyNs,
			WalSeq:          rep.WalSeq,
			JournalTxID:     journalTxID,
			EIP712Verified:  eip712Verified,
		},
	})
}

func (s *Server) settleExecReport(ctx context.Context, marketID, takerSide string, rep engine.ExecReport) error {
	if len(rep.Fills) == 0 && len(rep.CanceledIDs) == 0 {
		return nil
	}
	fills := make([]ledger.SettlementFill, 0, len(rep.Fills))
	for _, f := range rep.Fills {
		makerUID := engine.Uint64PairToUUID(f.MakerAccountLow, f.MakerAccountHigh)
		takerOrd, err := s.ledger.GetOrder(ctx, int64(f.TakerOrderID))
		if err != nil {
			return err
		}
		makerFee1e8, err := ledger.ComputeFeeCeil1e8(f.QuoteNotional1e8, s.cfg.MakerFeeBps)
		if err != nil {
			return err
		}
		takerFee1e8, err := ledger.ComputeFeeCeil1e8(f.QuoteNotional1e8, s.cfg.TakerFeeBps)
		if err != nil {
			return err
		}
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
		ord, err := s.ledger.GetOrder(ctx, int64(cid))
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
	_, err := s.ledger.ApplySettlementBatch(ctx, ledger.SettlementBatch{
		MarketID:  marketID,
		MaxWalSeq: int64(rep.WalSeq),
		Fills:     fills,
		Cancels:   cancels,
	})
	return err
}

func (s *Server) broadcastMarketUpdates(marketID, takerSide string, rep engine.ExecReport) {
	if s.wsHub == nil {
		return
	}
	depth, err := s.engine.Depth(marketID, 20)
	if err == nil {
		bids := make([][2]string, len(depth.Bids))
		for i, b := range depth.Bids {
			bids[i] = [2]string{ledger.CLOBInt64ToHuman(b.Price1e8).StringFixed(8), ledger.CLOBInt64ToHuman(b.Qty1e8).StringFixed(8)}
		}
		asks := make([][2]string, len(depth.Asks))
		for i, a := range depth.Asks {
			asks[i] = [2]string{ledger.CLOBInt64ToHuman(a.Price1e8).StringFixed(8), ledger.CLOBInt64ToHuman(a.Qty1e8).StringFixed(8)}
		}
		s.wsHub.Broadcast("l2:"+marketID, gin.H{
			"channel":                 "l2:" + marketID,
			"type":                    "l2_diff",
			"wal_seq":                 depth.WalSeq,
			"bids":                    bids,
			"asks":                    asks,
			"circuit_breaker_tripped": depth.CircuitBreakerTripped,
		})
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, f := range rep.Fills {
		s.wsHub.Broadcast("trades:"+marketID, gin.H{
			"channel":     "trades:" + marketID,
			"trade_id":    strconv.FormatUint(f.TradeID, 10),
			"wal_seq":     rep.WalSeq,
			"price":       ledger.CLOBInt64ToHuman(f.ExecPrice1e8).StringFixed(8),
			"quantity":    ledger.CLOBInt64ToHuman(f.FillQty1e8).StringFixed(8),
			"taker_side":  takerSide,
			"executed_at": now,
		})
	}
}

func mapExecStatus(st uint8) string {
	switch st {
	case engine.ExecStatusAccepted:
		return ledger.OrderStatusOpen
	case engine.ExecStatusPartiallyFilled:
		return ledger.OrderStatusPartiallyFilled
	case engine.ExecStatusFilled:
		return ledger.OrderStatusFilled
	case engine.ExecStatusCanceled:
		return ledger.OrderStatusCanceled
	case engine.ExecStatusSTPCanceled, engine.ExecStatusCBRejected:
		return ledger.OrderStatusRejectedReleased
	default:
		return ledger.OrderStatusOpen
	}
}

func (s *Server) handleCancelOrder(c *gin.Context) {
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || orderID <= 0 {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_ORDER_ID", "invalid order_id")
		return
	}
	marketID := strings.ToUpper(strings.TrimSpace(c.Query("market_id")))
	if marketID == "" {
		if ord, err := s.ledger.GetOrder(c.Request.Context(), orderID); err == nil {
			marketID = ord.MarketID
		} else {
			marketID = ledger.MarketETHUSDC
		}
	}
	code, err := engine.MarketIDToCode(marketID)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_MARKET", err.Error())
		return
	}

	rep, err := s.engine.Submit(engine.OrderCmd{
		OrderID:  uint64(orderID),
		MarketID: code,
		Opcode:   engine.OpCancelOrder,
	})
	if err != nil {
		writeAPIError(c, http.StatusInternalServerError, "ERR_ENGINE_CANCEL", err.Error())
		return
	}

	released, err := s.ledger.ReleaseOrderReservation(c.Request.Context(), orderID, int64(rep.WalSeq), ledger.OrderStatusCanceled)
	if err != nil && !errors.Is(err, ledger.ErrOrderNotFound) {
		writeAPIError(c, http.StatusInternalServerError, "ERR_LEDGER_RELEASE", err.Error())
		return
	}
	s.broadcastMarketUpdates(marketID, "", rep)

	c.JSON(http.StatusOK, gin.H{
		"order_id":               strconv.FormatInt(orderID, 10),
		"wal_seq":                rep.WalSeq,
		"status":                 ledger.OrderStatusCanceled,
		"released_locked_amount": released.StringFixed(8),
	})
}

func (s *Server) handleListOrders(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	orders, err := s.ledger.ListOrdersByUser(c.Request.Context(), userID, 100)
	if err != nil {
		writeAPIError(c, http.StatusInternalServerError, "ERR_LIST_ORDERS", err.Error())
		return
	}
	out := make([]gin.H, 0, len(orders))
	for _, o := range orders {
		out = append(out, gin.H{
			"order_id":        strconv.FormatInt(o.OrderID, 10),
			"idempotency_key": o.IdempotencyKey.String(),
			"market_id":       o.MarketID,
			"side":            o.Side,
			"order_type":      o.OrderType,
			"time_in_force":   o.TimeInForce,
			"price":           o.Price.StringFixed(8),
			"quantity":        o.Quantity.StringFixed(8),
			"filled_quantity": o.FilledQuantity.StringFixed(8),
			"locked_amount":   o.LockedAmount.StringFixed(8),
			"wal_seq":         o.WalSeq,
			"status":          o.Status,
			"created_at":      o.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"user_id": userID.String(), "orders": out})
}

func (s *Server) handleMarketDepth(c *gin.Context) {
	marketID := strings.ToUpper(strings.TrimSpace(c.DefaultQuery("market_id", ledger.MarketETHUSDC)))
	snap, err := s.engine.Depth(marketID, 25)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_MARKET", err.Error())
		return
	}
	bids := make([][2]string, len(snap.Bids))
	for i, b := range snap.Bids {
		bids[i] = [2]string{ledger.CLOBInt64ToHuman(b.Price1e8).StringFixed(8), ledger.CLOBInt64ToHuman(b.Qty1e8).StringFixed(8)}
	}
	asks := make([][2]string, len(snap.Asks))
	for i, a := range snap.Asks {
		asks[i] = [2]string{ledger.CLOBInt64ToHuman(a.Price1e8).StringFixed(8), ledger.CLOBInt64ToHuman(a.Qty1e8).StringFixed(8)}
	}
	c.JSON(http.StatusOK, gin.H{
		"market_id":               snap.MarketID,
		"wal_seq":                 snap.WalSeq,
		"mark_price":              ledger.CLOBInt64ToHuman(snap.MarkPrice1e8).StringFixed(8),
		"circuit_breaker_tripped": snap.CircuitBreakerTripped,
		"bids":                    bids,
		"asks":                    asks,
	})
}

func (s *Server) handleMarketTrades(c *gin.Context) {
	marketID := strings.ToUpper(strings.TrimSpace(c.DefaultQuery("market_id", ledger.MarketETHUSDC)))
	trades, err := s.ledger.ListRecentTrades(c.Request.Context(), marketID, 50)
	if err != nil {
		writeAPIError(c, http.StatusInternalServerError, "ERR_LIST_TRADES", err.Error())
		return
	}
	out := make([]gin.H, 0, len(trades))
	for _, t := range trades {
		out = append(out, gin.H{
			"trade_id":       strconv.FormatInt(t.TradeID, 10),
			"wal_seq":        t.WalSeq,
			"market_id":      t.MarketID,
			"maker_order_id": strconv.FormatInt(t.MakerOrderID, 10),
			"taker_order_id": strconv.FormatInt(t.TakerOrderID, 10),
			"price":          t.Price.StringFixed(8),
			"quantity":       t.Quantity.StringFixed(8),
			"quote_notional": t.QuoteNotional.StringFixed(8),
			"executed_at":    t.ExecutedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"market_id": marketID, "trades": out})
}

func (s *Server) handleGetBalances(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	bals, err := s.ledger.ListUserBalances(c.Request.Context(), userID)
	if err != nil {
		writeAPIError(c, http.StatusInternalServerError, "ERR_LIST_BALANCES", err.Error())
		return
	}
	resp := make([]gin.H, 0, len(bals))
	for _, b := range bals {
		resp = append(resp, gin.H{
			"asset":     b.Asset,
			"available": b.Available.StringFixed(18),
			"locked":    b.Locked.StringFixed(18),
		})
	}
	c.JSON(http.StatusOK, gin.H{"user_id": userID.String(), "balances": resp})
}

type withdrawReq struct {
	Asset            string `json:"asset" binding:"required"`
	Amount           string `json:"amount" binding:"required"`
	RecipientAddress string `json:"recipient_address" binding:"required"`
}

var globalVoucherNonce atomic.Int64

func init() {
	globalVoucherNonce.Store(10000)
}

func (s *Server) handleRequestWithdrawal(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}
	idemKey, ok := parseIdempotencyKey(c)
	if !ok {
		return
	}

	var req withdrawReq
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_PAYLOAD", err.Error())
		return
	}
	asset := strings.ToUpper(strings.TrimSpace(req.Asset))
	humanAmt, err := ledger.ParseDecimalString(req.Amount)
	if err != nil || !humanAmt.IsPositive() {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_DECIMAL_PRECISION", "invalid withdrawal amount")
		return
	}
	rawWei, err := ledger.HumanToWei(asset, humanAmt)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "ERR_INVALID_DECIMAL_PRECISION", err.Error())
		return
	}

	tier := evm.SigningTierHot
	if s.relayer != nil {
		tier = s.relayer.EvaluateTier(asset, rawWei)
	} else {
		// Default hot per-tx threshold: 10,000 USDC/USDT, 5 ETH, 0.25 WBTC.
		if hotThresholdExceeds(asset, rawWei) {
			tier = evm.SigningTierColdCosigned
		}
	}

	vNonce := globalVoucherNonce.Add(1)
	wRec, _, err := s.ledger.QueueWithdrawal(c.Request.Context(), ledger.WithdrawalRequest{
		WithdrawalID:     uuid.New(),
		IdempotencyKey:   idemKey,
		UserID:           userID,
		RecipientAddress: req.RecipientAddress,
		Asset:            asset,
		HumanAmount:      humanAmt,
		RawAmountWei:     rawWei,
		SigningTier:      tier,
		VoucherNonce:     vNonce,
	})
	if err != nil {
		if errors.Is(err, ledger.ErrInsufficientAvailable) {
			writeAPIError(c, http.StatusUnprocessableEntity, "ERR_INSUFFICIENT_AVAILABLE_BALANCE", err.Error())
			return
		}
		writeAPIError(c, http.StatusInternalServerError, "ERR_WITHDRAW_QUEUE", err.Error())
		return
	}

	if s.outbox != nil {
		if mem, ok := s.outbox.(*evm.MemoryOutboxStore); ok {
			mem.Enqueue(*wRec)
		}
	}

	c.JSON(http.StatusAccepted, gin.H{
		"withdrawal_id":  wRec.WithdrawalID.String(),
		"asset":          wRec.Asset,
		"amount":         wRec.HumanAmount.String(),
		"raw_amount_wei": wRec.RawAmountWei.String(),
		"signing_tier":   wRec.SigningTier,
		"voucher_nonce":  wRec.VoucherNonce,
		"status":         wRec.Status,
	})
}

func hotThresholdExceeds(asset string, rawWei *big.Int) bool {
	var limit *big.Int
	switch asset {
	case ledger.AssetUSDC, ledger.AssetUSDT:
		limit = big.NewInt(10_000_000_000) // 10,000 * 1e6
	case ledger.AssetETH:
		limit, _ = new(big.Int).SetString("5000000000000000000", 10) // 5 ETH
	case ledger.AssetWBTC:
		limit = big.NewInt(25_000_000) // 0.25 WBTC
	default:
		return false
	}
	return rawWei.Cmp(limit) > 0
}

func (s *Server) handleSolvencyProof(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	snap, err := s.snapStore.GetLatestSnapshot(c.Request.Context())
	if err != nil {
		writeAPIError(c, http.StatusInternalServerError, "ERR_SOLVENCY_SNAPSHOT", err.Error())
		return
	}
	var vault reconciler.VaultReader
	if s.rpcPool != nil {
		vault = s.rpcPool
	}
	if snap == nil || snap.Tree == nil {
		checker := reconciler.NewInvariantChecker(s.ledger, vault, s.metricsReg)
		snap, err = reconciler.GenerateAndSaveSnapshot(c.Request.Context(), s.ledger, checker, s.snapStore, nil)
		if err != nil {
			writeAPIError(c, http.StatusInternalServerError, "ERR_SOLVENCY_BUILD", err.Error())
			return
		}
	}

	leaf, path, err := snap.Tree.ProofForUser(userID)
	if err != nil {
		// Ensure user accounts exist and rebuild snapshot on demand.
		_ = s.ledger.EnsureUserAccounts(c.Request.Context(), userID)
		checker := reconciler.NewInvariantChecker(s.ledger, vault, s.metricsReg)
		snap, err = reconciler.GenerateAndSaveSnapshot(c.Request.Context(), s.ledger, checker, s.snapStore, nil)
		if err != nil {
			writeAPIError(c, http.StatusInternalServerError, "ERR_SOLVENCY_REBUILD", err.Error())
			return
		}
		leaf, path, err = snap.Tree.ProofForUser(userID)
		if err != nil {
			writeAPIError(c, http.StatusNotFound, "ERR_USER_NOT_IN_SNAPSHOT", fmt.Sprintf("user %s missing from snapshot", userID))
			return
		}
	}

	c.JSON(http.StatusOK, reconciler.SolvencyProofResponse{
		SnapshotID:     snap.SnapshotID,
		MerkleRoot:     snap.MerkleRoot,
		EVMBlockNumber: snap.EVMBlockNumber,
		PublishTxHash:  snap.PublishTxHash,
		InvariantOK:    snap.InvariantOK,
		UserLeaf:       leaf,
		MerklePath:     path,
	})
}
