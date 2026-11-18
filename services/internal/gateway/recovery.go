package gateway

import (
	"context"

	"github.com/settlecore/settlecore/services/internal/engine"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/shopspring/decimal"
)

// OrphanedOrderRecovery records a single pre-WAL RESERVED order rolled back during recovery.
type OrphanedOrderRecovery struct {
	OrderID        int64  `json:"order_id"`
	UserID         string `json:"user_id"`
	MarketID       string `json:"market_id"`
	ReleasedAmount string `json:"released_amount"`
	Status         string `json:"status"`
}

// OrphanRecoveryReport summarizes the WAL-vs-DB startup reconciliation pass.
type OrphanRecoveryReport struct {
	OrphansDetected     int                     `json:"orphans_detected"`
	OrphansRolledBack   int                     `json:"orphans_rolled_back"`
	RolledBackOrders    []OrphanedOrderRecovery `json:"rolled_back_orders"`
	ReleasedTotal       string                  `json:"released_total"`
	ReservesInvariantOK bool                    `json:"reserves_invariant_ok"`
}

// ReconcileOrphanedReservations scans DB orders stuck in RESERVED and atomically rolls back
// any unsubmitted order absent from the engine WAL (locked -> available, status = ABORTED_UNSUBMITTED).
func ReconcileOrphanedReservations(ctx context.Context, store ledger.Store, walPath string) (OrphanRecoveryReport, error) {
	report := OrphanRecoveryReport{
		RolledBackOrders:    make([]OrphanedOrderRecovery, 0),
		ReleasedTotal:       "0.00000000",
		ReservesInvariantOK: true,
	}

	reserved, err := store.ListReservedOrders(ctx)
	if err != nil {
		return report, err
	}

	walOrderSeqs := make(map[uint64]uint64)
	if walPath != "" {
		if recs, _, walErr := engine.ReadWAL(walPath); walErr == nil {
			for _, r := range recs {
				if r.Opcode == engine.OpSubmitOrder && r.OrderID > 0 {
					walOrderSeqs[r.OrderID] = r.WalSeq
				}
			}
		}
	}

	releasedSum := decimal.Zero
	for _, ord := range reserved {
		if ord.WalSeq > 0 {
			continue
		}
		if _, inWAL := walOrderSeqs[uint64(ord.OrderID)]; inWAL {
			continue
		}
		report.OrphansDetected++
		rel, relErr := store.ReleaseOrderReservation(ctx, ord.OrderID, 0, ledger.OrderStatusAbortedUnsubmitted)
		if relErr != nil {
			report.ReservesInvariantOK = false
			return report, relErr
		}
		releasedSum = releasedSum.Add(rel)
		report.OrphansRolledBack++
		report.RolledBackOrders = append(report.RolledBackOrders, OrphanedOrderRecovery{
			OrderID:        ord.OrderID,
			UserID:         ord.UserID.String(),
			MarketID:       ord.MarketID,
			ReleasedAmount: rel.StringFixed(8),
			Status:         ledger.OrderStatusAbortedUnsubmitted,
		})
	}
	report.ReleasedTotal = releasedSum.StringFixed(8)
	return report, nil
}
