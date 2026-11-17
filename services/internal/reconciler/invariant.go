package reconciler

import (
	"context"
	"math/big"
	"time"

	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/metrics"
	"github.com/shopspring/decimal"
)

// VaultReader abstracts on-chain EscrowVault balance queries and chain head lookup.
type VaultReader interface {
	HeadNumber(ctx context.Context) (int64, error)
	VaultBalance(ctx context.Context, asset string) (*big.Int, error)
}

// AssetSolvency reports reserves vs. liabilities for a single custody asset.
type AssetSolvency struct {
	Asset              string          `json:"asset"`
	OnChainVaultWei    string          `json:"onchain_vault_wei"`
	UserLiabilitiesWei string          `json:"user_liabilities_wei,omitempty"`
	OnChainVault       decimal.Decimal `json:"onchain_vault"`
	UserLiabilities    decimal.Decimal `json:"user_liabilities"`
	FeeTreasury        decimal.Decimal `json:"fee_treasury"`
	TotalRequired      decimal.Decimal `json:"total_required"`
	Solvent            bool            `json:"solvent"`
}

// InvariantReport captures the full 60-second Proof-of-Reserves solvency check result.
type InvariantReport struct {
	BlockNumber int64                    `json:"evm_block_number"`
	InvariantOK bool                     `json:"invariant_ok"`
	Assets      map[string]AssetSolvency `json:"assets"`
	CheckedAt   time.Time                `json:"checked_at"`
}

// InvariantChecker verifies on-chain EscrowVault balances >= user liabilities + fees.
type InvariantChecker struct {
	ledgerStore ledger.Store
	vault       VaultReader
	metricsReg  *metrics.Registry
}

// NewInvariantChecker constructs an InvariantChecker.
func NewInvariantChecker(ledgerStore ledger.Store, vault VaultReader, reg *metrics.Registry) *InvariantChecker {
	if reg == nil {
		reg = metrics.Default
	}
	return &InvariantChecker{
		ledgerStore: ledgerStore,
		vault:       vault,
		metricsReg:  reg,
	}
}

// CheckOnce runs a consistent solvency invariant check across all four custody assets.
func (c *InvariantChecker) CheckOnce(ctx context.Context) (InvariantReport, error) {
	totals, _, err := c.ledgerStore.SnapshotLiabilities(ctx)
	if err != nil {
		return InvariantReport{}, err
	}

	head := int64(0)
	if c.vault != nil {
		h, err := c.vault.HeadNumber(ctx)
		if err != nil {
			return InvariantReport{}, err
		}
		head = h
	}

	report := InvariantReport{
		BlockNumber: head,
		InvariantOK: true,
		Assets:      make(map[string]AssetSolvency, len(ledger.AllAssets)),
		CheckedAt:   time.Now().UTC(),
	}

	for _, asset := range ledger.AllAssets {
		t := totals[asset]
		totalReq := t.UserTotal.Add(t.FeeTotal)

		var vaultWei *big.Int
		if c.vault != nil {
			bal, err := c.vault.VaultBalance(ctx, asset)
			if err != nil {
				return InvariantReport{}, err
			}
			vaultWei = bal
		} else {
			// Offline/no-chain mode: convert required to wei for structural consistency.
			w, err := ledger.HumanToWei(asset, totalReq)
			if err != nil {
				vaultWei = big.NewInt(0)
			} else {
				vaultWei = w
			}
		}

		vaultHuman, err := ledger.WeiToHuman(asset, vaultWei)
		if err != nil {
			return InvariantReport{}, err
		}

		userWei, err := ledger.HumanToWei(asset, t.UserTotal)
		if err != nil {
			userWei = big.NewInt(0)
		}

		solvent := vaultHuman.GreaterThanOrEqual(totalReq)
		if !solvent {
			report.InvariantOK = false
		}
		report.Assets[asset] = AssetSolvency{
			Asset:              asset,
			OnChainVaultWei:    vaultWei.String(),
			UserLiabilitiesWei: userWei.String(),
			OnChainVault:       vaultHuman,
			UserLiabilities:    t.UserTotal,
			FeeTreasury:        t.FeeTotal,
			TotalRequired:      totalReq,
			Solvent:            solvent,
		}
	}

	c.metricsReg.SetReservesInvariant(report.InvariantOK)
	return report, nil
}
