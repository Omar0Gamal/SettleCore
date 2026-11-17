package reconciler

import (
	"context"
	"math/big"
	"testing"

	"github.com/google/uuid"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/metrics"
	"github.com/shopspring/decimal"
)

type mockVault struct {
	head     int64
	balances map[string]*big.Int
}

func (m *mockVault) HeadNumber(_ context.Context) (int64, error) {
	return m.head, nil
}

func (m *mockVault) VaultBalance(_ context.Context, asset string) (*big.Int, error) {
	if b, ok := m.balances[asset]; ok {
		return b, nil
	}
	return big.NewInt(0), nil
}

func TestInvariantAndMerkleProofOfReserves(t *testing.T) {
	ctx := context.Background()
	reg := metrics.NewRegistry()
	store := ledger.NewMemoryStore()

	u1 := uuid.New()
	u2 := uuid.New()
	u3 := uuid.New()
	_, _ = store.CreditDeposit(ctx, u1, ledger.AssetUSDC, decimal.NewFromInt(1000), "d1")
	_, _ = store.CreditDeposit(ctx, u2, ledger.AssetETH, decimal.NewFromInt(2), "d2")
	_, _ = store.CreditDeposit(ctx, u3, ledger.AssetWBTC, decimal.RequireFromString("0.5"), "d3")

	vaultBalances := map[string]*big.Int{
		ledger.AssetETH:  big.NewInt(2_000_000_000_000_000_000),
		ledger.AssetUSDC: big.NewInt(1_000_000_000),
		ledger.AssetWBTC: big.NewInt(50_000_000),
		ledger.AssetUSDT: big.NewInt(0),
	}
	vault := &mockVault{
		head:     1048,
		balances: vaultBalances,
	}

	checker := NewInvariantChecker(store, vault, reg)
	snapStore := NewMemorySnapshotStore()
	rec, err := GenerateAndSaveSnapshot(ctx, store, checker, snapStore, nil)
	if err != nil {
		t.Fatalf("GenerateAndSaveSnapshot: %v", err)
	}
	if !rec.InvariantOK || !reg.Snapshot().ReservesInvariantOK {
		t.Fatalf("expected reserves_invariant_ok=true")
	}

	// Root sums must equal total user liabilities
	if rec.Tree.RootSums[ledger.AssetETH].Cmp(vaultBalances[ledger.AssetETH]) != 0 ||
		rec.Tree.RootSums[ledger.AssetUSDC].Cmp(vaultBalances[ledger.AssetUSDC]) != 0 ||
		rec.Tree.RootSums[ledger.AssetWBTC].Cmp(vaultBalances[ledger.AssetWBTC]) != 0 {
		t.Fatalf("unexpected root sums: %+v", rec.Tree.RootSums)
	}

	// Verify Merkle Sum Tree inclusion proof for all 3 users (odd count tests sentinel leaf padding!)
	for _, uid := range []uuid.UUID{u1, u2, u3} {
		leaf, path, err := rec.Tree.ProofForUser(uid)
		if err != nil {
			t.Fatalf("ProofForUser(%s): %v", uid, err)
		}
		if !VerifyProof(rec.MerkleRoot, *leaf, path) {
			t.Fatalf("Merkle VerifyProof failed for user %s", uid)
		}
		if !VerifyProofWithReserves(rec.MerkleRoot, *leaf, path, rec.Tree.RootSums, vaultBalances) {
			t.Fatalf("Merkle VerifyProofWithReserves failed for user %s", uid)
		}

		// Tamper test 1: mutating user leaf balance must fail verification
		tamperedLeaf := *leaf
		tamperedLeaf.Balances = map[string]string{
			ledger.AssetETH: "9999999999999999999",
		}
		if VerifyProof(rec.MerkleRoot, tamperedLeaf, path) {
			t.Fatalf("expected tampered leaf to fail Merkle verification")
		}

		// Tamper test 2: mutating any sibling's asset sum must fail verification
		for stepIdx := range path {
			tamperedPath := make([]MerklePathStep, len(path))
			copy(tamperedPath, path)
			badSibSums := make(map[string]string, len(path[stepIdx].SiblingSums))
			for k, v := range path[stepIdx].SiblingSums {
				badSibSums[k] = v
			}
			badSibSums[ledger.AssetUSDC] = "999999999"
			tamperedPath[stepIdx].SiblingSums = badSibSums

			if VerifyProof(rec.MerkleRoot, *leaf, tamperedPath) {
				t.Fatalf("expected tampered sibling sum at step %d to fail verification", stepIdx)
			}

			// Even if NodeSums is also adjusted to satisfy parent == left + right,
			// the cryptographic internal node hash commitment must catch the tampering!
			badNodeSums := make(map[string]string, len(path[stepIdx].NodeSums))
			for k, v := range path[stepIdx].NodeSums {
				badNodeSums[k] = v
			}
			origNode, _ := new(big.Int).SetString(path[stepIdx].NodeSums[ledger.AssetUSDC], 10)
			origSib, _ := new(big.Int).SetString(path[stepIdx].SiblingSums[ledger.AssetUSDC], 10)
			delta := new(big.Int).Sub(big.NewInt(999999999), origSib)
			badNodeSums[ledger.AssetUSDC] = new(big.Int).Add(origNode, delta).String()
			tamperedPath[stepIdx].NodeSums = badNodeSums

			if VerifyProof(rec.MerkleRoot, *leaf, tamperedPath) {
				t.Fatalf("expected coordinated sibling+node sum tampering at step %d to fail hash check", stepIdx)
			}
		}
	}

	// Shortfall test: underfunded vault must fail VerifyProofWithReserves and CheckOnce
	shortfallVault := map[string]*big.Int{
		ledger.AssetETH:  big.NewInt(2_000_000_000_000_000_000),
		ledger.AssetUSDC: big.NewInt(500_000_000),
		ledger.AssetWBTC: big.NewInt(50_000_000),
		ledger.AssetUSDT: big.NewInt(0),
	}
	leaf1, path1, _ := rec.Tree.ProofForUser(u1)
	if VerifyProofWithReserves(rec.MerkleRoot, *leaf1, path1, rec.Tree.RootSums, shortfallVault) {
		t.Fatalf("expected VerifyProofWithReserves to fail on underfunded vault")
	}

	vault.balances[ledger.AssetUSDC] = big.NewInt(500_000_000)
	rep, err := checker.CheckOnce(ctx)
	if err != nil {
		t.Fatalf("CheckOnce shortfall: %v", err)
	}
	if rep.InvariantOK || reg.Snapshot().ReservesInvariantOK {
		t.Fatalf("expected invariant_ok=false on underfunded vault")
	}
}

func TestMerkleSumTreeNegativeAndEdgeCases(t *testing.T) {
	ctx := context.Background()
	store := ledger.NewMemoryStore()

	// Single-user tree
	u1 := uuid.New()
	_, _ = store.CreditDeposit(ctx, u1, ledger.AssetETH, decimal.NewFromInt(3), "d-single")
	_, users, err := store.SnapshotLiabilities(ctx)
	if err != nil {
		t.Fatalf("SnapshotLiabilities: %v", err)
	}
	tree, err := BuildMerkleTree(users, 42)
	if err != nil {
		t.Fatalf("BuildMerkleTree: %v", err)
	}
	leaf, path, err := tree.ProofForUser(u1)
	if err != nil {
		t.Fatalf("ProofForUser: %v", err)
	}
	if !VerifyProof(tree.Root.Hex(), *leaf, path) {
		t.Fatalf("expected single-user sum tree proof to verify")
	}

	// Negative sibling sum in a multi-user tree must be rejected
	u2 := uuid.New()
	_, _ = store.CreditDeposit(ctx, u2, ledger.AssetUSDT, decimal.NewFromInt(500), "d-second")
	_, users2, _ := store.SnapshotLiabilities(ctx)
	tree2, err := BuildMerkleTree(users2, 43)
	if err != nil {
		t.Fatalf("BuildMerkleTree 2: %v", err)
	}
	leaf2, path2, err := tree2.ProofForUser(u1)
	if err != nil {
		t.Fatalf("ProofForUser 2: %v", err)
	}
	path2[0].SiblingSums[ledger.AssetUSDT] = "-10"
	if VerifyProof(tree2.Root.Hex(), *leaf2, path2) {
		t.Fatalf("expected negative sibling sum to fail verification")
	}
}
