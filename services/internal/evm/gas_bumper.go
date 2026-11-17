package evm

import (
	"context"
	"math/big"
	"time"
)

var (
	big115 = big.NewInt(115)
	big100 = big.NewInt(100)
	big99  = big.NewInt(99)
	big2   = big.NewInt(2)
)

// ComputeBumpedEIP1559Fees computes +15% ceiling-bumped EIP-1559 fee caps per ADR-004 §3.3:
//
//	newPriority = ceil(oldPriority * 1.15)
//	newMaxFee   = max(ceil(oldMaxFee * 1.15), 2 * currentBaseFee + newPriority)
func ComputeBumpedEIP1559Fees(oldMaxFee, oldPriority, currentBaseFee *big.Int) (newMaxFee, newPriority *big.Int) {
	if oldPriority == nil || oldPriority.Sign() <= 0 {
		oldPriority = big.NewInt(1_500_000_000)
	}
	if oldMaxFee == nil || oldMaxFee.Sign() <= 0 {
		oldMaxFee = big.NewInt(3_000_000_000)
	}
	if currentBaseFee == nil {
		currentBaseFee = big.NewInt(1_000_000_000)
	}

	newPriority = ceilMul115Div100(oldPriority)
	bumpedOldMax := ceilMul115Div100(oldMaxFee)
	headroom := new(big.Int).Add(new(big.Int).Mul(big2, currentBaseFee), newPriority)

	newMaxFee = bumpedOldMax
	if headroom.Cmp(newMaxFee) > 0 {
		newMaxFee = headroom
	}
	return newMaxFee, newPriority
}

func ceilMul115Div100(v *big.Int) *big.Int {
	num := new(big.Int).Mul(v, big115)
	num.Add(num, big99)
	return num.Div(num, big100)
}

// BumpStuckOnce scans SUBMITTED withdrawals older than StuckThreshold and replaces them
// at the exact same relayer EOA nonce with >= +15% bumped EIP-1559 fee caps.
func (r *Relayer) BumpStuckOnce(ctx context.Context) (int, error) {
	cutoff := time.Now().UTC().Add(-r.cfg.StuckThreshold)
	stuck, err := r.outbox.ListStuckSubmitted(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	if len(stuck) == 0 {
		return 0, nil
	}

	baseFee, _, err := r.pool.SuggestGasFees(ctx)
	if err != nil {
		return 0, err
	}

	bumped := 0
	for _, rec := range stuck {
		if rec.RelayerEOANonce == nil {
			continue
		}
		// Skip if tx already mined since list query.
		if rec.LastTxHash != "" {
			if rcpt, err := r.pool.TxReceipt(ctx, rec.LastTxHash); err == nil && rcpt.Confirmed {
				continue
			}
		}

		newMaxFee, newTip := ComputeBumpedEIP1559Fees(rec.MaxFeePerGasWei, rec.MaxPriorityFeeWei, baseFee)
		sub, err := r.buildWithdrawTx(rec, uint64(*rec.RelayerEOANonce), newMaxFee, newTip)
		if err != nil {
			return bumped, err
		}
		txHash, err := r.pool.BroadcastTx(ctx, sub)
		if err != nil {
			return bumped, err
		}
		if err := r.outbox.RecordGasBump(ctx, rec.WithdrawalID, txHash, newMaxFee, newTip); err != nil {
			return bumped, err
		}
		r.metricsReg.IncStuckTxGasBumps()
		bumped++
	}
	return bumped, nil
}
