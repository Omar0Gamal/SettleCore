package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/settlecore/settlecore/services/internal/config"
	"github.com/settlecore/settlecore/services/internal/engine"
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

	store := ledger.NewPostgresStore(pool)

	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Default.Handler())
	metricsSrv := &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("settlement metrics server error", zap.Error(err))
		}
	}()

	walPath := filepath.Join(cfg.EngineWALDir, "engine.wal")
	logger.Info("settlement worker started", zap.String("wal_path", walPath))

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = metricsSrv.Shutdown(shutdownCtx)
			return
		case <-ticker.C:
			_ = settleOnce(ctx, walPath, cfg, store)
		}
	}
}

func settleOnce(ctx context.Context, walPath string, cfg config.Config, store ledger.Store) error {
	evlogPath := walPath + ".evlog"
	if _, statErr := os.Stat(evlogPath); statErr == nil {
		return settleFromEventLog(ctx, evlogPath, cfg, store)
	}
	return settleFromCommandWALFallback(ctx, walPath, cfg, store)
}

// settleFromEventLog consumes committed 64-byte "SCEV" execution events directly from
// engine.wal.evlog without re-running a matching engine (Upgrade A / ADR-006).
func settleFromEventLog(ctx context.Context, evlogPath string, cfg config.Config, store ledger.Store) error {
	events, _, err := engine.ReadEventLog(evlogPath)
	if err != nil || len(events) == 0 {
		return err
	}

	for _, marketID := range []string{ledger.MarketETHUSDC, ledger.MarketBTCUSDC} {
		lastSeq, err := store.GetLastSettledWalSeq(ctx, marketID)
		if err != nil {
			continue
		}
		code, _ := engine.MarketIDToCode(marketID)
		batch := ledger.SettlementBatch{MarketID: marketID}
		eventCount := 0

		for _, ev := range events {
			if ev.MarketID != code || int64(ev.WalSeq) <= lastSeq {
				continue
			}
			if int64(ev.WalSeq) > batch.MaxWalSeq {
				batch.MaxWalSeq = int64(ev.WalSeq)
			}

			switch ev.EventType {
			case engine.EventTypeTradeFill:
				makerOrd, mErr := store.GetOrder(ctx, int64(ev.MakerOrderID))
				takerOrd, tErr := store.GetOrder(ctx, int64(ev.OrderID))
				if mErr != nil || tErr != nil {
					continue
				}
				takerSide := ledger.SideBuy
				if (ev.Flags & engine.EventFlagSideSell) != 0 {
					takerSide = ledger.SideSell
				}
				notional1e8, nErr := ledger.ComputeQuoteNotional1e8(ev.ExecPrice1e8, ev.FillQty1e8)
				if nErr != nil {
					continue
				}
				makerFee1e8, _ := ledger.ComputeFeeCeil1e8(notional1e8, cfg.MakerFeeBps)
				takerFee1e8, _ := ledger.ComputeFeeCeil1e8(notional1e8, cfg.TakerFeeBps)
				batch.Fills = append(batch.Fills, ledger.SettlementFill{
					TradeID:       int64(ev.TradeID),
					WalSeq:        int64(ev.WalSeq),
					MarketID:      marketID,
					MakerOrderID:  int64(ev.MakerOrderID),
					TakerOrderID:  int64(ev.OrderID),
					MakerUserID:   makerOrd.UserID,
					TakerUserID:   takerOrd.UserID,
					TakerSide:     takerSide,
					ExecPrice:     ledger.CLOBInt64ToHuman(ev.ExecPrice1e8),
					FillQty:       ledger.CLOBInt64ToHuman(ev.FillQty1e8),
					QuoteNotional: ledger.CLOBInt64ToHuman(notional1e8),
					MakerFee:      ledger.CLOBInt64ToHuman(makerFee1e8),
					TakerFee:      ledger.CLOBInt64ToHuman(takerFee1e8),
					MakerTerminal: (ev.Flags & engine.EventFlagMakerTerminal) != 0,
					TakerTerminal: (ev.Flags & engine.EventFlagTakerTerminal) != 0,
				})
				eventCount++

			case engine.EventTypeOrderCanceled, engine.EventTypeOrderRejected:
				ord, oErr := store.GetOrder(ctx, int64(ev.OrderID))
				if oErr != nil {
					continue
				}
				newStatus := ledger.OrderStatusCanceled
				if ev.EventType == engine.EventTypeOrderRejected {
					newStatus = ledger.OrderStatusRejectedReleased
				}
				batch.Cancels = append(batch.Cancels, ledger.SettlementCancel{
					OrderID:   int64(ev.OrderID),
					WalSeq:    int64(ev.WalSeq),
					MarketID:  marketID,
					UserID:    ord.UserID,
					NewStatus: newStatus,
				})
				eventCount++
			}

			if eventCount >= 256 {
				break
			}
		}

		if batch.MaxWalSeq > lastSeq {
			if _, err := store.ApplySettlementBatch(ctx, batch); err != nil {
				return err
			}
		}
	}
	return nil
}

// settleFromCommandWALFallback replays engine.wal when engine.wal.evlog is absent.
func settleFromCommandWALFallback(ctx context.Context, walPath string, cfg config.Config, store ledger.Store) error {
	records, _, err := engine.ReadWAL(walPath)
	if err != nil || len(records) == 0 {
		return err
	}

	eng, err := engine.NewBridge(engine.EngineConfig{
		DisableWAL:        true,
		MaxRestingOrders:  cfg.EngineMaxRestingOrders,
		CircuitBreakerBps: cfg.EngineCircuitBreakerBps,
	}, metrics.Default)
	if err != nil {
		return err
	}
	defer func() { _ = eng.Close() }()

	for _, marketID := range []string{ledger.MarketETHUSDC, ledger.MarketBTCUSDC} {
		lastSeq, err := store.GetLastSettledWalSeq(ctx, marketID)
		if err != nil {
			continue
		}
		code, _ := engine.MarketIDToCode(marketID)
		var batch ledger.SettlementBatch
		batch.MarketID = marketID

		for _, rec := range records {
			if rec.MarketID != code {
				continue
			}
			side, ot, tif, stp := engine.UnpackFlags(rec.Flags)
			cmd := engine.OrderCmd{
				WalSeq:        rec.WalSeq,
				OrderID:       rec.OrderID,
				AccountIDLow:  rec.AccountIDLow,
				AccountIDHigh: rec.AccountIDHigh,
				Price1e8:      rec.Price1e8,
				Qty1e8:        rec.Qty1e8,
				MarketID:      rec.MarketID,
				Opcode:        rec.Opcode,
				Side:          side,
				OrderType:     ot,
				TimeInForce:   tif,
				STPMode:       stp,
			}
			rep, _ := eng.Submit(cmd)
			if int64(rec.WalSeq) <= lastSeq {
				continue
			}
			if int64(rec.WalSeq) > batch.MaxWalSeq {
				batch.MaxWalSeq = int64(rec.WalSeq)
			}
			takerSide := ledger.SideBuy
			if side == engine.SideCodeSell {
				takerSide = ledger.SideSell
			}
			for _, f := range rep.Fills {
				makerFee1e8, _ := ledger.ComputeFeeCeil1e8(f.QuoteNotional1e8, cfg.MakerFeeBps)
				takerFee1e8, _ := ledger.ComputeFeeCeil1e8(f.QuoteNotional1e8, cfg.TakerFeeBps)
				batch.Fills = append(batch.Fills, ledger.SettlementFill{
					TradeID:       int64(f.TradeID),
					WalSeq:        int64(rec.WalSeq),
					MarketID:      marketID,
					MakerOrderID:  int64(f.MakerOrderID),
					TakerOrderID:  int64(f.TakerOrderID),
					MakerUserID:   engine.Uint64PairToUUID(f.MakerAccountLow, f.MakerAccountHigh),
					TakerUserID:   engine.Uint64PairToUUID(rec.AccountIDLow, rec.AccountIDHigh),
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
			if rec.Opcode == engine.OpCancelOrder {
				batch.Cancels = append(batch.Cancels, ledger.SettlementCancel{
					OrderID:   int64(rec.OrderID),
					WalSeq:    int64(rec.WalSeq),
					MarketID:  marketID,
					UserID:    engine.Uint64PairToUUID(rec.AccountIDLow, rec.AccountIDHigh),
					NewStatus: ledger.OrderStatusCanceled,
				})
			}
			if len(batch.Fills)+len(batch.Cancels) >= 256 {
				break
			}
		}

		if batch.MaxWalSeq > lastSeq {
			_, _ = store.ApplySettlementBatch(ctx, batch)
		}
	}
	return nil
}
