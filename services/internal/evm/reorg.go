package evm

import (
	"context"
	"fmt"
)

// handleReorg walks backward from startHeight along indexed canonical blocks until finding
// the common ancestor whose hash matches the current RPC chain, marks orphaned blocks &
// deposits, posts compensating REORG_REVERSAL double-entry debits for any CONFIRMED
// orphaned deposits, and increments reorg_rollbacks_total.
func (w *Watcher) handleReorg(ctx context.Context, startHeight int64) (int64, error) {
	minHeight := max(0, startHeight-w.reorgWindow)
	ancestor := int64(-1)

	for h := startHeight; h >= minHeight; h-- {
		dbBlk, err := w.indexStore.GetCanonicalBlock(ctx, h)
		if err != nil {
			return 0, err
		}
		if dbBlk == nil {
			continue
		}
		rpcBlk, err := w.pool.BlockByNumber(ctx, h)
		if err != nil {
			return 0, err
		}
		if rpcBlk.Hash == dbBlk.BlockHash {
			ancestor = h
			break
		}
	}

	if ancestor < 0 {
		ancestor = minHeight - 1
	}

	_, orphanedDeps, err := w.indexStore.MarkOrphanedAbove(ctx, ancestor)
	if err != nil {
		return 0, err
	}

	for _, dep := range orphanedDeps {
		if dep.Status == DepositConfirmed {
			ref := fmt.Sprintf("reorg_reversal:%s:%d", dep.TxHash, dep.LogIndex)
			if _, err := w.ledgerStore.ReverseDeposit(ctx, dep.UserID, dep.Asset, dep.CreditedAmount, ref); err != nil {
				// Per ADR-004 §5: trip reserves_invariant_ok = 0 if reversal would go negative.
				w.metricsReg.SetReservesInvariant(false)
				return ancestor, err
			}
		}
	}

	w.metricsReg.IncReorgRollbacks()
	return ancestor, nil
}
