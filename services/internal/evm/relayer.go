package evm

import (
	"context"
	"crypto/ecdsa"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/settlecore/settlecore/services/internal/db"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/metrics"
)

// OutboxStore abstracts withdrawal_outbox persistence across PostgreSQL 18 and memory.
type OutboxStore interface {
	ClaimQueued(ctx context.Context, limit int32) ([]ledger.WithdrawalRecord, error)
	ListStuckSubmitted(ctx context.Context, olderThan time.Time) ([]ledger.WithdrawalRecord, error)
	ListSubmitted(ctx context.Context) ([]ledger.WithdrawalRecord, error)
	MarkSubmitted(ctx context.Context, withdrawalID uuid.UUID, eoaNonce int64, txHash string, maxFee, maxPriority *big.Int) error
	RecordGasBump(ctx context.Context, withdrawalID uuid.UUID, txHash string, maxFee, maxPriority *big.Int) error
	GetMaxVoucherNonce(ctx context.Context) (int64, error)
}

// MemoryOutboxStore implements OutboxStore in memory for unit tests and local runs.
type MemoryOutboxStore struct {
	mu    sync.Mutex
	items map[uuid.UUID]*ledger.WithdrawalRecord
}

// NewMemoryOutboxStore constructs an empty MemoryOutboxStore.
func NewMemoryOutboxStore() *MemoryOutboxStore {
	return &MemoryOutboxStore{items: make(map[uuid.UUID]*ledger.WithdrawalRecord)}
}

// Enqueue adds or updates a withdrawal record in memory (useful for tests).
func (m *MemoryOutboxStore) Enqueue(rec ledger.WithdrawalRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := rec
	if rec.RawAmountWei != nil {
		cp.RawAmountWei = new(big.Int).Set(rec.RawAmountWei)
	}
	m.items[rec.WithdrawalID] = &cp
}

// Get returns a copy of a withdrawal record by ID.
func (m *MemoryOutboxStore) Get(id uuid.UUID) (ledger.WithdrawalRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.items[id]
	if !ok {
		return ledger.WithdrawalRecord{}, false
	}
	cp := *r
	return cp, true
}

func (m *MemoryOutboxStore) ClaimQueued(_ context.Context, limit int32) ([]ledger.WithdrawalRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ledger.WithdrawalRecord
	for _, r := range m.items {
		if r.Status == "QUEUED" {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	if limit > 0 && len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryOutboxStore) ListStuckSubmitted(_ context.Context, olderThan time.Time) ([]ledger.WithdrawalRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ledger.WithdrawalRecord
	for _, r := range m.items {
		if r.Status == "SUBMITTED" && !r.UpdatedAt.After(olderThan) {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.Before(out[j].UpdatedAt)
	})
	return out, nil
}

func (m *MemoryOutboxStore) ListSubmitted(_ context.Context) ([]ledger.WithdrawalRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ledger.WithdrawalRecord
	for _, r := range m.items {
		if r.Status == "SUBMITTED" {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (m *MemoryOutboxStore) MarkSubmitted(_ context.Context, withdrawalID uuid.UUID, eoaNonce int64, txHash string, maxFee, maxPriority *big.Int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.items[withdrawalID]; ok {
		r.Status = "SUBMITTED"
		n := eoaNonce
		r.RelayerEOANonce = &n
		r.LastTxHash = txHash
		r.MaxFeePerGasWei = new(big.Int).Set(maxFee)
		r.MaxPriorityFeeWei = new(big.Int).Set(maxPriority)
		r.UpdatedAt = time.Now().UTC()
	}
	return nil
}

func (m *MemoryOutboxStore) RecordGasBump(_ context.Context, withdrawalID uuid.UUID, txHash string, maxFee, maxPriority *big.Int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.items[withdrawalID]; ok {
		r.LastTxHash = txHash
		r.MaxFeePerGasWei = new(big.Int).Set(maxFee)
		r.MaxPriorityFeeWei = new(big.Int).Set(maxPriority)
		r.GasBumpCount++
		r.UpdatedAt = time.Now().UTC()
	}
	return nil
}

// MarkStatus updates status in memory (called when ledger finalizes/refunds).
func (m *MemoryOutboxStore) MarkStatus(withdrawalID uuid.UUID, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.items[withdrawalID]; ok {
		r.Status = status
		r.UpdatedAt = time.Now().UTC()
	}
}

func (m *MemoryOutboxStore) GetMaxVoucherNonce(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var maxN int64
	for _, r := range m.items {
		if r.VoucherNonce > maxN {
			maxN = r.VoucherNonce
		}
	}
	return maxN, nil
}

// PostgresOutboxStore implements OutboxStore against PostgreSQL 18.
type PostgresOutboxStore struct {
	pool *pgxpool.Pool
}

// NewPostgresOutboxStore wraps pgxpool.Pool.
func NewPostgresOutboxStore(pool *pgxpool.Pool) *PostgresOutboxStore {
	return &PostgresOutboxStore{pool: pool}
}

func (p *PostgresOutboxStore) ClaimQueued(ctx context.Context, limit int32) ([]ledger.WithdrawalRecord, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := db.New(tx).ClaimQueuedWithdrawals(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.WithdrawalRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, dbOutboxToRecord(r))
	}
	return out, nil
}

func (p *PostgresOutboxStore) ListStuckSubmitted(ctx context.Context, olderThan time.Time) ([]ledger.WithdrawalRecord, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := db.New(tx).ListStuckSubmittedWithdrawals(ctx, olderThan)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.WithdrawalRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, dbOutboxToRecord(r))
	}
	return out, nil
}

func (p *PostgresOutboxStore) ListSubmitted(ctx context.Context) ([]ledger.WithdrawalRecord, error) {
	return p.ListStuckSubmitted(ctx, time.Now().UTC().Add(time.Hour))
}

func (p *PostgresOutboxStore) MarkSubmitted(ctx context.Context, withdrawalID uuid.UUID, eoaNonce int64, txHash string, maxFee, maxPriority *big.Int) error {
	_, err := db.New(p.pool).MarkWithdrawalSubmitted(ctx, db.MarkWithdrawalSubmittedParams{
		WithdrawalID:      pgtype.UUID{Bytes: withdrawalID, Valid: true},
		RelayerEoaNonce:   pgtype.Int8{Int64: eoaNonce, Valid: true},
		LastTxHash:        pgtype.Text{String: txHash, Valid: true},
		MaxFeePerGasWei:   pgtype.Numeric{Int: maxFee, Valid: true},
		MaxPriorityFeeWei: pgtype.Numeric{Int: maxPriority, Valid: true},
	})
	return err
}

func (p *PostgresOutboxStore) RecordGasBump(ctx context.Context, withdrawalID uuid.UUID, txHash string, maxFee, maxPriority *big.Int) error {
	_, err := db.New(p.pool).BumpWithdrawalGas(ctx, db.BumpWithdrawalGasParams{
		WithdrawalID:      pgtype.UUID{Bytes: withdrawalID, Valid: true},
		LastTxHash:        pgtype.Text{String: txHash, Valid: true},
		MaxFeePerGasWei:   pgtype.Numeric{Int: maxFee, Valid: true},
		MaxPriorityFeeWei: pgtype.Numeric{Int: maxPriority, Valid: true},
	})
	return err
}

func (p *PostgresOutboxStore) GetMaxVoucherNonce(ctx context.Context) (int64, error) {
	return db.New(p.pool).GetMaxVoucherNonce(ctx)
}

func dbOutboxToRecord(w db.WithdrawalOutbox) ledger.WithdrawalRecord {
	var eoaNonce *int64
	if w.RelayerEoaNonce.Valid {
		v := w.RelayerEoaNonce.Int64
		eoaNonce = &v
	}
	return ledger.WithdrawalRecord{
		WithdrawalID:      uuid.UUID(w.WithdrawalID.Bytes),
		IdempotencyKey:    uuid.UUID(w.IdempotencyKey.Bytes),
		UserID:            uuid.UUID(w.UserID.Bytes),
		RecipientAddress:  w.RecipientAddress,
		Asset:             w.Asset,
		HumanAmount:       w.HumanAmount,
		RawAmountWei:      w.RawAmountWei.BigInt(),
		SigningTier:       w.SigningTier,
		VoucherNonce:      w.VoucherNonce,
		RelayerEOANonce:   eoaNonce,
		LastTxHash:        w.LastTxHash.String,
		MaxFeePerGasWei:   w.MaxFeePerGasWei.Int,
		MaxPriorityFeeWei: w.MaxPriorityFeeWei.Int,
		GasBumpCount:      w.GasBumpCount,
		Status:            w.Status,
		CreatedAt:         w.CreatedAt,
		UpdatedAt:         w.UpdatedAt,
	}
}

// RelayerConfig holds keys, caps, and token addresses for the withdrawal relayer.
type RelayerConfig struct {
	ChainID        int64
	VaultAddress   common.Address
	TokenAddrs     map[string]common.Address
	HotKey         *ecdsa.PrivateKey
	ColdKey        *ecdsa.PrivateKey
	RelayerKey     *ecdsa.PrivateKey
	StuckThreshold time.Duration
	HotPerTxCaps   map[string]*big.Int
	HotDailyCaps   map[string]*big.Int
}

// Relayer consumes withdrawal_outbox, signs EIP-712 vouchers, allocates serialized EOA nonces,
// broadcasts EIP-1559 transactions, bumps stuck transactions, and finalizes confirmed receipts.
type Relayer struct {
	cfg         RelayerConfig
	pool        *RPCPool
	outbox      OutboxStore
	ledgerStore ledger.Store
	signer      *EIP712Signer
	relayerAddr common.Address
	metricsReg  *metrics.Registry

	nonceMu      sync.Mutex
	nextEOANonce uint64
	nonceInit    bool

	dailyMu     sync.Mutex
	dailyWindow int64
	dailyOut    map[string]*big.Int
}

// NewRelayer constructs a Relayer with EIP-712 signing and gas-bumping capabilities.
func NewRelayer(cfg RelayerConfig, pool *RPCPool, outbox OutboxStore, ledgerStore ledger.Store, reg *metrics.Registry) *Relayer {
	if cfg.StuckThreshold <= 0 {
		cfg.StuckThreshold = 12 * time.Second
	}
	if reg == nil {
		reg = metrics.Default
	}
	var relAddr common.Address
	if cfg.RelayerKey != nil {
		relAddr = crypto.PubkeyToAddress(cfg.RelayerKey.PublicKey)
	}
	return &Relayer{
		cfg:         cfg,
		pool:        pool,
		outbox:      outbox,
		ledgerStore: ledgerStore,
		signer:      NewEIP712Signer(cfg.ChainID, cfg.VaultAddress),
		relayerAddr: relAddr,
		metricsReg:  reg,
		dailyOut:    make(map[string]*big.Int),
	}
}

func (r *Relayer) allocateEOANonce(ctx context.Context) (uint64, error) {
	r.nonceMu.Lock()
	defer r.nonceMu.Unlock()
	if !r.nonceInit {
		n, err := r.pool.PendingNonce(ctx, r.relayerAddr)
		if err != nil {
			return 0, err
		}
		r.nextEOANonce = n
		r.nonceInit = true
	}
	n := r.nextEOANonce
	r.nextEOANonce++
	return n, nil
}

// EvaluateTier classifies a withdrawal as HOT or COLD_COSIGNED using per-tx and daily caps.
func (r *Relayer) EvaluateTier(asset string, rawAmountWei *big.Int) string {
	r.dailyMu.Lock()
	defer r.dailyMu.Unlock()
	day := time.Now().UTC().Unix() / 86400
	if day != r.dailyWindow {
		r.dailyWindow = day
		r.dailyOut = make(map[string]*big.Int)
	}
	perTx := r.cfg.HotPerTxCaps[asset]
	dailyCap := r.cfg.HotDailyCaps[asset]
	used := r.dailyOut[asset]
	return ClassifyTier(rawAmountWei, perTx, dailyCap, used)
}

func (r *Relayer) recordHotSpend(asset string, rawAmountWei *big.Int) {
	r.dailyMu.Lock()
	defer r.dailyMu.Unlock()
	day := time.Now().UTC().Unix() / 86400
	if day != r.dailyWindow {
		r.dailyWindow = day
		r.dailyOut = make(map[string]*big.Int)
	}
	cur := r.dailyOut[asset]
	if cur == nil {
		cur = big.NewInt(0)
	}
	r.dailyOut[asset] = new(big.Int).Add(cur, rawAmountWei)
}

func (r *Relayer) buildWithdrawTx(rec ledger.WithdrawalRecord, eoaNonce uint64, maxFee, maxTip *big.Int) (TxSubmission, error) {
	var wid [32]byte
	copy(wid[16:], rec.WithdrawalID[:])

	tokenAddr := r.cfg.TokenAddrs[strings.ToUpper(rec.Asset)]
	deadline := big.NewInt(time.Now().UTC().Add(1 * time.Hour).Unix())
	voucher := WithdrawalVoucher{
		WithdrawalID: wid,
		Recipient:    common.HexToAddress(rec.RecipientAddress),
		Token:        tokenAddr,
		Amount:       rec.RawAmountWei,
		Nonce:        big.NewInt(rec.VoucherNonce),
		Deadline:     deadline,
	}

	hotSig, err := r.signer.SignVoucher(r.cfg.HotKey, voucher)
	if err != nil {
		return TxSubmission{}, fmt.Errorf("hot sign: %w", err)
	}
	var coldSig []byte
	if rec.SigningTier == SigningTierColdCosigned {
		if r.cfg.ColdKey == nil {
			return TxSubmission{}, errors.New("cold signer key required for COLD_COSIGNED withdrawal")
		}
		coldSig, err = r.signer.SignVoucher(r.cfg.ColdKey, voucher)
		if err != nil {
			return TxSubmission{}, fmt.Errorf("cold sign: %w", err)
		}
	}

	callData := encodeWithdrawCalldata(voucher, hotSig, coldSig)
	txData := &types.DynamicFeeTx{
		ChainID:   big.NewInt(r.cfg.ChainID),
		Nonce:     eoaNonce,
		GasTipCap: maxTip,
		GasFeeCap: maxFee,
		Gas:       220_000,
		To:        &r.cfg.VaultAddress,
		Value:     big.NewInt(0),
		Data:      callData,
	}

	var signed *types.Transaction
	if r.cfg.RelayerKey != nil {
		signed, err = types.SignNewTx(r.cfg.RelayerKey, types.LatestSignerForChainID(big.NewInt(r.cfg.ChainID)), txData)
		if err != nil {
			return TxSubmission{}, err
		}
	} else {
		signed = types.NewTx(txData)
	}

	return TxSubmission{
		Nonce:                eoaNonce,
		To:                   r.cfg.VaultAddress,
		Data:                 callData,
		MaxFeePerGasWei:      maxFee,
		MaxPriorityFeePerGas: maxTip,
		RawTx:                signed,
	}, nil
}

// encodeWithdrawCalldata ABI-encodes EscrowVault.withdraw(WithdrawalVoucher,bytes,bytes).
func encodeWithdrawCalldata(v WithdrawalVoucher, hotSig, coldSig []byte) []byte {
	// Selector: keccak256("withdraw((bytes32,address,address,uint256,uint256,uint256),bytes,bytes)")[:4]
	selector := crypto.Keccak256([]byte("withdraw((bytes32,address,address,uint256,uint256,uint256),bytes,bytes)"))[:4]
	// Head: 6 static words for voucher + 2 dynamic offsets (at word 6 and word 7 => 8*32 = 256)
	padBytes := func(b []byte) []byte {
		rem := len(b) % 32
		if rem == 0 {
			return b
		}
		out := make([]byte, len(b)+(32-rem))
		copy(out, b)
		return out
	}

	hotPadded := padBytes(hotSig)
	coldPadded := padBytes(coldSig)
	hotOffset := 8 * 32
	coldOffset := hotOffset + 32 + len(hotPadded)

	var out []byte
	out = append(out, selector...)
	out = append(out, v.WithdrawalID[:]...)
	out = append(out, common.LeftPadBytes(v.Recipient.Bytes(), 32)...)
	out = append(out, common.LeftPadBytes(v.Token.Bytes(), 32)...)
	out = append(out, common.LeftPadBytes(v.Amount.Bytes(), 32)...)
	out = append(out, common.LeftPadBytes(v.Nonce.Bytes(), 32)...)
	out = append(out, common.LeftPadBytes(v.Deadline.Bytes(), 32)...)

	var offBuf [32]byte
	binary.BigEndian.PutUint64(offBuf[24:32], uint64(hotOffset))
	out = append(out, offBuf[:]...)
	binary.BigEndian.PutUint64(offBuf[24:32], uint64(coldOffset))
	out = append(out, offBuf[:]...)

	binary.BigEndian.PutUint64(offBuf[24:32], uint64(len(hotSig)))
	out = append(out, offBuf[:]...)
	out = append(out, hotPadded...)

	binary.BigEndian.PutUint64(offBuf[24:32], uint64(len(coldSig)))
	out = append(out, offBuf[:]...)
	out = append(out, coldPadded...)
	return out
}

// ProcessQueuedOnce claims QUEUED withdrawals, signs, and broadcasts EIP-1559 transactions.
func (r *Relayer) ProcessQueuedOnce(ctx context.Context) (int, error) {
	items, err := r.outbox.ClaimQueued(ctx, 16)
	if err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, nil
	}

	baseFee, tip, err := r.pool.SuggestGasFees(ctx)
	if err != nil {
		return 0, err
	}
	// maxFeePerGas = 2 * baseFee + tip (ADR-004 §3.3)
	maxFee := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tip)

	processed := 0
	for _, rec := range items {
		nonce, err := r.allocateEOANonce(ctx)
		if err != nil {
			return processed, err
		}
		sub, err := r.buildWithdrawTx(rec, nonce, maxFee, tip)
		if err != nil {
			return processed, err
		}
		txHash, err := r.pool.BroadcastTx(ctx, sub)
		if err != nil {
			return processed, err
		}
		if err := r.outbox.MarkSubmitted(ctx, rec.WithdrawalID, int64(nonce), txHash, maxFee, tip); err != nil {
			return processed, err
		}
		if rec.SigningTier == SigningTierHot {
			r.recordHotSpend(rec.Asset, rec.RawAmountWei)
		}
		processed++
	}
	return processed, nil
}

// PollReceiptsOnce finalizes CONFIRMED withdrawals or refunds FAILED transactions.
func (r *Relayer) PollReceiptsOnce(ctx context.Context) (int, error) {
	items, err := r.outbox.ListSubmitted(ctx)
	if err != nil {
		return 0, err
	}
	finalized := 0
	for _, rec := range items {
		if rec.LastTxHash == "" {
			continue
		}
		rcpt, err := r.pool.TxReceipt(ctx, rec.LastTxHash)
		if err != nil || !rcpt.Confirmed {
			continue
		}
		if rcpt.Succeeded {
			if err := r.ledgerStore.FinalizeWithdrawal(ctx, rec.WithdrawalID, rec.UserID, rec.Asset, rec.HumanAmount); err != nil {
				return finalized, err
			}
			if mem, ok := r.outbox.(*MemoryOutboxStore); ok {
				mem.MarkStatus(rec.WithdrawalID, "CONFIRMED")
			}
		} else {
			if err := r.ledgerStore.RefundFailedWithdrawal(ctx, rec.WithdrawalID, rec.UserID, rec.Asset, rec.HumanAmount); err != nil {
				return finalized, err
			}
			if mem, ok := r.outbox.(*MemoryOutboxStore); ok {
				mem.MarkStatus(rec.WithdrawalID, "FAILED_REFUNDED")
			}
		}
		finalized++
	}
	return finalized, nil
}

// Run runs outbox processing, stuck-tx gas bumping, and receipt finalization until ctx ends.
func (r *Relayer) Run(ctx context.Context, pollInterval time.Duration) error {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		_, _ = r.ProcessQueuedOnce(ctx)
		_, _ = r.BumpStuckOnce(ctx)
		_, _ = r.PollReceiptsOnce(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
