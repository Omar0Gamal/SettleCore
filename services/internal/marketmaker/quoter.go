package marketmaker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/shopspring/decimal"
)

// Quoter maintains two-sided bid/ask ladders against the Gateway REST API while holding
// a Cancel-on-Disconnect WebSocket session.
type Quoter struct {
	httpURL   string
	wsURL     string
	userID    uuid.UUID
	spreadBps int64
	levels    int
	client    *http.Client

	mu           sync.Mutex
	activeOrders map[string][]string // marketID -> orderIDs
}

// NewQuoter creates a market-maker ladder quoter.
func NewQuoter(httpURL, wsURL string, userID uuid.UUID, spreadBps int64, levels int) *Quoter {
	if spreadBps <= 0 {
		spreadBps = 8
	}
	if levels <= 0 {
		levels = 5
	}
	return &Quoter{
		httpURL:      httpURL,
		wsURL:        wsURL,
		userID:       userID,
		spreadBps:    spreadBps,
		levels:       levels,
		client:       &http.Client{Timeout: 3 * time.Second},
		activeOrders: make(map[string][]string),
	}
}

// BuildLadder computes deterministic bid and ask price levels around mid using exact decimal math.
func (q *Quoter) BuildLadder(marketID string, mid decimal.Decimal) (bids []decimal.Decimal, asks []decimal.Decimal) {
	halfSpread := decimal.NewFromInt(q.spreadBps).Div(decimal.NewFromInt(20_000))
	stepBps := decimal.NewFromInt(5).Div(decimal.NewFromInt(10_000)) // 5 bps per ladder rung

	qtyRound := int32(2)
	_ = qtyRound

	for i := 0; i < q.levels; i++ {
		offset := halfSpread.Add(stepBps.Mul(decimal.NewFromInt(int64(i))))
		bidPx := mid.Mul(decimal.NewFromInt(1).Sub(offset)).Round(2)
		askPx := mid.Mul(decimal.NewFromInt(1).Add(offset)).Round(2)
		if bidPx.IsPositive() {
			bids = append(bids, bidPx)
		}
		if askPx.IsPositive() {
			asks = append(asks, askPx)
		}
	}
	return bids, asks
}

// Run maintains the COD WebSocket session and refreshes quotes on incoming PriceTicks.
func (q *Quoter) Run(ctx context.Context, ticks <-chan PriceTick) error {
	go q.maintainWS(ctx)

	lastQuote := make(map[string]time.Time)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case tick := <-ticks:
			if time.Since(lastQuote[tick.MarketID]) < 1500*time.Millisecond {
				continue
			}
			lastQuote[tick.MarketID] = time.Now()
			_ = q.RefreshQuotes(ctx, tick)
		}
	}
}

func (q *Quoter) maintainWS(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		conn, _, err := websocket.Dial(dialCtx, q.wsURL, nil)
		cancel()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
				continue
			}
		}

		subPayload, _ := json.Marshal(map[string]any{
			"op":                   "subscribe",
			"cancel_on_disconnect": true,
			"user_id":              q.userID.String(),
			"channels": []string{
				"l2:" + ledger.MarketETHUSDC,
				"l2:" + ledger.MarketBTCUSDC,
				"user:" + q.userID.String(),
			},
		})
		_ = conn.Write(ctx, websocket.MessageText, subPayload)

		for {
			_, _, err := conn.Read(ctx)
			if err != nil {
				break
			}
		}
		_ = conn.Close(websocket.StatusNormalClosure, "reconnect")
	}
}

// RefreshQuotes cancels previous ladder orders for the market and submits fresh two-sided levels.
func (q *Quoter) RefreshQuotes(ctx context.Context, tick PriceTick) error {
	q.mu.Lock()
	prev := q.activeOrders[tick.MarketID]
	q.activeOrders[tick.MarketID] = nil
	q.mu.Unlock()

	for _, oid := range prev {
		_ = q.cancelOrder(ctx, tick.MarketID, oid)
	}

	bids, asks := q.BuildLadder(tick.MarketID, tick.Mid)
	qty := "0.50000000"
	if tick.MarketID == ledger.MarketBTCUSDC {
		qty = "0.05000000"
	}

	var newIDs []string
	for _, px := range bids {
		if oid, err := q.submitOrder(ctx, tick.MarketID, ledger.SideBuy, px.StringFixed(2), qty); err == nil && oid != "" {
			newIDs = append(newIDs, oid)
		}
	}
	for _, px := range asks {
		if oid, err := q.submitOrder(ctx, tick.MarketID, ledger.SideSell, px.StringFixed(2), qty); err == nil && oid != "" {
			newIDs = append(newIDs, oid)
		}
	}

	q.mu.Lock()
	q.activeOrders[tick.MarketID] = newIDs
	q.mu.Unlock()
	return nil
}

func (q *Quoter) submitOrder(ctx context.Context, marketID, side, price, qty string) (string, error) {
	body, err := json.Marshal(map[string]string{
		"market_id":     marketID,
		"side":          side,
		"order_type":    "LIMIT",
		"time_in_force": "GTC",
		"stp_mode":      "CANCEL_NEWEST",
		"price":         price,
		"quantity":      qty,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.httpURL+"/v1/orders", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SettleCore-User-Id", q.userID.String())
	req.Header.Set("Idempotency-Key", uuid.NewString())

	resp, err := q.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("submit status %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		OrderID string `json:"order_id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.OrderID, nil
}

func (q *Quoter) cancelOrder(ctx context.Context, marketID, orderID string) error {
	u := fmt.Sprintf("%s/v1/orders/%s?market_id=%s", q.httpURL, orderID, marketID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-SettleCore-User-Id", q.userID.String())
	resp, err := q.client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}
