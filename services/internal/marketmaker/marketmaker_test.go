package marketmaker

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/shopspring/decimal"
)

func TestDeterministicFallbackFeedAndLadderQuoter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	ticks := make(chan PriceTick, 16)
	feed := NewFeed("", 20*time.Millisecond)
	go func() {
		_ = feed.Run(ctx, ticks)
	}()

	var gotTick PriceTick
	select {
	case gotTick = <-ticks:
	case <-time.After(1 * time.Second):
		t.Fatalf("expected deterministic fallback tick")
	}
	if !gotTick.Mid.IsPositive() || gotTick.Source != "deterministic-fallback" {
		t.Fatalf("unexpected fallback tick: %+v", gotTick)
	}

	quoter := NewQuoter("http://localhost:8080", "ws://localhost:8080/v1/ws", uuid.New(), 8, 5)
	bids, asks := quoter.BuildLadder(ledger.MarketETHUSDC, decimal.NewFromInt(3100))
	if len(bids) != 5 || len(asks) != 5 {
		t.Fatalf("expected 5 bid and 5 ask levels, got %d/%d", len(bids), len(asks))
	}
	for i := 0; i < 5; i++ {
		if !bids[i].LessThan(asks[i]) {
			t.Fatalf("expected bid < ask at level %d: %s vs %s", i, bids[i], asks[i])
		}
		if i > 0 && (!bids[i].LessThan(bids[i-1]) || !asks[i].GreaterThan(asks[i-1])) {
			t.Fatalf("ladder levels not monotonically ordered at %d", i)
		}
	}
}
