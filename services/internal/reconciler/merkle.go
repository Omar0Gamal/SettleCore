package reconciler

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/settlecore/settlecore/services/internal/db"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/shopspring/decimal"
)

// Domain-separation prefixes per ADR-005 §3.4 & ADR-008.
const (
	LeafDomainPrefix byte = 0x00
	NodeDomainPrefix byte = 0x01
)

// MerklePathStep represents a sibling node along the inclusion path from leaf to root
// in a Merkle Sum Tree, carrying both sibling and cumulative parent asset sums.
type MerklePathStep struct {
	Position    string            `json:"position"`     // "LEFT" or "RIGHT"
	SiblingHash string            `json:"sibling_hash"` // 0x-prefixed hex
	SiblingSums map[string]string `json:"sibling_sums"` // native integer wei strings per asset
	NodeSums    map[string]string `json:"node_sums"`    // cumulative parent sums (wei strings) per asset
}

// MerklePathNode is an alias for MerklePathStep.
type MerklePathNode = MerklePathStep

// UserLeaf captures the canonical pre-image and Keccak-256 leaf hash for a user.
type UserLeaf struct {
	UserID   uuid.UUID         `json:"user_id"`
	Nonce    int64             `json:"nonce"`
	Balances map[string]string `json:"balances"` // native integer wei strings per asset
	LeafHash string            `json:"leaf_hash"`
}

// SolvencyProofResponse matches GET /v1/solvency/proof in docs/api-spec.md §2.5.
type SolvencyProofResponse struct {
	SnapshotID       uuid.UUID         `json:"snapshot_id"`
	MerkleRoot       string            `json:"merkle_root"`
	EVMBlockNumber   int64             `json:"evm_block_number"`
	PublishTxHash    string            `json:"publish_tx_hash,omitempty"`
	InvariantOK      bool              `json:"invariant_ok"`
	TotalLiabilities map[string]string `json:"total_liabilities,omitempty"`
	OnChainReserves  map[string]string `json:"onchain_reserves,omitempty"`
	UserLeaf         *UserLeaf         `json:"user_leaf,omitempty"`
	MerklePath       []MerklePathStep  `json:"merkle_path"`
}

type merkleNode struct {
	Hash common.Hash
	Sums map[string]*big.Int
}

// MerkleTree stores all levels of a domain-separated Keccak-256 Merkle Sum Tree.
type MerkleTree struct {
	Root        common.Hash
	RootSums    map[string]*big.Int
	BlockNumber int64
	Leaves      []UserLeaf
	leafIndex   map[uuid.UUID]int
	levels      [][]merkleNode // levels[0] = leaves (even-padded), levels[len-1][0] = root
}

// TotalLiabilitiesStrings returns the root's cumulative per-asset liability sums as wei strings.
func (t *MerkleTree) TotalLiabilitiesStrings() map[string]string {
	if t == nil {
		return nil
	}
	return sumsToStrings(t.RootSums)
}

func zeroSums() map[string]*big.Int {
	m := make(map[string]*big.Int, len(ledger.AllAssets))
	for _, asset := range ledger.AllAssets {
		m[asset] = big.NewInt(0)
	}
	return m
}

func cloneSums(in map[string]*big.Int) map[string]*big.Int {
	out := make(map[string]*big.Int, len(ledger.AllAssets))
	for _, asset := range ledger.AllAssets {
		if v := in[asset]; v != nil {
			out[asset] = new(big.Int).Set(v)
		} else {
			out[asset] = big.NewInt(0)
		}
	}
	return out
}

func addSums(a, b map[string]*big.Int) map[string]*big.Int {
	out := make(map[string]*big.Int, len(ledger.AllAssets))
	for _, asset := range ledger.AllAssets {
		va := a[asset]
		if va == nil {
			va = big.NewInt(0)
		}
		vb := b[asset]
		if vb == nil {
			vb = big.NewInt(0)
		}
		out[asset] = new(big.Int).Add(va, vb)
	}
	return out
}

func sumsToStrings(in map[string]*big.Int) map[string]string {
	out := make(map[string]string, len(ledger.AllAssets))
	for _, asset := range ledger.AllAssets {
		v := in[asset]
		if v == nil {
			out[asset] = "0"
		} else {
			out[asset] = v.String()
		}
	}
	return out
}

func parseSumsMap(in map[string]string) (map[string]*big.Int, bool) {
	out := make(map[string]*big.Int, len(ledger.AllAssets))
	for _, asset := range ledger.AllAssets {
		raw, ok := in[asset]
		if !ok {
			out[asset] = big.NewInt(0)
			continue
		}
		v, ok := new(big.Int).SetString(raw, 10)
		if !ok || v.Sign() < 0 {
			return nil, false
		}
		out[asset] = v
	}
	return out, true
}

// ComputeLeafHash hashes a user's balances using domain prefix 0x00:
// Keccak256(0x00 || user_id(16B) || nonce(8B BE) || ETH(32B) || USDC(32B) || WBTC(32B) || USDT(32B)).
func ComputeLeafHash(userID uuid.UUID, nonce int64, balancesWei map[string]*big.Int) common.Hash {
	buf := make([]byte, 0, 1+16+8+4*32)
	buf = append(buf, LeafDomainPrefix)
	buf = append(buf, userID[:]...)
	var nonceBuf [8]byte
	binary.BigEndian.PutUint64(nonceBuf[:], uint64(nonce))
	buf = append(buf, nonceBuf[:]...)
	for _, asset := range ledger.AllAssets {
		amt := balancesWei[asset]
		if amt == nil {
			amt = big.NewInt(0)
		}
		buf = append(buf, common.LeftPadBytes(amt.Bytes(), 32)...)
	}
	return crypto.Keccak256Hash(buf)
}

// HashInternalNode computes Keccak256(0x01 || left || right || sumETH_32B || sumUSDC_32B || sumWBTC_32B || sumUSDT_32B).
func HashInternalNode(left, right common.Hash, parentSums map[string]*big.Int) common.Hash {
	buf := make([]byte, 0, 1+32+32+4*32)
	buf = append(buf, NodeDomainPrefix)
	buf = append(buf, left.Bytes()...)
	buf = append(buf, right.Bytes()...)
	for _, asset := range ledger.AllAssets {
		amt := parentSums[asset]
		if amt == nil {
			amt = big.NewInt(0)
		}
		buf = append(buf, common.LeftPadBytes(amt.Bytes(), 32)...)
	}
	return crypto.Keccak256Hash(buf)
}

// BuildMerkleTree builds a domain-separated Keccak-256 Merkle Sum Tree from sorted user summaries.
func BuildMerkleTree(users []ledger.UserLiabilitySummary, blockNumber int64) (*MerkleTree, error) {
	sorted := make([]ledger.UserLiabilitySummary, len(users))
	copy(sorted, users)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].UserID.String() < sorted[j].UserID.String()
	})

	leaves := make([]UserLeaf, 0, len(sorted))
	leafNodes := make([]merkleNode, 0, len(sorted))
	idxMap := make(map[uuid.UUID]int, len(sorted))

	for i, u := range sorted {
		weiMap := make(map[string]*big.Int, len(ledger.AllAssets))
		strMap := make(map[string]string, len(ledger.AllAssets))
		for _, asset := range ledger.AllAssets {
			d := u.Balances[asset]
			if d.IsNegative() {
				d = decimal.Zero
			}
			w, err := ledger.HumanToWei(asset, d)
			if err != nil {
				w = big.NewInt(0)
			}
			weiMap[asset] = w
			strMap[asset] = w.String()
		}
		lh := ComputeLeafHash(u.UserID, blockNumber, weiMap)
		leaves = append(leaves, UserLeaf{
			UserID:   u.UserID,
			Nonce:    blockNumber,
			Balances: strMap,
			LeafHash: lh.Hex(),
		})
		leafNodes = append(leafNodes, merkleNode{
			Hash: lh,
			Sums: weiMap,
		})
		idxMap[u.UserID] = i
	}

	if len(leafNodes) == 0 {
		sentinel := merkleNode{Hash: common.Hash{}, Sums: zeroSums()}
		return &MerkleTree{
			Root:        sentinel.Hash,
			RootSums:    zeroSums(),
			BlockNumber: blockNumber,
			Leaves:      leaves,
			leafIndex:   idxMap,
			levels:      [][]merkleNode{{sentinel}},
		}, nil
	}

	var levels [][]merkleNode
	cur := leafNodes
	for {
		if len(cur) > 1 && len(cur)%2 != 0 {
			// Odd-length levels duplicate a zero-balance sentinel leaf (0x00...00) with zero sums.
			cur = append(cur, merkleNode{Hash: common.Hash{}, Sums: zeroSums()})
		}
		levels = append(levels, cur)
		if len(cur) == 1 {
			break
		}
		next := make([]merkleNode, len(cur)/2)
		for i := 0; i < len(cur); i += 2 {
			pSums := addSums(cur[i].Sums, cur[i+1].Sums)
			pHash := HashInternalNode(cur[i].Hash, cur[i+1].Hash, pSums)
			next[i/2] = merkleNode{Hash: pHash, Sums: pSums}
		}
		cur = next
	}

	rootNode := levels[len(levels)-1][0]
	return &MerkleTree{
		Root:        rootNode.Hash,
		RootSums:    cloneSums(rootNode.Sums),
		BlockNumber: blockNumber,
		Leaves:      leaves,
		leafIndex:   idxMap,
		levels:      levels,
	}, nil
}

// ProofForUser returns the user's leaf and sibling Merkle Sum Tree inclusion path.
func (t *MerkleTree) ProofForUser(userID uuid.UUID) (*UserLeaf, []MerklePathStep, error) {
	idx, ok := t.leafIndex[userID]
	if !ok {
		return nil, nil, errors.New("user not present in merkle snapshot")
	}
	leaf := t.Leaves[idx]
	path := make([]MerklePathStep, 0, len(t.levels)-1)

	pos := idx
	for lvl := 0; lvl < len(t.levels)-1; lvl++ {
		nodes := t.levels[lvl]
		parent := t.levels[lvl+1][pos/2]
		if pos%2 == 0 {
			sib := nodes[pos+1]
			path = append(path, MerklePathStep{
				Position:    "RIGHT",
				SiblingHash: sib.Hash.Hex(),
				SiblingSums: sumsToStrings(sib.Sums),
				NodeSums:    sumsToStrings(parent.Sums),
			})
		} else {
			sib := nodes[pos-1]
			path = append(path, MerklePathStep{
				Position:    "LEFT",
				SiblingHash: sib.Hash.Hex(),
				SiblingSums: sumsToStrings(sib.Sums),
				NodeSums:    sumsToStrings(parent.Sums),
			})
		}
		pos /= 2
	}
	return &leaf, path, nil
}

func verifyProofInternal(rootHex string, leaf UserLeaf, path []MerklePathStep) (map[string]*big.Int, bool) {
	curSums, ok := parseSumsMap(leaf.Balances)
	if !ok {
		return nil, false
	}

	curHash := ComputeLeafHash(leaf.UserID, leaf.Nonce, curSums)
	if !stringsEqualFoldHex(curHash.Hex(), leaf.LeafHash) {
		return nil, false
	}

	for _, step := range path {
		if step.SiblingSums == nil || step.NodeSums == nil {
			return nil, false
		}
		sibSums, ok := parseSumsMap(step.SiblingSums)
		if !ok {
			return nil, false
		}
		nodeSums, ok := parseSumsMap(step.NodeSums)
		if !ok {
			return nil, false
		}

		parentSums := addSums(curSums, sibSums)
		for _, asset := range ledger.AllAssets {
			if parentSums[asset].Cmp(nodeSums[asset]) != 0 {
				return nil, false
			}
		}

		sibHash := common.HexToHash(step.SiblingHash)
		switch step.Position {
		case "RIGHT":
			curHash = HashInternalNode(curHash, sibHash, parentSums)
		case "LEFT":
			curHash = HashInternalNode(sibHash, curHash, parentSums)
		default:
			return nil, false
		}
		curSums = parentSums
	}

	if !stringsEqualFoldHex(curHash.Hex(), rootHex) {
		return nil, false
	}
	return curSums, true
}

// VerifyProof independently recomputes the leaf hash, per-level non-negative sum conservation,
// and Merkle Sum Tree root from the inclusion path.
func VerifyProof(rootHex string, leaf UserLeaf, path []MerklePathStep) bool {
	_, ok := verifyProofInternal(rootHex, leaf, path)
	return ok
}

// VerifyProofWithReserves verifies the inclusion path AND asserts that the root sums equal
// expectedLiabilities (when non-nil) and do not exceed onChainReserves (when non-nil).
func VerifyProofWithReserves(
	rootHex string,
	leaf UserLeaf,
	path []MerklePathStep,
	expectedLiabilities map[string]*big.Int,
	onChainReserves map[string]*big.Int,
) bool {
	rootSums, ok := verifyProofInternal(rootHex, leaf, path)
	if !ok {
		return false
	}
	for _, asset := range ledger.AllAssets {
		if expectedLiabilities != nil {
			exp := expectedLiabilities[asset]
			if exp == nil {
				exp = big.NewInt(0)
			}
			if rootSums[asset].Cmp(exp) != 0 {
				return false
			}
		}
		if onChainReserves != nil {
			res := onChainReserves[asset]
			if res == nil {
				res = big.NewInt(0)
			}
			if rootSums[asset].Cmp(res) > 0 {
				return false
			}
		}
	}
	return true
}

func stringsEqualFoldHex(a, b string) bool {
	return common.HexToHash(a) == common.HexToHash(b)
}

// SnapshotRecord captures a persisted Proof-of-Reserves snapshot and its tree.
type SnapshotRecord struct {
	SnapshotID           uuid.UUID
	MerkleRoot           string
	EVMBlockNumber       int64
	PublishTxHash        string
	TotalUsers           int32
	AssetLiabilities     map[string]AssetSolvency
	OnChainVaultBalances map[string]string
	InvariantOK          bool
	Tree                 *MerkleTree
	CreatedAt            time.Time
}

// SnapshotStore persists and retrieves Merkle PoR snapshots.
type SnapshotStore interface {
	SaveSnapshot(ctx context.Context, rec SnapshotRecord) error
	GetLatestSnapshot(ctx context.Context) (*SnapshotRecord, error)
}

// MemorySnapshotStore implements SnapshotStore in memory.
type MemorySnapshotStore struct {
	mu     sync.Mutex
	latest *SnapshotRecord
}

// NewMemorySnapshotStore constructs a MemorySnapshotStore.
func NewMemorySnapshotStore() *MemorySnapshotStore {
	return &MemorySnapshotStore{}
}

func (m *MemorySnapshotStore) SaveSnapshot(_ context.Context, rec SnapshotRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := rec
	m.latest = &cp
	return nil
}

func (m *MemorySnapshotStore) GetLatestSnapshot(_ context.Context) (*SnapshotRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.latest == nil {
		return nil, nil
	}
	cp := *m.latest
	return &cp, nil
}

// PostgresSnapshotStore persists Merkle snapshots into PostgreSQL 18.
type PostgresSnapshotStore struct {
	pool *pgxpool.Pool
}

// NewPostgresSnapshotStore wraps pgxpool.Pool.
func NewPostgresSnapshotStore(pool *pgxpool.Pool) *PostgresSnapshotStore {
	return &PostgresSnapshotStore{pool: pool}
}

func (p *PostgresSnapshotStore) SaveSnapshot(ctx context.Context, rec SnapshotRecord) error {
	liabJSON, err := json.Marshal(rec.AssetLiabilities)
	if err != nil {
		return err
	}
	vaultJSON, err := json.Marshal(rec.OnChainVaultBalances)
	if err != nil {
		return err
	}
	var pubTx pgtype.Text
	if rec.PublishTxHash != "" {
		pubTx = pgtype.Text{String: rec.PublishTxHash, Valid: true}
	}
	_, err = db.New(p.pool).InsertMerkleSnapshot(ctx, db.InsertMerkleSnapshotParams{
		SnapshotID:           pgtype.UUID{Bytes: rec.SnapshotID, Valid: true},
		MerkleRoot:           rec.MerkleRoot,
		EvmBlockNumber:       rec.EVMBlockNumber,
		PublishTxHash:        pubTx,
		TotalUsers:           rec.TotalUsers,
		AssetLiabilities:     liabJSON,
		OnchainVaultBalances: vaultJSON,
		InvariantOk:          rec.InvariantOK,
	})
	return err
}

func (p *PostgresSnapshotStore) GetLatestSnapshot(ctx context.Context) (*SnapshotRecord, error) {
	row, err := db.New(p.pool).GetLatestMerkleSnapshot(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var liab map[string]AssetSolvency
	_ = json.Unmarshal(row.AssetLiabilities, &liab)
	var vault map[string]string
	_ = json.Unmarshal(row.OnchainVaultBalances, &vault)
	return &SnapshotRecord{
		SnapshotID:           uuid.UUID(row.SnapshotID.Bytes),
		MerkleRoot:           row.MerkleRoot,
		EVMBlockNumber:       row.EvmBlockNumber,
		PublishTxHash:        row.PublishTxHash.String,
		TotalUsers:           row.TotalUsers,
		AssetLiabilities:     liab,
		OnChainVaultBalances: vault,
		InvariantOK:          row.InvariantOk,
		CreatedAt:            row.CreatedAt,
	}, nil
}

// GenerateAndSaveSnapshot runs the solvency check, builds the Merkle Sum Tree, optionally
// publishes the root on-chain via rootPublisher, and saves the snapshot.
func GenerateAndSaveSnapshot(
	ctx context.Context,
	ledgerStore ledger.Store,
	checker *InvariantChecker,
	snapStore SnapshotStore,
	rootPublisher func(ctx context.Context, root common.Hash, blockNum int64) (string, error),
) (*SnapshotRecord, error) {
	report, err := checker.CheckOnce(ctx)
	if err != nil {
		return nil, err
	}
	_, users, err := ledgerStore.SnapshotLiabilities(ctx)
	if err != nil {
		return nil, err
	}
	tree, err := BuildMerkleTree(users, report.BlockNumber)
	if err != nil {
		return nil, err
	}

	var txHash string
	if report.InvariantOK && rootPublisher != nil {
		txHash, _ = rootPublisher(ctx, tree.Root, report.BlockNumber)
	}

	vaultMap := make(map[string]string, len(report.Assets))
	for asset, sol := range report.Assets {
		vaultMap[asset] = sol.OnChainVaultWei
	}

	rec := SnapshotRecord{
		SnapshotID:           uuid.New(),
		MerkleRoot:           tree.Root.Hex(),
		EVMBlockNumber:       report.BlockNumber,
		PublishTxHash:        txHash,
		TotalUsers:           int32(len(users)),
		AssetLiabilities:     report.Assets,
		OnChainVaultBalances: vaultMap,
		InvariantOK:          report.InvariantOK,
		Tree:                 tree,
		CreatedAt:            time.Now().UTC(),
	}
	if err := snapStore.SaveSnapshot(ctx, rec); err != nil {
		return nil, err
	}
	return &rec, nil
}
