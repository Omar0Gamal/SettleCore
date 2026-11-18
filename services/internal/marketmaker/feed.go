package marketmaker

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/shopspring/decimal"
)

// PriceTick represents a normalized top-of-book reference price tick (zero float64).
type PriceTick struct {
	MarketID  string
	Bid       decimal.Decimal
	Ask       decimal.Decimal
	Mid       decimal.Decimal
	Source    string
	Timestamp time.Time
}

// Feed streams live Binance bookTicker ticks with automatic deterministic random-walk fallback.
type Feed struct {
	wsURL        string
	tickInterval time.Duration
}

// NewFeed constructs a reference price feed.
func NewFeed(wsURL string, tickInterval time.Duration) *Feed {
	if tickInterval <= 0 {
		tickInterval = 1 * time.Second
	}
	return &Feed{
		wsURL:        wsURL,
		tickInterval: tickInterval,
	}
}

type binanceBookTicker struct {
	Symbol string `json:"s"`
	Bid    string `json:"b"`
	Ask    string `json:"a"`
}

// Run streams PriceTicks onto out until ctx is canceled, automatically falling back
// to deterministic random-walk ticks whenever Binance WS is unreachable.
func (f *Feed) Run(ctx context.Context, out chan<- PriceTick) error {
	// Deterministic initial prices in 1e2 cents (3100.00 USDC, 95000.00 USDC).
	ethCents := int64(310_000)
	btcCents := int64(9_500_000)
	var lcgState uint64 = 0x5E771EC04E123456

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if f.wsURL != "" {
			if err := f.streamBinance(ctx, out); err == nil {
				return nil
			}
		}

		// Deterministic random-walk fallback for offline/airgapped runs (10s window before retry).
		fallbackEnd := time.Now().Add(10 * time.Second)
		ticker := time.NewTicker(f.tickInterval)
		for time.Now().Before(fallbackEnd) {
			lcgState = lcgState*6364136223846793005 + 1442695040888963407
			deltaEth := int64((lcgState>>32)%41) - 20 // [-0.20, +0.20]
			lcgState = lcgState*6364136223846793005 + 1442695040888963407
			deltaBtc := int64((lcgState>>32)%401) - 200 // [-2.00, +2.00]

			ethCents = max(10_000, ethCents+deltaEth)
			btcCents = max(100_000, btcCents+deltaBtc)

			emitFallbackTick(ctx, out, ledger.MarketETHUSDC, ethCents, 25)
			emitFallbackTick(ctx, out, ledger.MarketBTCUSDC, btcCents, 500)

			select {
			case <-ctx.Done():
				ticker.Stop()
				return ctx.Err()
			case <-ticker.C:
			}
		}
		ticker.Stop()
	}
}

func emitFallbackTick(ctx context.Context, out chan<- PriceTick, market string, midCents, halfSpreadCents int64) {
	bid := decimal.New(midCents-halfSpreadCents, -2)
	ask := decimal.New(midCents+halfSpreadCents, -2)
	mid := decimal.New(midCents, -2)
	select {
	case out <- PriceTick{
		MarketID:  market,
		Bid:       bid,
		Ask:       ask,
		Mid:       mid,
		Source:    "deterministic-fallback",
		Timestamp: time.Now().UTC(),
	}:
	case <-ctx.Done():
	}
}

func (f *Feed) streamBinance(ctx context.Context, out chan<- PriceTick) error {
	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	streamURL := strings.TrimRight(f.wsURL, "/") + "/ethusdt@bookTicker/btcusdt@bookTicker"
	conn, _, err := websocket.Dial(dialCtx, streamURL, nil)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "done") }()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var msg binanceBookTicker
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		var market string
		switch strings.ToUpper(msg.Symbol) {
		case "ETHUSDT":
			market = ledger.MarketETHUSDC
		case "BTCUSDT":
			market = ledger.MarketBTCUSDC
		default:
			continue
		}
		bid, err1 := ledger.ParseDecimalString(msg.Bid)
		ask, err2 := ledger.ParseDecimalString(msg.Ask)
		if err1 != nil || err2 != nil || !bid.IsPositive() || !ask.IsPositive() {
			continue
		}
		mid := bid.Add(ask).Div(decimal.NewFromInt(2)).Round(2)
		select {
		case out <- PriceTick{
			MarketID:  market,
			Bid:       bid.Round(2),
			Ask:       ask.Round(2),
			Mid:       mid,
			Source:    "binance-ws",
			Timestamp: time.Now().UTC(),
		}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
