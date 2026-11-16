package evm

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/settlecore/settlecore/services/internal/db"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/metrics"
	"github.com/shopspring/decimal"
)

// DepositStatus constants.
const (
	DepositPending   = "PENDING"
	DepositConfirmed = "CONFIRMED"
	DepositOrphaned  = "ORPHANED"
)

// IndexedBlockRecord represents a persisted EVM block header in indexed_blocks.
type IndexedBlockRecord struct {
	BlockNumber int64
	BlockHash   string
	ParentHash  string
	Status      string
}

// DepositRecord represents a persisted on-chain deposit in onchain_deposits.
type DepositRecord struct {
	DepositID      uuid.UUID
	TxHash         string
	LogIndex       int32
	BlockNumber    int64
	BlockHash      string
	Depositor      string
	UserID         uuid.UUID
	Asset          string
	RawAmountWei   *big.Int
	CreditedAmount decimal.Decimal
	Status         string
	JournalTxID    uuid.UUID
}

// IndexStore persists EVM block lineage and deposit finality state.
type IndexStore interface {
	GetLatestCanonicalBlock(ctx context.Context) (*IndexedBlockRecord, error)
	GetCanonicalBlock(ctx context.Context, num int64) (*IndexedBlockRecord, error)
	InsertCanonicalBlock(ctx context.Context, blk BlockHeader) error
	InsertPendingDeposit(ctx context.Context, dep DepositRecord) error
	ListPendingDepositsUpTo(ctx context.Context, maxBlock int64) ([]DepositRecord, error)
	ConfirmDeposit(ctx context.Context, depositID uuid.UUID, journalTxID uuid.UUID) error
	MarkOrphanedAbove(ctx context.Context, ancestorHeight int64) (orphanedBlocks int, orphanedDeposits []DepositRecord, err error)
	ListRecentDeposits(ctx context.Context, userID uuid.UUID, limit int32) ([]DepositRecord, error)
}

// MemoryIndexStore implements IndexStore in memory for deterministic testing and local runs.
type MemoryIndexStore struct {
	mu       sync.Mutex
	blocks   map[string]*IndexedBlockRecord
	canon    map[int64]string
	deposits map[uuid.UUID]*DepositRecord
	depKey   map[string]uuid.UUID
}

// NewMemoryIndexStore constructs an empty MemoryIndexStore.
func NewMemoryIndexStore() *MemoryIndexStore {
	return &MemoryIndexStore{
		blocks:   make(map[string]*IndexedBlockRecord),
		canon:    make(map[int64]string),
		deposits: make(map[uuid.UUID]*DepositRecord),
		depKey:   make(map[string]uuid.UUID),
	}
}

func (m *MemoryIndexStore) GetLatestCanonicalBlock(_ context.Context) (*IndexedBlockRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *IndexedBlockRecord
	for num, hash := range m.canon {
		if best == nil || num > best.BlockNumber {
			blk := *m.blocks[hash]
			best = &blk
		}
	}
	return best, nil
}

func (m *MemoryIndexStore) GetCanonicalBlock(_ context.Context, num int64) (*IndexedBlockRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hash, ok := m.canon[num]
	if !ok {
		return nil, nil
	}
	cp := *m.blocks[hash]
	return &cp, nil
}

func (m *MemoryIndexStore) InsertCanonicalBlock(_ context.Context, blk BlockHeader) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blocks[blk.Hash] = &IndexedBlockRecord{
		BlockNumber: blk.Number,
		BlockHash:   blk.Hash,
		ParentHash:  blk.ParentHash,
		Status:      "CANONICAL",
	}
	m.canon[blk.Number] = blk.Hash
	return nil
}

func (m *MemoryIndexStore) InsertPendingDeposit(_ context.Context, dep DepositRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := fmt.Sprintf("%s:%d:%s", dep.TxHash, dep.LogIndex, dep.BlockHash)
	if _, exists := m.depKey[k]; exists {
		return nil
	}
	cp := dep
	cp.Status = DepositPending
	cp.RawAmountWei = new(big.Int).Set(dep.RawAmountWei)
	m.deposits[dep.DepositID] = &cp
	m.depKey[k] = dep.DepositID
	return nil
}

func (m *MemoryIndexStore) ListPendingDepositsUpTo(_ context.Context, maxBlock int64) ([]DepositRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DepositRecord
	for _, d := range m.deposits {
		if d.Status == DepositPending && d.BlockNumber <= maxBlock {
			cp := *d
			cp.RawAmountWei = new(big.Int).Set(d.RawAmountWei)
			out = append(out, cp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BlockNumber == out[j].BlockNumber {
			return out[i].LogIndex < out[j].LogIndex
		}
		return out[i].BlockNumber < out[j].BlockNumber
	})
	return out, nil
}

func (m *MemoryIndexStore) ConfirmDeposit(_ context.Context, depositID uuid.UUID, journalTxID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.deposits[depositID]; ok && d.Status == DepositPending {
		d.Status = DepositConfirmed
		d.JournalTxID = journalTxID
	}
	return nil
}

func (m *MemoryIndexStore) MarkOrphanedAbove(_ context.Context, ancestorHeight int64) (int, []DepositRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	orphanedBlocks := 0
	for num, hash := range m.canon {
		if num > ancestorHeight {
			if b, ok := m.blocks[hash]; ok {
				b.Status = "ORPHANED"
			}
			delete(m.canon, num)
			orphanedBlocks++
		}
	}
	var orphanedDeps []DepositRecord
	for _, d := range m.deposits {
		if d.BlockNumber > ancestorHeight && (d.Status == DepositPending || d.Status == DepositConfirmed) {
			cp := *d
			cp.RawAmountWei = new(big.Int).Set(d.RawAmountWei)
			orphanedDeps = append(orphanedDeps, cp)
			d.Status = DepositOrphaned
		}
	}
	return orphanedBlocks, orphanedDeps, nil
}

func (m *MemoryIndexStore) ListRecentDeposits(_ context.Context, userID uuid.UUID, limit int32) ([]DepositRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DepositRecord
	for _, d := range m.deposits {
		if d.UserID == userID {
			cp := *d
			cp.RawAmountWei = new(big.Int).Set(d.RawAmountWei)
			out = append(out, cp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].BlockNumber > out[j].BlockNumber
	})
	if limit > 0 && len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}

// PostgresIndexStore implements IndexStore using PostgreSQL 18 and sqlc queries.
type PostgresIndexStore struct {
	pool *pgxpool.Pool
}

// NewPostgresIndexStore wraps pgxpool.Pool.
func NewPostgresIndexStore(pool *pgxpool.Pool) *PostgresIndexStore {
	return &PostgresIndexStore{pool: pool}
}

func (p *PostgresIndexStore) GetLatestCanonicalBlock(ctx context.Context) (*IndexedBlockRecord, error) {
	b, err := db.New(p.pool).GetLatestCanonicalBlock(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &IndexedBlockRecord{
		BlockNumber: b.BlockNumber,
		BlockHash:   b.BlockHash,
		ParentHash:  b.ParentHash,
		Status:      b.Status,
	}, nil
}

func (p *PostgresIndexStore) GetCanonicalBlock(ctx context.Context, num int64) (*IndexedBlockRecord, error) {
	b, err := db.New(p.pool).GetCanonicalBlockByNumber(ctx, num)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &IndexedBlockRecord{
		BlockNumber: b.BlockNumber,
		BlockHash:   b.BlockHash,
		ParentHash:  b.ParentHash,
		Status:      b.Status,
	}, nil
}

func (p *PostgresIndexStore) InsertCanonicalBlock(ctx context.Context, blk BlockHeader) error {
	_, err := db.New(p.pool).InsertIndexedBlock(ctx, db.InsertIndexedBlockParams{
		BlockNumber: blk.Number,
		BlockHash:   blk.Hash,
		ParentHash:  blk.ParentHash,
	})
	return err
}

func (p *PostgresIndexStore) InsertPendingDeposit(ctx context.Context, dep DepositRecord) error {
	_, err := db.New(p.pool).InsertOnchainDeposit(ctx, db.InsertOnchainDepositParams{
		DepositID:        pgtype.UUID{Bytes: dep.DepositID, Valid: true},
		TxHash:           dep.TxHash,
		LogIndex:         dep.LogIndex,
		BlockNumber:      dep.BlockNumber,
		BlockHash:        dep.BlockHash,
		DepositorAddress: dep.Depositor,
		UserID:           pgtype.UUID{Bytes: dep.UserID, Valid: true},
		Asset:            dep.Asset,
		RawAmountWei:     decimal.NewFromBigInt(dep.RawAmountWei, 0),
		CreditedAmount:   dep.CreditedAmount,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

func (p *PostgresIndexStore) ListPendingDepositsUpTo(ctx context.Context, maxBlock int64) ([]DepositRecord, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := db.New(tx).ListPendingDepositsAtOrBelow(ctx, maxBlock)
	if err != nil {
		return nil, err
	}
	out := make([]DepositRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, dbDepositToRecord(r))
	}
	return out, nil
}

func (p *PostgresIndexStore) ConfirmDeposit(ctx context.Context, depositID uuid.UUID, journalTxID uuid.UUID) error {
	_, err := db.New(p.pool).ConfirmOnchainDeposit(ctx, db.ConfirmOnchainDepositParams{
		DepositID:   pgtype.UUID{Bytes: depositID, Valid: true},
		JournalTxID: pgtype.UUID{Bytes: journalTxID, Valid: true},
	})
	return err
}

func (p *PostgresIndexStore) MarkOrphanedAbove(ctx context.Context, ancestorHeight int64) (int, []DepositRecord, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	deps, err := q.MarkDepositsOrphanedAbove(ctx, ancestorHeight)
	if err != nil {
		return 0, nil, err
	}
	blks, err := q.MarkBlocksOrphanedAbove(ctx, ancestorHeight)
	if err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	out := make([]DepositRecord, 0, len(deps))
	for _, d := range deps {
		out = append(out, dbDepositToRecord(d))
	}
	return len(blks), out, nil
}

func (p *PostgresIndexStore) ListRecentDeposits(ctx context.Context, userID uuid.UUID, limit int32) ([]DepositRecord, error) {
	rows, err := db.New(p.pool).ListRecentDepositsByUser(ctx, db.ListRecentDepositsByUserParams{
		UserID: pgtype.UUID{Bytes: userID, Valid: true},
		Limit:  limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]DepositRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, dbDepositToRecord(r))
	}
	return out, nil
}

func dbDepositToRecord(r db.OnchainDeposit) DepositRecord {
	var jtx uuid.UUID
	if r.JournalTxID.Valid {
		jtx = uuid.UUID(r.JournalTxID.Bytes)
	}
	return DepositRecord{
		DepositID:      uuid.UUID(r.DepositID.Bytes),
		TxHash:         r.TxHash,
		LogIndex:       r.LogIndex,
		BlockNumber:    r.BlockNumber,
		BlockHash:      r.BlockHash,
		Depositor:      r.DepositorAddress,
		UserID:         uuid.UUID(r.UserID.Bytes),
		Asset:          r.Asset,
		RawAmountWei:   r.RawAmountWei.BigInt(),
		CreditedAmount: r.CreditedAmount,
		Status:         r.Status,
		JournalTxID:    jtx,
	}
}

// Watcher polls blocks via RPCPool, checks parent-hash lineage, promotes N-confirmed deposits,
// and executes deterministic reorg rollbacks.
type Watcher struct {
	pool          *RPCPool
	indexStore    IndexStore
	ledgerStore   ledger.Store
	confirmations int64
	reorgWindow   int64
	metricsReg    *metrics.Registry
}

// NewWatcher creates a reorg-safe EVM block & deposit watcher.
func NewWatcher(pool *RPCPool, idxStore IndexStore, ledgerStore ledger.Store, confirmations, reorgWindow int64, reg *metrics.Registry) *Watcher {
	if confirmations <= 0 {
		confirmations = 3
	}
	if reorgWindow <= 0 {
		reorgWindow = 64
	}
	if reg == nil {
		reg = metrics.Default
	}
	return &Watcher{
		pool:          pool,
		indexStore:    idxStore,
		ledgerStore:   ledgerStore,
		confirmations: confirmations,
		reorgWindow:   reorgWindow,
		metricsReg:    reg,
	}
}

// PollOnce advances canonical indexing up to the current RPC head, rolling back any detected forks.
func (w *Watcher) PollOnce(ctx context.Context) error {
	w.pool.Probe(ctx)
	head, err := w.pool.HeadNumber(ctx)
	if err != nil {
		return err
	}

	latest, err := w.indexStore.GetLatestCanonicalBlock(ctx)
	if err != nil {
		return err
	}

	nextHeight := int64(0)
	if latest != nil {
		nextHeight = latest.BlockNumber + 1
		// Also check if latest indexed block itself was replaced by a fork at the same or higher height!
		if head >= latest.BlockNumber {
			chainLatest, err := w.pool.BlockByNumber(ctx, latest.BlockNumber)
			if err == nil && chainLatest.Hash != latest.BlockHash {
				ancestor, err := w.handleReorg(ctx, latest.BlockNumber)
				if err != nil {
					return err
				}
				nextHeight = ancestor + 1
			}
		}
	}

	w.metricsReg.SetBlockLag(max(0, head-nextHeight+1))

	for h := nextHeight; h <= head; h++ {
		blk, err := w.pool.BlockByNumber(ctx, h)
		if err != nil {
			return err
		}
		if h > 0 {
			prev, err := w.indexStore.GetCanonicalBlock(ctx, h-1)
			if err != nil {
				return err
			}
			if prev != nil && prev.BlockHash != blk.ParentHash {
				ancestor, err := w.handleReorg(ctx, h-1)
				if err != nil {
					return err
				}
				h = ancestor // loop increments to ancestor + 1
				continue
			}
		}

		if err := w.indexStore.InsertCanonicalBlock(ctx, blk); err != nil {
			return err
		}
		logs, err := w.pool.DepositLogs(ctx, h)
		if err != nil {
			return err
		}
		for _, lg := range logs {
			humanAmt, err := ledger.WeiToHuman(lg.Asset, lg.RawAmountWei)
			if err != nil {
				continue
			}
			if err := w.indexStore.InsertPendingDeposit(ctx, DepositRecord{
				DepositID:      uuid.New(),
				TxHash:         lg.TxHash,
				LogIndex:       lg.LogIndex,
				BlockNumber:    lg.BlockNumber,
				BlockHash:      lg.BlockHash,
				Depositor:      lg.Depositor,
				UserID:         lg.UserID,
				Asset:          lg.Asset,
				RawAmountWei:   lg.RawAmountWei,
				CreditedAmount: humanAmt,
			}); err != nil {
				return err
			}
		}
	}

	// Promote deposits that have reached N confirmations (head - depBlock + 1 >= confirmations).
	finalCutoff := head - (w.confirmations - 1)
	if finalCutoff >= 0 {
		pending, err := w.indexStore.ListPendingDepositsUpTo(ctx, finalCutoff)
		if err != nil {
			return err
		}
		for _, dep := range pending {
			ref := fmt.Sprintf("deposit:%s:%d", dep.TxHash, dep.LogIndex)
			jtx, err := w.ledgerStore.CreditDeposit(ctx, dep.UserID, dep.Asset, dep.CreditedAmount, ref)
			if err != nil {
				return err
			}
			if err := w.indexStore.ConfirmDeposit(ctx, dep.DepositID, jtx); err != nil {
				return err
			}
		}
	}

	w.metricsReg.SetBlockLag(0)
	return nil
}

// Run polls continuously until ctx is canceled.
func (w *Watcher) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		_ = w.PollOnce(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
