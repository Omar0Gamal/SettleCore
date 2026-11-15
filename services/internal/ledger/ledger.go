package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/shopspring/decimal"
)

// Account types.
const (
	AccountTypeUser        = "USER"
	AccountTypeEscrowVault = "SYSTEM_ESCROW_VAULT"
	AccountTypeFeeTreasury = "SYSTEM_FEE_TREASURY"
)

// Ledger entry types.
const (
	EntryTypeDeposit       = "DEPOSIT"
	EntryTypeOrderReserve  = "ORDER_RESERVE"
	EntryTypeTradeSettle   = "TRADE_SETTLE"
	EntryTypeFee           = "FEE"
	EntryTypeCancelRelease = "CANCEL_RELEASE"
	EntryTypeWithdrawLock  = "WITHDRAW_LOCK"
	EntryTypeWithdrawFinal = "WITHDRAW_FINAL"
	EntryTypeReorgReversal = "REORG_REVERSAL"
)

// Ledger directions.
const (
	DirectionDebit  = "DEBIT"
	DirectionCredit = "CREDIT"
)

// Order statuses.
const (
	OrderStatusReserved           = "RESERVED"
	OrderStatusOpen               = "OPEN"
	OrderStatusPartiallyFilled    = "PARTIALLY_FILLED"
	OrderStatusFilled             = "FILLED"
	OrderStatusCanceled           = "CANCELED"
	OrderStatusRejectedReleased   = "REJECTED_RELEASED"
	OrderStatusAbortedUnsubmitted = "ABORTED_UNSUBMITTED"
)

// Order sides, types, time-in-force.
const (
	SideBuy  = "BUY"
	SideSell = "SELL"

	OrderTypeLimit  = "LIMIT"
	OrderTypeMarket = "MARKET"

	TIFGTC = "GTC"
	TIFIOC = "IOC"
	TIFFOK = "FOK"
)

// Well-known deterministic UUIDs for system accounts and default demo trader.
var (
	SystemVaultUserID   = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	SystemFeeUserID     = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	DefaultTraderUserID = uuid.MustParse("8e3b91c0-4a12-4e8d-9b1c-001122334455")
	MarketMakerUserID   = uuid.MustParse("9a4c12d0-5b23-4f9e-8c2d-112233445566")
)

// Common ledger errors.
var (
	ErrInsufficientAvailable = errors.New("ERR_INSUFFICIENT_AVAILABLE_BALANCE")
	ErrInsufficientLocked    = errors.New("insufficient locked balance")
	ErrIdempotencyConflict   = errors.New("ERR_IDEMPOTENCY_CONFLICT")
	ErrOrderNotFound         = errors.New("order not found")
	ErrUnbalancedJournal     = errors.New("double-entry journal tx is unbalanced")
	ErrReorgUnderflow        = errors.New("reorg reversal would drive account negative")
)

// AccountBalance represents an account's balances in human decimal units.
type AccountBalance struct {
	AccountID   uuid.UUID       `json:"account_id"`
	UserID      uuid.UUID       `json:"user_id"`
	Asset       string          `json:"asset"`
	AccountType string          `json:"account_type"`
	Available   decimal.Decimal `json:"available"`
	Locked      decimal.Decimal `json:"locked"`
	Version     int64           `json:"version"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// LedgerEntryRecord is a single immutable leg of a double-entry journal transaction.
type LedgerEntryRecord struct {
	EntryID      int64           `json:"entry_id"`
	JournalTxID  uuid.UUID       `json:"journal_tx_id"`
	AccountID    uuid.UUID       `json:"account_id"`
	Asset        string          `json:"asset"`
	EntryType    string          `json:"entry_type"`
	Direction    string          `json:"direction"`
	Amount       decimal.Decimal `json:"amount"`
	BalanceAfter decimal.Decimal `json:"balance_after"`
	ReferenceID  string          `json:"reference_id"`
	CreatedAt    time.Time       `json:"created_at"`
}

// OrderRecord tracks an order across pre-trade reservation and batch settlement.
type OrderRecord struct {
	OrderID        int64           `json:"order_id"`
	IdempotencyKey uuid.UUID       `json:"idempotency_key"`
	PayloadHash    string          `json:"payload_hash"`
	UserID         uuid.UUID       `json:"user_id"`
	MarketID       string          `json:"market_id"`
	Side           string          `json:"side"`
	OrderType      string          `json:"order_type"`
	TimeInForce    string          `json:"time_in_force"`
	Price          decimal.Decimal `json:"price"`
	Quantity       decimal.Decimal `json:"quantity"`
	FilledQuantity decimal.Decimal `json:"filled_quantity"`
	LockedAmount   decimal.Decimal `json:"locked_amount"`
	WalSeq         int64           `json:"wal_seq"`
	Status         string          `json:"status"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// TradeRecord tracks an executed trade persisted by the settlement worker.
type TradeRecord struct {
	TradeID       int64           `json:"trade_id"`
	WalSeq        int64           `json:"wal_seq"`
	MarketID      string          `json:"market_id"`
	MakerOrderID  int64           `json:"maker_order_id"`
	TakerOrderID  int64           `json:"taker_order_id"`
	MakerUserID   uuid.UUID       `json:"maker_user_id"`
	TakerUserID   uuid.UUID       `json:"taker_user_id"`
	Price         decimal.Decimal `json:"price"`
	Quantity      decimal.Decimal `json:"quantity"`
	QuoteNotional decimal.Decimal `json:"quote_notional"`
	MakerFee      decimal.Decimal `json:"maker_fee"`
	TakerFee      decimal.Decimal `json:"taker_fee"`
	ExecutedAt    time.Time       `json:"executed_at"`
}

// ReserveOrderReq describes a pre-trade reservation request.
type ReserveOrderReq struct {
	OrderID        int64
	IdempotencyKey uuid.UUID
	UserID         uuid.UUID
	MarketID       string
	Side           string
	OrderType      string
	TimeInForce    string
	Price          decimal.Decimal
	Quantity       decimal.Decimal
	ReserveAmount  decimal.Decimal
}

// SettlementFill describes a single fill in a WAL settlement batch.
type SettlementFill struct {
	TradeID       int64
	WalSeq        int64
	MarketID      string
	MakerOrderID  int64
	TakerOrderID  int64
	MakerUserID   uuid.UUID
	TakerUserID   uuid.UUID
	TakerSide     string // BUY or SELL
	ExecPrice     decimal.Decimal
	FillQty       decimal.Decimal
	QuoteNotional decimal.Decimal
	MakerFee      decimal.Decimal
	TakerFee      decimal.Decimal
	MakerTerminal bool
	TakerTerminal bool
}

// SettlementCancel describes an order cancellation or expiration in a WAL batch.
type SettlementCancel struct {
	OrderID   int64
	WalSeq    int64
	MarketID  string
	UserID    uuid.UUID
	NewStatus string // CANCELED or REJECTED_RELEASED
}

// SettlementBatch bundles fills and cancels up to MaxWalSeq for one market.
type SettlementBatch struct {
	MarketID  string
	MaxWalSeq int64
	Fills     []SettlementFill
	Cancels   []SettlementCancel
}

// WithdrawalRequest describes a user withdrawal request to lock and queue.
type WithdrawalRequest struct {
	WithdrawalID     uuid.UUID
	IdempotencyKey   uuid.UUID
	UserID           uuid.UUID
	RecipientAddress string
	Asset            string
	HumanAmount      decimal.Decimal
	RawAmountWei     *big.Int
	SigningTier      string
	VoucherNonce     int64
}

// WithdrawalRecord represents a row in withdrawal_outbox.
type WithdrawalRecord struct {
	WithdrawalID      uuid.UUID       `json:"withdrawal_id"`
	IdempotencyKey    uuid.UUID       `json:"idempotency_key"`
	UserID            uuid.UUID       `json:"user_id"`
	RecipientAddress  string          `json:"recipient_address"`
	Asset             string          `json:"asset"`
	HumanAmount       decimal.Decimal `json:"human_amount"`
	RawAmountWei      *big.Int        `json:"raw_amount_wei"`
	SigningTier       string          `json:"signing_tier"`
	VoucherNonce      int64           `json:"voucher_nonce"`
	RelayerEOANonce   *int64          `json:"relayer_eoa_nonce,omitempty"`
	LastTxHash        string          `json:"last_tx_hash,omitempty"`
	MaxFeePerGasWei   *big.Int        `json:"max_fee_per_gas_wei,omitempty"`
	MaxPriorityFeeWei *big.Int        `json:"max_priority_fee_wei,omitempty"`
	GasBumpCount      int32           `json:"gas_bump_count"`
	Status            string          `json:"status"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// UserLiabilitySummary aggregates a user's balances across all assets for Merkle PoR.
type UserLiabilitySummary struct {
	UserID   uuid.UUID
	Balances map[string]decimal.Decimal // available + locked per asset
}

// AssetTotal holds aggregated liabilities per asset.
type AssetTotal struct {
	UserTotal decimal.Decimal
	FeeTotal  decimal.Decimal
}

// Store abstracts transactional ledger operations over PostgreSQL 18 or an in-memory ACID store.
type Store interface {
	EnsureUserAccounts(ctx context.Context, userID uuid.UUID) error
	ListUserBalances(ctx context.Context, userID uuid.UUID) ([]AccountBalance, error)
	CreditDeposit(ctx context.Context, userID uuid.UUID, asset string, amount decimal.Decimal, refID string) (uuid.UUID, error)
	ReverseDeposit(ctx context.Context, userID uuid.UUID, asset string, amount decimal.Decimal, refID string) (uuid.UUID, error)
	ReserveOrder(ctx context.Context, req ReserveOrderReq) (*OrderRecord, bool, error)
	UpdateOrderStatus(ctx context.Context, orderID int64, walSeq int64, status string) error
	ReleaseOrderReservation(ctx context.Context, orderID int64, walSeq int64, newStatus string) (decimal.Decimal, error)
	ApplySettlementBatch(ctx context.Context, batch SettlementBatch) (int, error)
	GetLastSettledWalSeq(ctx context.Context, marketID string) (int64, error)
	GetOrder(ctx context.Context, orderID int64) (*OrderRecord, error)
	ListOrdersByUser(ctx context.Context, userID uuid.UUID, limit int32) ([]OrderRecord, error)
	ListRecentTrades(ctx context.Context, marketID string, limit int32) ([]TradeRecord, error)
	ListReservedOrders(ctx context.Context) ([]OrderRecord, error)
	QueueWithdrawal(ctx context.Context, req WithdrawalRequest) (*WithdrawalRecord, bool, error)
	FinalizeWithdrawal(ctx context.Context, withdrawalID uuid.UUID, userID uuid.UUID, asset string, amount decimal.Decimal) error
	RefundFailedWithdrawal(ctx context.Context, withdrawalID uuid.UUID, userID uuid.UUID, asset string, amount decimal.Decimal) error
	GetLedgerEntriesByTx(ctx context.Context, journalTxID uuid.UUID) ([]LedgerEntryRecord, error)
	SnapshotLiabilities(ctx context.Context) (map[string]AssetTotal, []UserLiabilitySummary, error)
	NextOrderID() int64
}

// DeterministicAccountID derives a deterministic UUID for (userID, asset, accountType)
// so both MemoryStore and PostgresStore produce identical account_id lock orderings.
func DeterministicAccountID(userID uuid.UUID, asset, accountType string) uuid.UUID {
	key := fmt.Sprintf("%s:%s:%s", userID.String(), asset, accountType)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key))
}

// OrderPayloadHash computes a canonical SHA-256 hash of order request fields for idempotency checks.
func OrderPayloadHash(userID uuid.UUID, marketID, side, orderType, tif string, price, qty decimal.Decimal) string {
	h := sha256.New()
	_, _ = h.Write([]byte(userID.String()))
	_, _ = h.Write([]byte("|" + marketID + "|" + side + "|" + orderType + "|" + tif + "|"))
	_, _ = h.Write([]byte(price.String() + "|" + qty.String()))
	return hex.EncodeToString(h.Sum(nil))
}

// ComputeOrderReserveAmount computes the pre-trade reserve amount in the locked asset:
// BUY locks quote (notional + maxFee); SELL locks base (quantity).
func ComputeOrderReserveAmount(marketID, side string, price, qty decimal.Decimal, takerFeeBps, makerFeeBps int64) (string, decimal.Decimal, error) {
	baseAsset, quoteAsset, err := MarketAssets(marketID)
	if err != nil {
		return "", decimal.Zero, err
	}
	qty1e8, err := QtyToCLOBInt64(marketID, qty)
	if err != nil {
		return "", decimal.Zero, err
	}

	if side == SideSell {
		return baseAsset, CLOBInt64ToHuman(qty1e8), nil
	}
	if side != SideBuy {
		return "", decimal.Zero, fmt.Errorf("%w: invalid side %s", ErrInvalidPrecision, side)
	}

	price1e8, err := PriceToCLOBInt64(marketID, price)
	if err != nil {
		return "", decimal.Zero, err
	}
	notional1e8, err := ComputeQuoteNotional1e8(price1e8, qty1e8)
	if err != nil {
		return "", decimal.Zero, err
	}
	maxFeeBps := takerFeeBps
	if makerFeeBps > maxFeeBps {
		maxFeeBps = makerFeeBps
	}
	maxFee1e8, err := ComputeFeeCeil1e8(notional1e8, maxFeeBps)
	if err != nil {
		return "", decimal.Zero, err
	}
	return quoteAsset, CLOBInt64ToHuman(notional1e8 + maxFee1e8), nil
}

// =============================================================================
// In-Memory ACID Double-Entry Store (for unit tests & standalone local runs)
// =============================================================================

// MemoryStore implements Store with strict ACID conservation checks and deterministic locking.
type MemoryStore struct {
	mu              sync.Mutex
	accounts        map[uuid.UUID]*AccountBalance
	accountLookup   map[string]uuid.UUID // "userID:asset:type" -> accountID
	entries         []LedgerEntryRecord
	entriesByTx     map[uuid.UUID][]LedgerEntryRecord
	txByRef         map[string]uuid.UUID
	orders          map[int64]*OrderRecord
	ordersByIdem    map[uuid.UUID]int64
	trades          []TradeRecord
	tradesByWalSeq  map[int64]struct{}
	checkpoints     map[string]int64
	withdrawals     map[uuid.UUID]*WithdrawalRecord
	withdrawalsIdem map[uuid.UUID]uuid.UUID
	nextEntryID     int64
	nextOrderID     int64
}

// NewMemoryStore constructs an initialized MemoryStore with system vault and fee treasury accounts.
func NewMemoryStore() *MemoryStore {
	s := &MemoryStore{
		accounts:        make(map[uuid.UUID]*AccountBalance),
		accountLookup:   make(map[string]uuid.UUID),
		entriesByTx:     make(map[uuid.UUID][]LedgerEntryRecord),
		txByRef:         make(map[string]uuid.UUID),
		orders:          make(map[int64]*OrderRecord),
		ordersByIdem:    make(map[uuid.UUID]int64),
		tradesByWalSeq:  make(map[int64]struct{}),
		checkpoints:     map[string]int64{MarketETHUSDC: 0, MarketBTCUSDC: 0},
		withdrawals:     make(map[uuid.UUID]*WithdrawalRecord),
		withdrawalsIdem: make(map[uuid.UUID]uuid.UUID),
		nextEntryID:     1,
		nextOrderID:     9000001,
	}
	for _, asset := range AllAssets {
		s.ensureAccountLocked(SystemVaultUserID, asset, AccountTypeEscrowVault)
		s.ensureAccountLocked(SystemFeeUserID, asset, AccountTypeFeeTreasury)
	}
	return s
}

func accountKey(userID uuid.UUID, asset, accountType string) string {
	return userID.String() + ":" + asset + ":" + accountType
}

func (s *MemoryStore) ensureAccountLocked(userID uuid.UUID, asset, accountType string) *AccountBalance {
	k := accountKey(userID, asset, accountType)
	if id, ok := s.accountLookup[k]; ok {
		return s.accounts[id]
	}
	id := DeterministicAccountID(userID, asset, accountType)
	acc := &AccountBalance{
		AccountID:   id,
		UserID:      userID,
		Asset:       asset,
		AccountType: accountType,
		Available:   decimal.Zero,
		Locked:      decimal.Zero,
		Version:     1,
		UpdatedAt:   time.Now().UTC(),
	}
	s.accounts[id] = acc
	s.accountLookup[k] = id
	return acc
}

func (s *MemoryStore) NextOrderID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextOrderID
	s.nextOrderID++
	return id
}

func (s *MemoryStore) EnsureUserAccounts(_ context.Context, userID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, asset := range AllAssets {
		s.ensureAccountLocked(userID, asset, AccountTypeUser)
	}
	return nil
}

func (s *MemoryStore) ListUserBalances(_ context.Context, userID uuid.UUID) ([]AccountBalance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AccountBalance, 0, len(AllAssets))
	for _, asset := range AllAssets {
		acc := s.ensureAccountLocked(userID, asset, AccountTypeUser)
		out = append(out, *acc)
	}
	return out, nil
}

// JournalLeg describes a single debit or credit leg of a double-entry journal transaction.
type JournalLeg struct {
	AccountID    uuid.UUID
	Asset        string
	EntryType    string
	Direction    string
	Amount       decimal.Decimal
	BalanceAfter decimal.Decimal
}

type pendingLeg = JournalLeg

func (s *MemoryStore) commitJournalLocked(journalTxID uuid.UUID, refID string, legs []pendingLeg) error {
	if len(legs) < 2 {
		return fmt.Errorf("%w: journal tx requires at least 2 legs, got %d", ErrUnbalancedJournal, len(legs))
	}
	// Verify per-asset conservation: sum(credits) - sum(debits) == 0.
	nets := make(map[string]decimal.Decimal)
	for _, leg := range legs {
		if !leg.amountPositive() {
			return fmt.Errorf("%w: non-positive leg amount %s", ErrUnbalancedJournal, leg.Amount)
		}
		cur := nets[leg.Asset]
		switch leg.Direction {
		case DirectionCredit:
			nets[leg.Asset] = cur.Add(leg.Amount)
		case DirectionDebit:
			nets[leg.Asset] = cur.Sub(leg.Amount)
		default:
			return fmt.Errorf("%w: unknown direction %s", ErrUnbalancedJournal, leg.Direction)
		}
	}
	for asset, net := range nets {
		if !net.IsZero() {
			return fmt.Errorf("%w: asset %s net=%s", ErrUnbalancedJournal, asset, net.String())
		}
	}

	now := time.Now().UTC()
	for _, leg := range legs {
		rec := LedgerEntryRecord{
			EntryID:      s.nextEntryID,
			JournalTxID:  journalTxID,
			AccountID:    leg.AccountID,
			Asset:        leg.Asset,
			EntryType:    leg.EntryType,
			Direction:    leg.Direction,
			Amount:       leg.Amount,
			BalanceAfter: leg.BalanceAfter,
			ReferenceID:  refID,
			CreatedAt:    now,
		}
		s.nextEntryID++
		s.entries = append(s.entries, rec)
		s.entriesByTx[journalTxID] = append(s.entriesByTx[journalTxID], rec)
	}
	if refID != "" {
		s.txByRef[refID] = journalTxID
	}
	return nil
}

func (l JournalLeg) amountPositive() bool {
	return l.Amount.IsPositive()
}

// PostJournal validates and appends a double-entry journal transaction, rejecting any unbalanced legs.
func (s *MemoryStore) PostJournal(_ context.Context, journalTxID uuid.UUID, refID string, legs []JournalLeg) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitJournalLocked(journalTxID, refID, legs)
}

// GetLatestJournalTxByReference returns the latest journal_tx_id recorded for reference_id.
func (s *MemoryStore) GetLatestJournalTxByReference(_ context.Context, refID string) (uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.txByRef[refID]; ok {
		return id, nil
	}
	return uuid.Nil, nil
}

// VerifyAllJournalsBalanced verifies every committed journal_tx_id in MemoryStore sums to zero per asset.
func (s *MemoryStore) VerifyAllJournalsBalanced() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for txID, entries := range s.entriesByTx {
		if len(entries) < 2 {
			return fmt.Errorf("%w: tx %s has %d legs", ErrUnbalancedJournal, txID, len(entries))
		}
		nets := make(map[string]decimal.Decimal)
		for _, e := range entries {
			if e.Direction == DirectionCredit {
				nets[e.Asset] = nets[e.Asset].Add(e.Amount)
			} else {
				nets[e.Asset] = nets[e.Asset].Sub(e.Amount)
			}
		}
		for asset, net := range nets {
			if !net.IsZero() {
				return fmt.Errorf("%w: tx %s asset %s net=%s", ErrUnbalancedJournal, txID, asset, net)
			}
		}
	}
	return nil
}

func (s *MemoryStore) CreditDeposit(_ context.Context, userID uuid.UUID, asset string, amount decimal.Decimal, refID string) (uuid.UUID, error) {
	if _, err := AssetDecimals(asset); err != nil {
		return uuid.Nil, err
	}
	if !amount.IsPositive() {
		return uuid.Nil, fmt.Errorf("%w: deposit must be positive", ErrInvalidPrecision)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	vault := s.ensureAccountLocked(SystemVaultUserID, asset, AccountTypeEscrowVault)
	userAcc := s.ensureAccountLocked(userID, asset, AccountTypeUser)

	vault.Available = vault.Available.Sub(amount)
	vault.Version++
	vault.UpdatedAt = time.Now().UTC()

	userAcc.Available = userAcc.Available.Add(amount)
	userAcc.Version++
	userAcc.UpdatedAt = vault.UpdatedAt

	txID := uuid.New()
	legs := []pendingLeg{
		{vault.AccountID, asset, EntryTypeDeposit, DirectionDebit, amount, vault.Available},
		{userAcc.AccountID, asset, EntryTypeDeposit, DirectionCredit, amount, userAcc.Available},
	}
	if err := s.commitJournalLocked(txID, refID, legs); err != nil {
		return uuid.Nil, err
	}
	return txID, nil
}

func (s *MemoryStore) ReverseDeposit(_ context.Context, userID uuid.UUID, asset string, amount decimal.Decimal, refID string) (uuid.UUID, error) {
	if _, err := AssetDecimals(asset); err != nil {
		return uuid.Nil, err
	}
	if !amount.IsPositive() {
		return uuid.Nil, fmt.Errorf("%w: reversal must be positive", ErrInvalidPrecision)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	vault := s.ensureAccountLocked(SystemVaultUserID, asset, AccountTypeEscrowVault)
	userAcc := s.ensureAccountLocked(userID, asset, AccountTypeUser)
	if userAcc.Available.LessThan(amount) {
		return uuid.Nil, fmt.Errorf("%w: user available %s < %s", ErrReorgUnderflow, userAcc.Available, amount)
	}

	userAcc.Available = userAcc.Available.Sub(amount)
	userAcc.Version++
	userAcc.UpdatedAt = time.Now().UTC()

	vault.Available = vault.Available.Add(amount)
	vault.Version++
	vault.UpdatedAt = userAcc.UpdatedAt

	txID := uuid.New()
	legs := []pendingLeg{
		{userAcc.AccountID, asset, EntryTypeReorgReversal, DirectionDebit, amount, userAcc.Available},
		{vault.AccountID, asset, EntryTypeReorgReversal, DirectionCredit, amount, vault.Available},
	}
	if err := s.commitJournalLocked(txID, refID, legs); err != nil {
		return uuid.Nil, err
	}
	return txID, nil
}

func (s *MemoryStore) ReserveOrder(_ context.Context, req ReserveOrderReq) (*OrderRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	payloadHash := OrderPayloadHash(req.UserID, req.MarketID, req.Side, req.OrderType, req.TimeInForce, req.Price, req.Quantity)
	if existingID, ok := s.ordersByIdem[req.IdempotencyKey]; ok {
		existing := s.orders[existingID]
		if existing.PayloadHash != payloadHash {
			return nil, false, ErrIdempotencyConflict
		}
		cp := *existing
		return &cp, true, nil
	}

	baseAsset, quoteAsset, err := MarketAssets(req.MarketID)
	if err != nil {
		return nil, false, err
	}
	reserveAsset := quoteAsset
	if req.Side == SideSell {
		reserveAsset = baseAsset
	}

	if !req.ReserveAmount.IsPositive() {
		return nil, false, fmt.Errorf("%w: reserve amount must be positive", ErrInvalidPrecision)
	}
	acc := s.ensureAccountLocked(req.UserID, reserveAsset, AccountTypeUser)
	if acc.Available.LessThan(req.ReserveAmount) {
		return nil, false, fmt.Errorf("%w: available %s < required %s %s", ErrInsufficientAvailable, acc.Available, req.ReserveAmount, reserveAsset)
	}

	acc.Available = acc.Available.Sub(req.ReserveAmount)
	acc.Locked = acc.Locked.Add(req.ReserveAmount)
	acc.Version++
	acc.UpdatedAt = time.Now().UTC()

	orderID := req.OrderID
	if orderID == 0 {
		orderID = s.nextOrderID
		s.nextOrderID++
	}
	rec := &OrderRecord{
		OrderID:        orderID,
		IdempotencyKey: req.IdempotencyKey,
		PayloadHash:    payloadHash,
		UserID:         req.UserID,
		MarketID:       req.MarketID,
		Side:           req.Side,
		OrderType:      req.OrderType,
		TimeInForce:    req.TimeInForce,
		Price:          req.Price,
		Quantity:       req.Quantity,
		FilledQuantity: decimal.Zero,
		LockedAmount:   req.ReserveAmount,
		Status:         OrderStatusReserved,
		CreatedAt:      acc.UpdatedAt,
		UpdatedAt:      acc.UpdatedAt,
	}
	s.orders[orderID] = rec
	s.ordersByIdem[req.IdempotencyKey] = orderID

	txID := uuid.New()
	refID := fmt.Sprintf("order:%d", orderID)
	legs := []pendingLeg{
		{acc.AccountID, reserveAsset, EntryTypeOrderReserve, DirectionDebit, req.ReserveAmount, acc.Available},
		{acc.AccountID, reserveAsset, EntryTypeOrderReserve, DirectionCredit, req.ReserveAmount, acc.Locked},
	}
	if err := s.commitJournalLocked(txID, refID, legs); err != nil {
		return nil, false, err
	}

	cp := *rec
	return &cp, false, nil
}

func (s *MemoryStore) UpdateOrderStatus(_ context.Context, orderID int64, walSeq int64, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ord, ok := s.orders[orderID]
	if !ok {
		return ErrOrderNotFound
	}
	if walSeq > 0 {
		ord.WalSeq = walSeq
	}
	ord.Status = status
	ord.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *MemoryStore) ReleaseOrderReservation(_ context.Context, orderID int64, walSeq int64, newStatus string) (decimal.Decimal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ord, ok := s.orders[orderID]
	if !ok {
		return decimal.Zero, ErrOrderNotFound
	}
	if walSeq > 0 {
		ord.WalSeq = walSeq
	}
	released := ord.LockedAmount
	if released.IsPositive() {
		baseAsset, quoteAsset, err := MarketAssets(ord.MarketID)
		if err != nil {
			return decimal.Zero, err
		}
		reserveAsset := quoteAsset
		if ord.Side == SideSell {
			reserveAsset = baseAsset
		}
		acc := s.ensureAccountLocked(ord.UserID, reserveAsset, AccountTypeUser)
		if acc.Locked.LessThan(released) {
			return decimal.Zero, fmt.Errorf("%w: locked %s < release %s", ErrInsufficientLocked, acc.Locked, released)
		}
		acc.Locked = acc.Locked.Sub(released)
		acc.Available = acc.Available.Add(released)
		acc.Version++
		acc.UpdatedAt = time.Now().UTC()

		txID := uuid.New()
		legs := []pendingLeg{
			{acc.AccountID, reserveAsset, EntryTypeCancelRelease, DirectionDebit, released, acc.Locked},
			{acc.AccountID, reserveAsset, EntryTypeCancelRelease, DirectionCredit, released, acc.Available},
		}
		if err := s.commitJournalLocked(txID, fmt.Sprintf("cancel:%d", orderID), legs); err != nil {
			return decimal.Zero, err
		}
		ord.LockedAmount = decimal.Zero
	}
	ord.Status = newStatus
	ord.UpdatedAt = time.Now().UTC()
	return released, nil
}

func (s *MemoryStore) ApplySettlementBatch(_ context.Context, batch SettlementBatch) (int, error) {
	baseAsset, quoteAsset, err := MarketAssets(batch.MarketID)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	lastSeq := s.checkpoints[batch.MarketID]
	applied := 0

	for _, f := range batch.Fills {
		if f.WalSeq <= lastSeq {
			continue
		}
		if _, exists := s.tradesByWalSeq[f.WalSeq]; exists {
			continue
		}
		makerOrd, ok := s.orders[f.MakerOrderID]
		if !ok {
			return applied, fmt.Errorf("%w: maker %d", ErrOrderNotFound, f.MakerOrderID)
		}
		takerOrd, ok := s.orders[f.TakerOrderID]
		if !ok {
			return applied, fmt.Errorf("%w: taker %d", ErrOrderNotFound, f.TakerOrderID)
		}

		var buyerOrd, sellerOrd *OrderRecord
		var buyerFee, sellerFee decimal.Decimal
		if f.TakerSide == SideBuy {
			buyerOrd, sellerOrd = takerOrd, makerOrd
			buyerFee, sellerFee = f.TakerFee, f.MakerFee
		} else {
			buyerOrd, sellerOrd = makerOrd, takerOrd
			buyerFee, sellerFee = f.MakerFee, f.TakerFee
		}

		buyerQuoteAcc := s.ensureAccountLocked(buyerOrd.UserID, quoteAsset, AccountTypeUser)
		buyerBaseAcc := s.ensureAccountLocked(buyerOrd.UserID, baseAsset, AccountTypeUser)
		sellerQuoteAcc := s.ensureAccountLocked(sellerOrd.UserID, quoteAsset, AccountTypeUser)
		sellerBaseAcc := s.ensureAccountLocked(sellerOrd.UserID, baseAsset, AccountTypeUser)
		feeQuoteAcc := s.ensureAccountLocked(SystemFeeUserID, quoteAsset, AccountTypeFeeTreasury)

		buyerQuoteDebit := f.QuoteNotional.Add(buyerFee)
		sellerQuoteCredit := f.QuoteNotional.Sub(sellerFee)
		totalFee := buyerFee.Add(sellerFee)

		if buyerQuoteAcc.Locked.LessThan(buyerQuoteDebit) {
			return applied, fmt.Errorf("%w: buyer locked %s < %s", ErrInsufficientLocked, buyerQuoteAcc.Locked, buyerQuoteDebit)
		}
		if sellerBaseAcc.Locked.LessThan(f.FillQty) {
			return applied, fmt.Errorf("%w: seller locked %s < %s", ErrInsufficientLocked, sellerBaseAcc.Locked, f.FillQty)
		}

		now := time.Now().UTC()
		buyerQuoteAcc.Locked = buyerQuoteAcc.Locked.Sub(buyerQuoteDebit)
		buyerQuoteAcc.Version++
		buyerQuoteAcc.UpdatedAt = now

		sellerQuoteAcc.Available = sellerQuoteAcc.Available.Add(sellerQuoteCredit)
		sellerQuoteAcc.Version++
		sellerQuoteAcc.UpdatedAt = now

		if totalFee.IsPositive() {
			feeQuoteAcc.Available = feeQuoteAcc.Available.Add(totalFee)
			feeQuoteAcc.Version++
			feeQuoteAcc.UpdatedAt = now
		}

		sellerBaseAcc.Locked = sellerBaseAcc.Locked.Sub(f.FillQty)
		sellerBaseAcc.Version++
		sellerBaseAcc.UpdatedAt = now

		buyerBaseAcc.Available = buyerBaseAcc.Available.Add(f.FillQty)
		buyerBaseAcc.Version++
		buyerBaseAcc.UpdatedAt = now

		var legs []pendingLeg
		legs = append(legs,
			pendingLeg{buyerQuoteAcc.AccountID, quoteAsset, EntryTypeTradeSettle, DirectionDebit, buyerQuoteDebit, buyerQuoteAcc.Locked},
		)
		if sellerQuoteCredit.IsPositive() {
			legs = append(legs, pendingLeg{sellerQuoteAcc.AccountID, quoteAsset, EntryTypeTradeSettle, DirectionCredit, sellerQuoteCredit, sellerQuoteAcc.Available})
		}
		if totalFee.IsPositive() {
			legs = append(legs, pendingLeg{feeQuoteAcc.AccountID, quoteAsset, EntryTypeFee, DirectionCredit, totalFee, feeQuoteAcc.Available})
		}
		legs = append(legs,
			pendingLeg{sellerBaseAcc.AccountID, baseAsset, EntryTypeTradeSettle, DirectionDebit, f.FillQty, sellerBaseAcc.Locked},
			pendingLeg{buyerBaseAcc.AccountID, baseAsset, EntryTypeTradeSettle, DirectionCredit, f.FillQty, buyerBaseAcc.Available},
		)

		buyerOrd.FilledQuantity = buyerOrd.FilledQuantity.Add(f.FillQty)
		buyerOrd.LockedAmount = buyerOrd.LockedAmount.Sub(buyerQuoteDebit)
		sellerOrd.FilledQuantity = sellerOrd.FilledQuantity.Add(f.FillQty)
		sellerOrd.LockedAmount = sellerOrd.LockedAmount.Sub(f.FillQty)

		updateStatus := func(ord *OrderRecord, terminal bool) {
			ord.WalSeq = f.WalSeq
			ord.UpdatedAt = now
			if ord.FilledQuantity.GreaterThanOrEqual(ord.Quantity) {
				ord.Status = OrderStatusFilled
			} else if terminal {
				ord.Status = OrderStatusCanceled
			} else {
				ord.Status = OrderStatusPartiallyFilled
			}
		}
		updateStatus(makerOrd, f.MakerTerminal)
		updateStatus(takerOrd, f.TakerTerminal)

		// Price-improvement / terminal excess refund for buyer if order reached terminal state.
		if (buyerOrd.Status == OrderStatusFilled || buyerOrd.Status == OrderStatusCanceled) && buyerOrd.LockedAmount.IsPositive() {
			refund := buyerOrd.LockedAmount
			if buyerQuoteAcc.Locked.GreaterThanOrEqual(refund) {
				buyerQuoteAcc.Locked = buyerQuoteAcc.Locked.Sub(refund)
				buyerQuoteAcc.Available = buyerQuoteAcc.Available.Add(refund)
				buyerOrd.LockedAmount = decimal.Zero
				legs = append(legs,
					pendingLeg{buyerQuoteAcc.AccountID, quoteAsset, EntryTypeCancelRelease, DirectionDebit, refund, buyerQuoteAcc.Locked},
					pendingLeg{buyerQuoteAcc.AccountID, quoteAsset, EntryTypeCancelRelease, DirectionCredit, refund, buyerQuoteAcc.Available},
				)
			}
		}

		txID := uuid.New()
		if err := s.commitJournalLocked(txID, fmt.Sprintf("trade:%d", f.TradeID), legs); err != nil {
			return applied, err
		}

		s.trades = append(s.trades, TradeRecord{
			TradeID:       f.TradeID,
			WalSeq:        f.WalSeq,
			MarketID:      f.MarketID,
			MakerOrderID:  f.MakerOrderID,
			TakerOrderID:  f.TakerOrderID,
			MakerUserID:   f.MakerUserID,
			TakerUserID:   f.TakerUserID,
			Price:         f.ExecPrice,
			Quantity:      f.FillQty,
			QuoteNotional: f.QuoteNotional,
			MakerFee:      f.MakerFee,
			TakerFee:      f.TakerFee,
			ExecutedAt:    now,
		})
		s.tradesByWalSeq[f.WalSeq] = struct{}{}
		applied++
	}

	for _, c := range batch.Cancels {
		if c.WalSeq <= lastSeq {
			continue
		}
		ord, ok := s.orders[c.OrderID]
		if !ok {
			continue
		}
		if ord.LockedAmount.IsPositive() {
			reserveAsset := quoteAsset
			if ord.Side == SideSell {
				reserveAsset = baseAsset
			}
			acc := s.ensureAccountLocked(ord.UserID, reserveAsset, AccountTypeUser)
			rel := ord.LockedAmount
			if acc.Locked.GreaterThanOrEqual(rel) {
				acc.Locked = acc.Locked.Sub(rel)
				acc.Available = acc.Available.Add(rel)
				acc.Version++
				acc.UpdatedAt = time.Now().UTC()
				txID := uuid.New()
				legs := []pendingLeg{
					{acc.AccountID, reserveAsset, EntryTypeCancelRelease, DirectionDebit, rel, acc.Locked},
					{acc.AccountID, reserveAsset, EntryTypeCancelRelease, DirectionCredit, rel, acc.Available},
				}
				if err := s.commitJournalLocked(txID, fmt.Sprintf("cancel:%d", c.OrderID), legs); err != nil {
					return applied, err
				}
				ord.LockedAmount = decimal.Zero
			}
		}
		if c.NewStatus != "" {
			ord.Status = c.NewStatus
		} else {
			ord.Status = OrderStatusCanceled
		}
		ord.WalSeq = c.WalSeq
		ord.UpdatedAt = time.Now().UTC()
		applied++
	}

	if batch.MaxWalSeq > lastSeq {
		s.checkpoints[batch.MarketID] = batch.MaxWalSeq
	}
	return applied, nil
}

func (s *MemoryStore) GetLastSettledWalSeq(_ context.Context, marketID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkpoints[marketID], nil
}

func (s *MemoryStore) GetOrder(_ context.Context, orderID int64) (*OrderRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ord, ok := s.orders[orderID]
	if !ok {
		return nil, ErrOrderNotFound
	}
	cp := *ord
	return &cp, nil
}

func (s *MemoryStore) ListOrdersByUser(_ context.Context, userID uuid.UUID, limit int32) ([]OrderRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []OrderRecord
	for _, ord := range s.orders {
		if ord.UserID == userID {
			out = append(out, *ord)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].OrderID > out[j].OrderID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if limit > 0 && len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) ListRecentTrades(_ context.Context, marketID string, limit int32) ([]TradeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []TradeRecord
	for i := len(s.trades) - 1; i >= 0; i-- {
		if s.trades[i].MarketID == marketID {
			out = append(out, s.trades[i])
			if limit > 0 && len(out) >= int(limit) {
				break
			}
		}
	}
	return out, nil
}

func (s *MemoryStore) ListReservedOrders(_ context.Context) ([]OrderRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []OrderRecord
	for _, ord := range s.orders {
		if ord.Status == OrderStatusReserved {
			out = append(out, *ord)
		}
	}
	return out, nil
}

func (s *MemoryStore) QueueWithdrawal(_ context.Context, req WithdrawalRequest) (*WithdrawalRecord, bool, error) {
	if _, err := AssetDecimals(req.Asset); err != nil {
		return nil, false, err
	}
	if !req.HumanAmount.IsPositive() {
		return nil, false, fmt.Errorf("%w: withdrawal amount must be positive", ErrInvalidPrecision)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if existingID, ok := s.withdrawalsIdem[req.IdempotencyKey]; ok {
		rec := s.withdrawals[existingID]
		cp := *rec
		return &cp, true, nil
	}

	acc := s.ensureAccountLocked(req.UserID, req.Asset, AccountTypeUser)
	if acc.Available.LessThan(req.HumanAmount) {
		return nil, false, fmt.Errorf("%w: available %s < %s", ErrInsufficientAvailable, acc.Available, req.HumanAmount)
	}

	acc.Available = acc.Available.Sub(req.HumanAmount)
	acc.Locked = acc.Locked.Add(req.HumanAmount)
	acc.Version++
	acc.UpdatedAt = time.Now().UTC()

	rec := &WithdrawalRecord{
		WithdrawalID:     req.WithdrawalID,
		IdempotencyKey:   req.IdempotencyKey,
		UserID:           req.UserID,
		RecipientAddress: req.RecipientAddress,
		Asset:            req.Asset,
		HumanAmount:      req.HumanAmount,
		RawAmountWei:     new(big.Int).Set(req.RawAmountWei),
		SigningTier:      req.SigningTier,
		VoucherNonce:     req.VoucherNonce,
		Status:           "QUEUED",
		CreatedAt:        acc.UpdatedAt,
		UpdatedAt:        acc.UpdatedAt,
	}
	s.withdrawals[req.WithdrawalID] = rec
	s.withdrawalsIdem[req.IdempotencyKey] = req.WithdrawalID

	txID := uuid.New()
	legs := []pendingLeg{
		{acc.AccountID, req.Asset, EntryTypeWithdrawLock, DirectionDebit, req.HumanAmount, acc.Available},
		{acc.AccountID, req.Asset, EntryTypeWithdrawLock, DirectionCredit, req.HumanAmount, acc.Locked},
	}
	if err := s.commitJournalLocked(txID, fmt.Sprintf("withdraw:%s", req.WithdrawalID), legs); err != nil {
		return nil, false, err
	}
	cp := *rec
	return &cp, false, nil
}

func (s *MemoryStore) FinalizeWithdrawal(_ context.Context, withdrawalID uuid.UUID, userID uuid.UUID, asset string, amount decimal.Decimal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc := s.ensureAccountLocked(userID, asset, AccountTypeUser)
	if acc.Locked.LessThan(amount) {
		return fmt.Errorf("%w: locked %s < %s", ErrInsufficientLocked, acc.Locked, amount)
	}
	vault := s.ensureAccountLocked(SystemVaultUserID, asset, AccountTypeEscrowVault)

	now := time.Now().UTC()
	acc.Locked = acc.Locked.Sub(amount)
	acc.Version++
	acc.UpdatedAt = now

	vault.Available = vault.Available.Add(amount)
	vault.Version++
	vault.UpdatedAt = now

	if w, ok := s.withdrawals[withdrawalID]; ok {
		w.Status = "CONFIRMED"
		w.UpdatedAt = now
	}

	txID := uuid.New()
	legs := []pendingLeg{
		{acc.AccountID, asset, EntryTypeWithdrawFinal, DirectionDebit, amount, acc.Locked},
		{vault.AccountID, asset, EntryTypeWithdrawFinal, DirectionCredit, amount, vault.Available},
	}
	return s.commitJournalLocked(txID, fmt.Sprintf("withdraw_final:%s", withdrawalID), legs)
}

func (s *MemoryStore) RefundFailedWithdrawal(_ context.Context, withdrawalID uuid.UUID, userID uuid.UUID, asset string, amount decimal.Decimal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc := s.ensureAccountLocked(userID, asset, AccountTypeUser)
	if acc.Locked.LessThan(amount) {
		return fmt.Errorf("%w: locked %s < %s", ErrInsufficientLocked, acc.Locked, amount)
	}
	now := time.Now().UTC()
	acc.Locked = acc.Locked.Sub(amount)
	acc.Available = acc.Available.Add(amount)
	acc.Version++
	acc.UpdatedAt = now
	if w, ok := s.withdrawals[withdrawalID]; ok {
		w.Status = "FAILED_REFUNDED"
		w.UpdatedAt = now
	}
	txID := uuid.New()
	legs := []pendingLeg{
		{acc.AccountID, asset, EntryTypeCancelRelease, DirectionDebit, amount, acc.Locked},
		{acc.AccountID, asset, EntryTypeCancelRelease, DirectionCredit, amount, acc.Available},
	}
	return s.commitJournalLocked(txID, fmt.Sprintf("withdraw_refund:%s", withdrawalID), legs)
}

func (s *MemoryStore) GetLedgerEntriesByTx(_ context.Context, journalTxID uuid.UUID) ([]LedgerEntryRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.entriesByTx[journalTxID]
	out := make([]LedgerEntryRecord, len(src))
	copy(out, src)
	return out, nil
}

func (s *MemoryStore) SnapshotLiabilities(_ context.Context) (map[string]AssetTotal, []UserLiabilitySummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	totals := make(map[string]AssetTotal, len(AllAssets))
	for _, a := range AllAssets {
		totals[a] = AssetTotal{UserTotal: decimal.Zero, FeeTotal: decimal.Zero}
	}
	userMap := make(map[uuid.UUID]map[string]decimal.Decimal)
	for _, acc := range s.accounts {
		sum := acc.Available.Add(acc.Locked)
		switch acc.AccountType {
		case AccountTypeUser:
			t := totals[acc.Asset]
			t.UserTotal = t.UserTotal.Add(sum)
			totals[acc.Asset] = t
			if userMap[acc.UserID] == nil {
				userMap[acc.UserID] = make(map[string]decimal.Decimal)
			}
			userMap[acc.UserID][acc.Asset] = sum
		case AccountTypeFeeTreasury:
			t := totals[acc.Asset]
			t.FeeTotal = t.FeeTotal.Add(sum)
			totals[acc.Asset] = t
		}
	}
	users := make([]UserLiabilitySummary, 0, len(userMap))
	for uid, bals := range userMap {
		users = append(users, UserLiabilitySummary{UserID: uid, Balances: bals})
	}
	sort.Slice(users, func(i, j int) bool {
		return users[i].UserID.String() < users[j].UserID.String()
	})
	return totals, users, nil
}

// =============================================================================
// PostgreSQL 18 Store (pgxpool.Pool + sqlc db.Queries)
// =============================================================================

// PostgresStore implements Store against PostgreSQL 18 using deadlock-free sorted row locking.
type PostgresStore struct {
	pool        *pgxpool.Pool
	nextOrderID int64
	orderMu     sync.Mutex
}

// NewPostgresStore wraps a pgxpool.Pool.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{
		pool:        pool,
		nextOrderID: time.Now().UnixMilli() * 1000,
	}
}

func (p *PostgresStore) NextOrderID() int64 {
	p.orderMu.Lock()
	defer p.orderMu.Unlock()
	p.nextOrderID++
	return p.nextOrderID
}

func toPgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func fromPgUUID(id pgtype.UUID) uuid.UUID {
	return uuid.UUID(id.Bytes)
}

func (p *PostgresStore) EnsureUserAccounts(ctx context.Context, userID uuid.UUID) error {
	q := db.New(p.pool)
	for _, asset := range AllAssets {
		if _, err := q.EnsureAccount(ctx, db.EnsureAccountParams{
			AccountID:   toPgUUID(DeterministicAccountID(userID, asset, AccountTypeUser)),
			UserID:      toPgUUID(userID),
			Asset:       asset,
			AccountType: AccountTypeUser,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (p *PostgresStore) ListUserBalances(ctx context.Context, userID uuid.UUID) ([]AccountBalance, error) {
	if err := p.EnsureUserAccounts(ctx, userID); err != nil {
		return nil, err
	}
	q := db.New(p.pool)
	rows, err := q.ListUserBalances(ctx, toPgUUID(userID))
	if err != nil {
		return nil, err
	}
	out := make([]AccountBalance, 0, len(rows))
	for _, r := range rows {
		out = append(out, AccountBalance{
			AccountID:   fromPgUUID(r.AccountID),
			UserID:      fromPgUUID(r.UserID),
			Asset:       r.Asset,
			AccountType: r.AccountType,
			Available:   r.Available,
			Locked:      r.Locked,
			Version:     r.Version,
			UpdatedAt:   r.UpdatedAt,
		})
	}
	return out, nil
}

func (p *PostgresStore) ensureSystemAccountsTx(ctx context.Context, q *db.Queries, asset string) (db.Account, db.Account, error) {
	vault, err := q.EnsureAccount(ctx, db.EnsureAccountParams{
		AccountID:   toPgUUID(DeterministicAccountID(SystemVaultUserID, asset, AccountTypeEscrowVault)),
		UserID:      toPgUUID(SystemVaultUserID),
		Asset:       asset,
		AccountType: AccountTypeEscrowVault,
	})
	if err != nil {
		return db.Account{}, db.Account{}, err
	}
	fee, err := q.EnsureAccount(ctx, db.EnsureAccountParams{
		AccountID:   toPgUUID(DeterministicAccountID(SystemFeeUserID, asset, AccountTypeFeeTreasury)),
		UserID:      toPgUUID(SystemFeeUserID),
		Asset:       asset,
		AccountType: AccountTypeFeeTreasury,
	})
	if err != nil {
		return db.Account{}, db.Account{}, err
	}
	return vault, fee, nil
}

func lockAccountsSortedTx(ctx context.Context, q *db.Queries, ids []uuid.UUID) (map[uuid.UUID]db.Account, error) {
	dedup := make(map[uuid.UUID]struct{}, len(ids))
	uniq := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := dedup[id]; !ok {
			dedup[id] = struct{}{}
			uniq = append(uniq, toPgUUID(id))
		}
	}
	rows, err := q.GetAccountsByIDsForUpdate(ctx, uniq)
	if err != nil {
		return nil, err
	}
	res := make(map[uuid.UUID]db.Account, len(rows))
	for _, r := range rows {
		res[fromPgUUID(r.AccountID)] = r
	}
	return res, nil
}

func (p *PostgresStore) CreditDeposit(ctx context.Context, userID uuid.UUID, asset string, amount decimal.Decimal, refID string) (uuid.UUID, error) {
	if _, err := AssetDecimals(asset); err != nil {
		return uuid.Nil, err
	}
	if !amount.IsPositive() {
		return uuid.Nil, fmt.Errorf("%w: deposit must be positive", ErrInvalidPrecision)
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	vault, _, err := p.ensureSystemAccountsTx(ctx, q, asset)
	if err != nil {
		return uuid.Nil, err
	}
	userAcc, err := q.EnsureAccount(ctx, db.EnsureAccountParams{
		AccountID:   toPgUUID(DeterministicAccountID(userID, asset, AccountTypeUser)),
		UserID:      toPgUUID(userID),
		Asset:       asset,
		AccountType: AccountTypeUser,
	})
	if err != nil {
		return uuid.Nil, err
	}
	lockedMap, err := lockAccountsSortedTx(ctx, q, []uuid.UUID{fromPgUUID(vault.AccountID), fromPgUUID(userAcc.AccountID)})
	if err != nil {
		return uuid.Nil, err
	}
	_ = lockedMap

	newVault, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
		AccountID:      vault.AccountID,
		AvailableDelta: amount.Neg(),
		LockedDelta:    decimal.Zero,
	})
	if err != nil {
		return uuid.Nil, err
	}
	newUser, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
		AccountID:      userAcc.AccountID,
		AvailableDelta: amount,
		LockedDelta:    decimal.Zero,
	})
	if err != nil {
		return uuid.Nil, err
	}

	journalTxID := uuid.New()
	if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
		JournalTxID:  toPgUUID(journalTxID),
		AccountID:    vault.AccountID,
		Asset:        asset,
		EntryType:    EntryTypeDeposit,
		Direction:    DirectionDebit,
		Amount:       amount,
		BalanceAfter: newVault.Available,
		ReferenceID:  refID,
	}); err != nil {
		return uuid.Nil, err
	}
	if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
		JournalTxID:  toPgUUID(journalTxID),
		AccountID:    userAcc.AccountID,
		Asset:        asset,
		EntryType:    EntryTypeDeposit,
		Direction:    DirectionCredit,
		Amount:       amount,
		BalanceAfter: newUser.Available,
		ReferenceID:  refID,
	}); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return journalTxID, nil
}

func (p *PostgresStore) ReverseDeposit(ctx context.Context, userID uuid.UUID, asset string, amount decimal.Decimal, refID string) (uuid.UUID, error) {
	if _, err := AssetDecimals(asset); err != nil {
		return uuid.Nil, err
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	vault, _, err := p.ensureSystemAccountsTx(ctx, q, asset)
	if err != nil {
		return uuid.Nil, err
	}
	userAccID := DeterministicAccountID(userID, asset, AccountTypeUser)
	lockedMap, err := lockAccountsSortedTx(ctx, q, []uuid.UUID{fromPgUUID(vault.AccountID), userAccID})
	if err != nil {
		return uuid.Nil, err
	}
	userAcc, ok := lockedMap[userAccID]
	if !ok || userAcc.Available.LessThan(amount) {
		return uuid.Nil, ErrReorgUnderflow
	}

	newUser, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
		AccountID:      toPgUUID(userAccID),
		AvailableDelta: amount.Neg(),
		LockedDelta:    decimal.Zero,
	})
	if err != nil {
		return uuid.Nil, err
	}
	newVault, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
		AccountID:      vault.AccountID,
		AvailableDelta: amount,
		LockedDelta:    decimal.Zero,
	})
	if err != nil {
		return uuid.Nil, err
	}

	journalTxID := uuid.New()
	if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
		JournalTxID:  toPgUUID(journalTxID),
		AccountID:    toPgUUID(userAccID),
		Asset:        asset,
		EntryType:    EntryTypeReorgReversal,
		Direction:    DirectionDebit,
		Amount:       amount,
		BalanceAfter: newUser.Available,
		ReferenceID:  refID,
	}); err != nil {
		return uuid.Nil, err
	}
	if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
		JournalTxID:  toPgUUID(journalTxID),
		AccountID:    vault.AccountID,
		Asset:        asset,
		EntryType:    EntryTypeReorgReversal,
		Direction:    DirectionCredit,
		Amount:       amount,
		BalanceAfter: newVault.Available,
		ReferenceID:  refID,
	}); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return journalTxID, nil
}

func (p *PostgresStore) ReserveOrder(ctx context.Context, req ReserveOrderReq) (*OrderRecord, bool, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	if existing, err := q.GetOrderByIdempotencyKey(ctx, toPgUUID(req.IdempotencyKey)); err == nil {
		if existing.MarketID != req.MarketID || existing.Side != req.Side || !existing.Quantity.Equal(req.Quantity) || !existing.Price.Equal(req.Price) {
			return nil, false, ErrIdempotencyConflict
		}
		return dbOrderToRecord(existing), true, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	baseAsset, quoteAsset, err := MarketAssets(req.MarketID)
	if err != nil {
		return nil, false, err
	}
	reserveAsset := quoteAsset
	if req.Side == SideSell {
		reserveAsset = baseAsset
	}

	accID := DeterministicAccountID(req.UserID, reserveAsset, AccountTypeUser)
	if _, err := q.EnsureAccount(ctx, db.EnsureAccountParams{
		AccountID:   toPgUUID(accID),
		UserID:      toPgUUID(req.UserID),
		Asset:       reserveAsset,
		AccountType: AccountTypeUser,
	}); err != nil {
		return nil, false, err
	}
	acc, err := q.GetAccountForUpdate(ctx, db.GetAccountForUpdateParams{
		UserID:      toPgUUID(req.UserID),
		Asset:       reserveAsset,
		AccountType: AccountTypeUser,
	})
	if err != nil {
		return nil, false, err
	}
	if acc.Available.LessThan(req.ReserveAmount) {
		return nil, false, ErrInsufficientAvailable
	}

	updatedAcc, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
		AccountID:      acc.AccountID,
		AvailableDelta: req.ReserveAmount.Neg(),
		LockedDelta:    req.ReserveAmount,
	})
	if err != nil {
		return nil, false, err
	}

	orderID := req.OrderID
	if orderID == 0 {
		orderID = p.NextOrderID()
	}
	inserted, err := q.InsertOrder(ctx, db.InsertOrderParams{
		OrderID:        orderID,
		IdempotencyKey: toPgUUID(req.IdempotencyKey),
		UserID:         toPgUUID(req.UserID),
		MarketID:       req.MarketID,
		Side:           req.Side,
		OrderType:      req.OrderType,
		TimeInForce:    req.TimeInForce,
		Price:          req.Price,
		Quantity:       req.Quantity,
		FilledQuantity: decimal.Zero,
		LockedAmount:   req.ReserveAmount,
		Status:         OrderStatusReserved,
	})
	if err != nil {
		return nil, false, err
	}

	journalTxID := uuid.New()
	refID := fmt.Sprintf("order:%d", orderID)
	if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
		JournalTxID:  toPgUUID(journalTxID),
		AccountID:    acc.AccountID,
		Asset:        reserveAsset,
		EntryType:    EntryTypeOrderReserve,
		Direction:    DirectionDebit,
		Amount:       req.ReserveAmount,
		BalanceAfter: updatedAcc.Available,
		ReferenceID:  refID,
	}); err != nil {
		return nil, false, err
	}
	if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
		JournalTxID:  toPgUUID(journalTxID),
		AccountID:    acc.AccountID,
		Asset:        reserveAsset,
		EntryType:    EntryTypeOrderReserve,
		Direction:    DirectionCredit,
		Amount:       req.ReserveAmount,
		BalanceAfter: updatedAcc.Locked,
		ReferenceID:  refID,
	}); err != nil {
		return nil, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return dbOrderToRecord(inserted), false, nil
}

func (p *PostgresStore) UpdateOrderStatus(ctx context.Context, orderID int64, walSeq int64, status string) error {
	q := db.New(p.pool)
	ord, err := q.GetOrderByID(ctx, orderID)
	if err != nil {
		return err
	}
	var seq pgtype.Int8
	if walSeq > 0 {
		seq = pgtype.Int8{Int64: walSeq, Valid: true}
	}
	_, err = q.UpdateOrderExec(ctx, db.UpdateOrderExecParams{
		OrderID:        orderID,
		FilledQuantity: ord.FilledQuantity,
		LockedAmount:   ord.LockedAmount,
		WalSeq:         seq,
		Status:         status,
	})
	return err
}

func (p *PostgresStore) ReleaseOrderReservation(ctx context.Context, orderID int64, walSeq int64, newStatus string) (decimal.Decimal, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return decimal.Zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	ord, err := q.GetOrderByID(ctx, orderID)
	if err != nil {
		return decimal.Zero, err
	}
	released := ord.LockedAmount
	if released.IsPositive() {
		baseAsset, quoteAsset, err := MarketAssets(ord.MarketID)
		if err != nil {
			return decimal.Zero, err
		}
		reserveAsset := quoteAsset
		if ord.Side == SideSell {
			reserveAsset = baseAsset
		}
		acc, err := q.GetAccountForUpdate(ctx, db.GetAccountForUpdateParams{
			UserID:      ord.UserID,
			Asset:       reserveAsset,
			AccountType: AccountTypeUser,
		})
		if err != nil {
			return decimal.Zero, err
		}
		updated, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
			AccountID:      acc.AccountID,
			AvailableDelta: released,
			LockedDelta:    released.Neg(),
		})
		if err != nil {
			return decimal.Zero, err
		}
		journalTxID := uuid.New()
		ref := fmt.Sprintf("cancel:%d", orderID)
		if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
			JournalTxID:  toPgUUID(journalTxID),
			AccountID:    acc.AccountID,
			Asset:        reserveAsset,
			EntryType:    EntryTypeCancelRelease,
			Direction:    DirectionDebit,
			Amount:       released,
			BalanceAfter: updated.Locked,
			ReferenceID:  ref,
		}); err != nil {
			return decimal.Zero, err
		}
		if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
			JournalTxID:  toPgUUID(journalTxID),
			AccountID:    acc.AccountID,
			Asset:        reserveAsset,
			EntryType:    EntryTypeCancelRelease,
			Direction:    DirectionCredit,
			Amount:       released,
			BalanceAfter: updated.Available,
			ReferenceID:  ref,
		}); err != nil {
			return decimal.Zero, err
		}
	}

	var seq pgtype.Int8
	if walSeq > 0 {
		seq = pgtype.Int8{Int64: walSeq, Valid: true}
	}
	if _, err := q.UpdateOrderExec(ctx, db.UpdateOrderExecParams{
		OrderID:        orderID,
		FilledQuantity: ord.FilledQuantity,
		LockedAmount:   decimal.Zero,
		WalSeq:         seq,
		Status:         newStatus,
	}); err != nil {
		return decimal.Zero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return decimal.Zero, err
	}
	return released, nil
}

func (p *PostgresStore) ApplySettlementBatch(ctx context.Context, batch SettlementBatch) (int, error) {
	baseAsset, quoteAsset, err := MarketAssets(batch.MarketID)
	if err != nil {
		return 0, err
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	lastSeq := int64(0)
	cp, err := q.GetEngineCheckpointForUpdate(ctx, batch.MarketID)
	if err == nil {
		lastSeq = cp.LastSettledWalSeq
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}

	// Ensure system fee account & collect all impacted account IDs sorted ascending.
	_, feeAcc, err := p.ensureSystemAccountsTx(ctx, q, quoteAsset)
	if err != nil {
		return 0, err
	}
	accIDs := []uuid.UUID{fromPgUUID(feeAcc.AccountID)}
	for _, f := range batch.Fills {
		if f.WalSeq <= lastSeq {
			continue
		}
		for _, uid := range []uuid.UUID{f.MakerUserID, f.TakerUserID} {
			for _, a := range []string{baseAsset, quoteAsset} {
				acc, err := q.EnsureAccount(ctx, db.EnsureAccountParams{
					AccountID:   toPgUUID(DeterministicAccountID(uid, a, AccountTypeUser)),
					UserID:      toPgUUID(uid),
					Asset:       a,
					AccountType: AccountTypeUser,
				})
				if err != nil {
					return 0, err
				}
				accIDs = append(accIDs, fromPgUUID(acc.AccountID))
			}
		}
	}
	for _, c := range batch.Cancels {
		if c.WalSeq <= lastSeq {
			continue
		}
		for _, a := range []string{baseAsset, quoteAsset} {
			accIDs = append(accIDs, DeterministicAccountID(c.UserID, a, AccountTypeUser))
		}
	}
	if _, err := lockAccountsSortedTx(ctx, q, accIDs); err != nil {
		return 0, err
	}

	applied := 0
	for _, f := range batch.Fills {
		if f.WalSeq <= lastSeq {
			continue
		}
		makerOrd, err := q.GetOrderByID(ctx, f.MakerOrderID)
		if err != nil {
			return applied, err
		}
		takerOrd, err := q.GetOrderByID(ctx, f.TakerOrderID)
		if err != nil {
			return applied, err
		}
		buyerOrd, sellerOrd := makerOrd, takerOrd
		buyerFee, sellerFee := f.MakerFee, f.TakerFee
		buyerTerminal, sellerTerminal := f.MakerTerminal, f.TakerTerminal
		if f.TakerSide == SideBuy {
			buyerOrd, sellerOrd = takerOrd, makerOrd
			buyerFee, sellerFee = f.TakerFee, f.MakerFee
			buyerTerminal, sellerTerminal = f.TakerTerminal, f.MakerTerminal
		}

		buyerQuoteID := toPgUUID(DeterministicAccountID(fromPgUUID(buyerOrd.UserID), quoteAsset, AccountTypeUser))
		buyerBaseID := toPgUUID(DeterministicAccountID(fromPgUUID(buyerOrd.UserID), baseAsset, AccountTypeUser))
		sellerQuoteID := toPgUUID(DeterministicAccountID(fromPgUUID(sellerOrd.UserID), quoteAsset, AccountTypeUser))
		sellerBaseID := toPgUUID(DeterministicAccountID(fromPgUUID(sellerOrd.UserID), baseAsset, AccountTypeUser))

		buyerQuoteDebit := f.QuoteNotional.Add(buyerFee)
		sellerQuoteCredit := f.QuoteNotional.Sub(sellerFee)
		totalFee := buyerFee.Add(sellerFee)

		newBuyerQuote, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
			AccountID:      buyerQuoteID,
			AvailableDelta: decimal.Zero,
			LockedDelta:    buyerQuoteDebit.Neg(),
		})
		if err != nil {
			return applied, err
		}
		newSellerQuote, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
			AccountID:      sellerQuoteID,
			AvailableDelta: sellerQuoteCredit,
			LockedDelta:    decimal.Zero,
		})
		if err != nil {
			return applied, err
		}
		var newFee db.Account
		if totalFee.IsPositive() {
			newFee, err = q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
				AccountID:      feeAcc.AccountID,
				AvailableDelta: totalFee,
				LockedDelta:    decimal.Zero,
			})
			if err != nil {
				return applied, err
			}
		}
		newSellerBase, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
			AccountID:      sellerBaseID,
			AvailableDelta: decimal.Zero,
			LockedDelta:    f.FillQty.Neg(),
		})
		if err != nil {
			return applied, err
		}
		newBuyerBase, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
			AccountID:      buyerBaseID,
			AvailableDelta: f.FillQty,
			LockedDelta:    decimal.Zero,
		})
		if err != nil {
			return applied, err
		}

		journalTxID := uuid.New()
		ref := fmt.Sprintf("trade:%d", f.TradeID)
		if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
			JournalTxID: toPgUUID(journalTxID), AccountID: buyerQuoteID, Asset: quoteAsset,
			EntryType: EntryTypeTradeSettle, Direction: DirectionDebit, Amount: buyerQuoteDebit,
			BalanceAfter: newBuyerQuote.Locked, ReferenceID: ref,
		}); err != nil {
			return applied, err
		}
		if sellerQuoteCredit.IsPositive() {
			if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
				JournalTxID: toPgUUID(journalTxID), AccountID: sellerQuoteID, Asset: quoteAsset,
				EntryType: EntryTypeTradeSettle, Direction: DirectionCredit, Amount: sellerQuoteCredit,
				BalanceAfter: newSellerQuote.Available, ReferenceID: ref,
			}); err != nil {
				return applied, err
			}
		}
		if totalFee.IsPositive() {
			if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
				JournalTxID: toPgUUID(journalTxID), AccountID: feeAcc.AccountID, Asset: quoteAsset,
				EntryType: EntryTypeFee, Direction: DirectionCredit, Amount: totalFee,
				BalanceAfter: newFee.Available, ReferenceID: ref,
			}); err != nil {
				return applied, err
			}
		}
		if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
			JournalTxID: toPgUUID(journalTxID), AccountID: sellerBaseID, Asset: baseAsset,
			EntryType: EntryTypeTradeSettle, Direction: DirectionDebit, Amount: f.FillQty,
			BalanceAfter: newSellerBase.Locked, ReferenceID: ref,
		}); err != nil {
			return applied, err
		}
		if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
			JournalTxID: toPgUUID(journalTxID), AccountID: buyerBaseID, Asset: baseAsset,
			EntryType: EntryTypeTradeSettle, Direction: DirectionCredit, Amount: f.FillQty,
			BalanceAfter: newBuyerBase.Available, ReferenceID: ref,
		}); err != nil {
			return applied, err
		}

		buyerFilled := buyerOrd.FilledQuantity.Add(f.FillQty)
		buyerLocked := buyerOrd.LockedAmount.Sub(buyerQuoteDebit)
		buyerStatus := OrderStatusPartiallyFilled
		if buyerFilled.GreaterThanOrEqual(buyerOrd.Quantity) {
			buyerStatus = OrderStatusFilled
		} else if buyerTerminal {
			buyerStatus = OrderStatusCanceled
		}
		if (buyerStatus == OrderStatusFilled || buyerStatus == OrderStatusCanceled) && buyerLocked.IsPositive() {
			refund := buyerLocked
			buyerLocked = decimal.Zero
			refundedAcc, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
				AccountID: buyerQuoteID, AvailableDelta: refund, LockedDelta: refund.Neg(),
			})
			if err != nil {
				return applied, err
			}
			if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
				JournalTxID: toPgUUID(journalTxID), AccountID: buyerQuoteID, Asset: quoteAsset,
				EntryType: EntryTypeCancelRelease, Direction: DirectionDebit, Amount: refund,
				BalanceAfter: refundedAcc.Locked, ReferenceID: ref,
			}); err != nil {
				return applied, err
			}
			if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
				JournalTxID: toPgUUID(journalTxID), AccountID: buyerQuoteID, Asset: quoteAsset,
				EntryType: EntryTypeCancelRelease, Direction: DirectionCredit, Amount: refund,
				BalanceAfter: refundedAcc.Available, ReferenceID: ref,
			}); err != nil {
				return applied, err
			}
		}

		sellerFilled := sellerOrd.FilledQuantity.Add(f.FillQty)
		sellerLocked := sellerOrd.LockedAmount.Sub(f.FillQty)
		sellerStatus := OrderStatusPartiallyFilled
		if sellerFilled.GreaterThanOrEqual(sellerOrd.Quantity) {
			sellerStatus = OrderStatusFilled
		} else if sellerTerminal {
			sellerStatus = OrderStatusCanceled
		}

		seq := pgtype.Int8{Int64: f.WalSeq, Valid: true}
		if _, err := q.UpdateOrderExec(ctx, db.UpdateOrderExecParams{
			OrderID: buyerOrd.OrderID, FilledQuantity: buyerFilled, LockedAmount: buyerLocked, WalSeq: seq, Status: buyerStatus,
		}); err != nil {
			return applied, err
		}
		if _, err := q.UpdateOrderExec(ctx, db.UpdateOrderExecParams{
			OrderID: sellerOrd.OrderID, FilledQuantity: sellerFilled, LockedAmount: sellerLocked, WalSeq: seq, Status: sellerStatus,
		}); err != nil {
			return applied, err
		}
		if _, err := q.InsertTrade(ctx, db.InsertTradeParams{
			TradeID: f.TradeID, WalSeq: f.WalSeq, MarketID: f.MarketID,
			MakerOrderID: f.MakerOrderID, TakerOrderID: f.TakerOrderID,
			MakerUserID: toPgUUID(f.MakerUserID), TakerUserID: toPgUUID(f.TakerUserID),
			Price: f.ExecPrice, Quantity: f.FillQty, QuoteNotional: f.QuoteNotional,
			MakerFee: f.MakerFee, TakerFee: f.TakerFee,
		}); err != nil {
			return applied, err
		}
		applied++
	}

	for _, c := range batch.Cancels {
		if c.WalSeq <= lastSeq {
			continue
		}
		ord, err := q.GetOrderByID(ctx, c.OrderID)
		if err != nil {
			continue
		}
		if ord.LockedAmount.IsPositive() {
			reserveAsset := quoteAsset
			if ord.Side == SideSell {
				reserveAsset = baseAsset
			}
			accID := toPgUUID(DeterministicAccountID(c.UserID, reserveAsset, AccountTypeUser))
			updated, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
				AccountID: accID, AvailableDelta: ord.LockedAmount, LockedDelta: ord.LockedAmount.Neg(),
			})
			if err != nil {
				return applied, err
			}
			journalTxID := uuid.New()
			ref := fmt.Sprintf("cancel:%d", c.OrderID)
			if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
				JournalTxID: toPgUUID(journalTxID), AccountID: accID, Asset: reserveAsset,
				EntryType: EntryTypeCancelRelease, Direction: DirectionDebit, Amount: ord.LockedAmount,
				BalanceAfter: updated.Locked, ReferenceID: ref,
			}); err != nil {
				return applied, err
			}
			if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
				JournalTxID: toPgUUID(journalTxID), AccountID: accID, Asset: reserveAsset,
				EntryType: EntryTypeCancelRelease, Direction: DirectionCredit, Amount: ord.LockedAmount,
				BalanceAfter: updated.Available, ReferenceID: ref,
			}); err != nil {
				return applied, err
			}
		}
		newStatus := c.NewStatus
		if newStatus == "" {
			newStatus = OrderStatusCanceled
		}
		if _, err := q.UpdateOrderExec(ctx, db.UpdateOrderExecParams{
			OrderID: c.OrderID, FilledQuantity: ord.FilledQuantity, LockedAmount: decimal.Zero,
			WalSeq: pgtype.Int8{Int64: c.WalSeq, Valid: true}, Status: newStatus,
		}); err != nil {
			return applied, err
		}
		applied++
	}

	if batch.MaxWalSeq > lastSeq {
		if _, err := q.UpsertEngineCheckpoint(ctx, db.UpsertEngineCheckpointParams{
			MarketID: batch.MarketID, LastSettledWalSeq: batch.MaxWalSeq,
		}); err != nil {
			return applied, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return applied, err
	}
	return applied, nil
}

func (p *PostgresStore) GetLastSettledWalSeq(ctx context.Context, marketID string) (int64, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)
	cp, err := q.GetEngineCheckpointForUpdate(ctx, marketID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return cp.LastSettledWalSeq, nil
}

func (p *PostgresStore) GetOrder(ctx context.Context, orderID int64) (*OrderRecord, error) {
	q := db.New(p.pool)
	o, err := q.GetOrderByID(ctx, orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	return dbOrderToRecord(o), nil
}

func (p *PostgresStore) ListOrdersByUser(ctx context.Context, userID uuid.UUID, limit int32) ([]OrderRecord, error) {
	q := db.New(p.pool)
	rows, err := q.ListOrdersByUser(ctx, db.ListOrdersByUserParams{UserID: toPgUUID(userID), Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]OrderRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, *dbOrderToRecord(r))
	}
	return out, nil
}

func (p *PostgresStore) ListRecentTrades(ctx context.Context, marketID string, limit int32) ([]TradeRecord, error) {
	q := db.New(p.pool)
	rows, err := q.ListRecentTradesByMarket(ctx, db.ListRecentTradesByMarketParams{MarketID: marketID, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]TradeRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, TradeRecord{
			TradeID:       r.TradeID,
			WalSeq:        r.WalSeq,
			MarketID:      r.MarketID,
			MakerOrderID:  r.MakerOrderID,
			TakerOrderID:  r.TakerOrderID,
			MakerUserID:   fromPgUUID(r.MakerUserID),
			TakerUserID:   fromPgUUID(r.TakerUserID),
			Price:         r.Price,
			Quantity:      r.Quantity,
			QuoteNotional: r.QuoteNotional,
			MakerFee:      r.MakerFee,
			TakerFee:      r.TakerFee,
			ExecutedAt:    r.ExecutedAt,
		})
	}
	return out, nil
}

func (p *PostgresStore) ListReservedOrders(ctx context.Context) ([]OrderRecord, error) {
	q := db.New(p.pool)
	rows, err := q.ListReservedOrders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]OrderRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, *dbOrderToRecord(r))
	}
	return out, nil
}

func (p *PostgresStore) QueueWithdrawal(ctx context.Context, req WithdrawalRequest) (*WithdrawalRecord, bool, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	if existing, err := q.GetWithdrawalByIdempotencyKey(ctx, toPgUUID(req.IdempotencyKey)); err == nil {
		return dbWithdrawalToRecord(existing), true, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	accID := DeterministicAccountID(req.UserID, req.Asset, AccountTypeUser)
	if _, err := q.EnsureAccount(ctx, db.EnsureAccountParams{
		AccountID: toPgUUID(accID), UserID: toPgUUID(req.UserID), Asset: req.Asset, AccountType: AccountTypeUser,
	}); err != nil {
		return nil, false, err
	}
	acc, err := q.GetAccountForUpdate(ctx, db.GetAccountForUpdateParams{
		UserID: toPgUUID(req.UserID), Asset: req.Asset, AccountType: AccountTypeUser,
	})
	if err != nil {
		return nil, false, err
	}
	if acc.Available.LessThan(req.HumanAmount) {
		return nil, false, ErrInsufficientAvailable
	}

	updated, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
		AccountID: acc.AccountID, AvailableDelta: req.HumanAmount.Neg(), LockedDelta: req.HumanAmount,
	})
	if err != nil {
		return nil, false, err
	}

	rawDec := decimal.NewFromBigInt(req.RawAmountWei, 0)
	inserted, err := q.InsertWithdrawalOutbox(ctx, db.InsertWithdrawalOutboxParams{
		WithdrawalID:     toPgUUID(req.WithdrawalID),
		IdempotencyKey:   toPgUUID(req.IdempotencyKey),
		UserID:           toPgUUID(req.UserID),
		RecipientAddress: req.RecipientAddress,
		Asset:            req.Asset,
		HumanAmount:      req.HumanAmount,
		RawAmountWei:     rawDec,
		SigningTier:      req.SigningTier,
		VoucherNonce:     req.VoucherNonce,
	})
	if err != nil {
		return nil, false, err
	}

	journalTxID := uuid.New()
	ref := fmt.Sprintf("withdraw:%s", req.WithdrawalID)
	if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
		JournalTxID: toPgUUID(journalTxID), AccountID: acc.AccountID, Asset: req.Asset,
		EntryType: EntryTypeWithdrawLock, Direction: DirectionDebit, Amount: req.HumanAmount,
		BalanceAfter: updated.Available, ReferenceID: ref,
	}); err != nil {
		return nil, false, err
	}
	if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
		JournalTxID: toPgUUID(journalTxID), AccountID: acc.AccountID, Asset: req.Asset,
		EntryType: EntryTypeWithdrawLock, Direction: DirectionCredit, Amount: req.HumanAmount,
		BalanceAfter: updated.Locked, ReferenceID: ref,
	}); err != nil {
		return nil, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return dbWithdrawalToRecord(inserted), false, nil
}

func (p *PostgresStore) FinalizeWithdrawal(ctx context.Context, withdrawalID uuid.UUID, userID uuid.UUID, asset string, amount decimal.Decimal) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	vault, _, err := p.ensureSystemAccountsTx(ctx, q, asset)
	if err != nil {
		return err
	}
	userAccID := DeterministicAccountID(userID, asset, AccountTypeUser)
	if _, err := lockAccountsSortedTx(ctx, q, []uuid.UUID{fromPgUUID(vault.AccountID), userAccID}); err != nil {
		return err
	}

	newUser, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
		AccountID: toPgUUID(userAccID), AvailableDelta: decimal.Zero, LockedDelta: amount.Neg(),
	})
	if err != nil {
		return err
	}
	newVault, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
		AccountID: vault.AccountID, AvailableDelta: amount, LockedDelta: decimal.Zero,
	})
	if err != nil {
		return err
	}
	if _, err := q.MarkWithdrawalConfirmed(ctx, toPgUUID(withdrawalID)); err != nil {
		return err
	}
	journalTxID := uuid.New()
	ref := fmt.Sprintf("withdraw_final:%s", withdrawalID)
	if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
		JournalTxID: toPgUUID(journalTxID), AccountID: toPgUUID(userAccID), Asset: asset,
		EntryType: EntryTypeWithdrawFinal, Direction: DirectionDebit, Amount: amount,
		BalanceAfter: newUser.Locked, ReferenceID: ref,
	}); err != nil {
		return err
	}
	if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
		JournalTxID: toPgUUID(journalTxID), AccountID: vault.AccountID, Asset: asset,
		EntryType: EntryTypeWithdrawFinal, Direction: DirectionCredit, Amount: amount,
		BalanceAfter: newVault.Available, ReferenceID: ref,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *PostgresStore) RefundFailedWithdrawal(ctx context.Context, withdrawalID uuid.UUID, userID uuid.UUID, asset string, amount decimal.Decimal) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	accID := DeterministicAccountID(userID, asset, AccountTypeUser)
	updated, err := q.UpdateAccountBalances(ctx, db.UpdateAccountBalancesParams{
		AccountID: toPgUUID(accID), AvailableDelta: amount, LockedDelta: amount.Neg(),
	})
	if err != nil {
		return err
	}
	if _, err := q.MarkWithdrawalFailedRefunded(ctx, toPgUUID(withdrawalID)); err != nil {
		return err
	}
	journalTxID := uuid.New()
	ref := fmt.Sprintf("withdraw_refund:%s", withdrawalID)
	if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
		JournalTxID: toPgUUID(journalTxID), AccountID: toPgUUID(accID), Asset: asset,
		EntryType: EntryTypeCancelRelease, Direction: DirectionDebit, Amount: amount,
		BalanceAfter: updated.Locked, ReferenceID: ref,
	}); err != nil {
		return err
	}
	if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
		JournalTxID: toPgUUID(journalTxID), AccountID: toPgUUID(accID), Asset: asset,
		EntryType: EntryTypeCancelRelease, Direction: DirectionCredit, Amount: amount,
		BalanceAfter: updated.Available, ReferenceID: ref,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *PostgresStore) GetLedgerEntriesByTx(ctx context.Context, journalTxID uuid.UUID) ([]LedgerEntryRecord, error) {
	q := db.New(p.pool)
	rows, err := q.ListLedgerEntriesByJournalTx(ctx, toPgUUID(journalTxID))
	if err != nil {
		return nil, err
	}
	out := make([]LedgerEntryRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, LedgerEntryRecord{
			EntryID:      r.EntryID,
			JournalTxID:  fromPgUUID(r.JournalTxID),
			AccountID:    fromPgUUID(r.AccountID),
			Asset:        r.Asset,
			EntryType:    r.EntryType,
			Direction:    r.Direction,
			Amount:       r.Amount,
			BalanceAfter: r.BalanceAfter,
			ReferenceID:  r.ReferenceID,
			CreatedAt:    r.CreatedAt,
		})
	}
	return out, nil
}

// GetLatestJournalTxByReference returns the latest journal_tx_id recorded for reference_id.
func (p *PostgresStore) GetLatestJournalTxByReference(ctx context.Context, refID string) (uuid.UUID, error) {
	var raw pgtype.UUID
	err := p.pool.QueryRow(ctx,
		`SELECT journal_tx_id FROM ledger_entries WHERE reference_id = $1 ORDER BY entry_id DESC LIMIT 1`,
		refID,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	return fromPgUUID(raw), nil
}

func (p *PostgresStore) SnapshotLiabilities(ctx context.Context) (map[string]AssetTotal, []UserLiabilitySummary, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	totals := make(map[string]AssetTotal, len(AllAssets))
	for _, a := range AllAssets {
		totals[a] = AssetTotal{UserTotal: decimal.Zero, FeeTotal: decimal.Zero}
	}

	userRows, err := q.SumUserLiabilitiesByAsset(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, r := range userRows {
		t := totals[r.Asset]
		t.UserTotal = r.TotalAvailable.Add(r.TotalLocked)
		totals[r.Asset] = t
	}

	feeRows, err := q.SumFeeTreasuryByAsset(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, r := range feeRows {
		t := totals[r.Asset]
		t.FeeTotal = r.TotalFees
		totals[r.Asset] = t
	}

	accRows, err := q.ListAllUserAccountsForMerkle(ctx)
	if err != nil {
		return nil, nil, err
	}
	userMap := make(map[uuid.UUID]map[string]decimal.Decimal)
	var orderedIDs []uuid.UUID
	for _, r := range accRows {
		uid := fromPgUUID(r.UserID)
		if userMap[uid] == nil {
			userMap[uid] = make(map[string]decimal.Decimal)
			orderedIDs = append(orderedIDs, uid)
		}
		userMap[uid][r.Asset] = r.Available.Add(r.Locked)
	}
	users := make([]UserLiabilitySummary, 0, len(orderedIDs))
	for _, uid := range orderedIDs {
		users = append(users, UserLiabilitySummary{UserID: uid, Balances: userMap[uid]})
	}
	return totals, users, nil
}

func dbOrderToRecord(o db.Order) *OrderRecord {
	var walSeq int64
	if o.WalSeq.Valid {
		walSeq = o.WalSeq.Int64
	}
	return &OrderRecord{
		OrderID:        o.OrderID,
		IdempotencyKey: fromPgUUID(o.IdempotencyKey),
		UserID:         fromPgUUID(o.UserID),
		MarketID:       o.MarketID,
		Side:           o.Side,
		OrderType:      o.OrderType,
		TimeInForce:    o.TimeInForce,
		Price:          o.Price,
		Quantity:       o.Quantity,
		FilledQuantity: o.FilledQuantity,
		LockedAmount:   o.LockedAmount,
		WalSeq:         walSeq,
		Status:         o.Status,
		CreatedAt:      o.CreatedAt,
		UpdatedAt:      o.UpdatedAt,
	}
}

func dbWithdrawalToRecord(w db.WithdrawalOutbox) *WithdrawalRecord {
	var eoaNonce *int64
	if w.RelayerEoaNonce.Valid {
		v := w.RelayerEoaNonce.Int64
		eoaNonce = &v
	}
	return &WithdrawalRecord{
		WithdrawalID:     fromPgUUID(w.WithdrawalID),
		IdempotencyKey:   fromPgUUID(w.IdempotencyKey),
		UserID:           fromPgUUID(w.UserID),
		RecipientAddress: w.RecipientAddress,
		Asset:            w.Asset,
		HumanAmount:      w.HumanAmount,
		RawAmountWei:     w.RawAmountWei.BigInt(),
		SigningTier:      w.SigningTier,
		VoucherNonce:     w.VoucherNonce,
		RelayerEOANonce:  eoaNonce,
		LastTxHash:       w.LastTxHash.String,
		GasBumpCount:     w.GasBumpCount,
		Status:           w.Status,
		CreatedAt:        w.CreatedAt,
		UpdatedAt:        w.UpdatedAt,
	}
}
