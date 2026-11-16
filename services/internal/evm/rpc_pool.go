package evm

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"
	"github.com/settlecore/settlecore/services/internal/metrics"
)

var (
	// ErrNoHealthyRPC indicates all configured RPC providers are unavailable.
	ErrNoHealthyRPC = errors.New("no healthy EVM RPC endpoint available")
	// ErrBlockNotFound indicates the requested block height was not returned by RPC.
	ErrBlockNotFound = errors.New("block not found")
)

// BlockHeader captures block lineage metadata for reorg-safe indexing.
type BlockHeader struct {
	Number     int64
	Hash       string
	ParentHash string
	BaseFee    *big.Int
}

// DepositLog represents an on-chain EscrowVault.Deposited event.
type DepositLog struct {
	TxHash       string
	LogIndex     int32
	BlockNumber  int64
	BlockHash    string
	Depositor    string
	UserID       uuid.UUID
	Asset        string
	RawAmountWei *big.Int
}

// TxSubmission captures an EIP-1559 transaction payload for relayer broadcast.
type TxSubmission struct {
	Nonce                uint64
	To                   common.Address
	Data                 []byte
	MaxFeePerGasWei      *big.Int
	MaxPriorityFeePerGas *big.Int
	RawTx                *types.Transaction
}

// TxReceipt represents a mined transaction status.
type TxReceipt struct {
	TxHash      string
	BlockNumber int64
	Confirmed   bool
	Succeeded   bool
}

// ChainProvider abstracts JSON-RPC interactions for live Anvil/Sepolia ethclient or test doubles.
type ChainProvider interface {
	Name() string
	HeadNumber(ctx context.Context) (int64, error)
	BlockByNumber(ctx context.Context, num int64) (BlockHeader, error)
	DepositLogs(ctx context.Context, blockNum int64) ([]DepositLog, error)
	SuggestGasFees(ctx context.Context) (baseFee *big.Int, tip *big.Int, err error)
	PendingNonce(ctx context.Context, from common.Address) (uint64, error)
	BroadcastTx(ctx context.Context, sub TxSubmission) (string, error)
	TxReceipt(ctx context.Context, txHash string) (TxReceipt, error)
	VaultBalance(ctx context.Context, asset string) (*big.Int, error)
}

// EndpointState tracks health and chaos state for a single RPC node.
type EndpointState struct {
	Name             string        `json:"name"`
	URL              string        `json:"url"`
	Provider         ChainProvider `json:"-"`
	Healthy          bool          `json:"healthy"`
	Killed           bool          `json:"killed"`
	HeadBlock        int64         `json:"head_block"`
	ConsecutiveFails int           `json:"consecutive_fails"`
	LastChecked      time.Time     `json:"last_checked"`
}

// RPCPool provides health-scored sub-500ms failover between primary and fallback RPC endpoints.
type RPCPool struct {
	mu           sync.RWMutex
	endpoints    []*EndpointState
	activeIdx    int
	probeTimeout time.Duration
	metricsReg   *metrics.Registry
}

// NewRPCPool builds an RPCPool from pre-constructed ChainProvider instances.
func NewRPCPool(providers []ChainProvider, reg *metrics.Registry) *RPCPool {
	if reg == nil {
		reg = metrics.Default
	}
	eps := make([]*EndpointState, 0, len(providers))
	names := make([]string, 0, len(providers))
	for _, p := range providers {
		eps = append(eps, &EndpointState{
			Name:     p.Name(),
			Provider: p,
			Healthy:  true,
		})
		names = append(names, p.Name())
	}
	if len(names) > 0 {
		reg.SetActiveRPC(names[0], names...)
	}
	return &RPCPool{
		endpoints:    eps,
		activeIdx:    0,
		probeTimeout: 400 * time.Millisecond,
		metricsReg:   reg,
	}
}

// NewEthRPCPool dials primary and fallback EVM JSON-RPC endpoints using go-ethereum ethclient.
func NewEthRPCPool(primaryURL, fallbackURL, vaultAddr, usdcAddr, wbtcAddr, usdtAddr string, reg *metrics.Registry) (*RPCPool, error) {
	p1, err := newEthChainProvider("rpc-proxy-1", primaryURL, vaultAddr, usdcAddr, wbtcAddr, usdtAddr)
	if err != nil {
		return nil, err
	}
	providers := []ChainProvider{p1}
	if fallbackURL != "" {
		if p2, err := newEthChainProvider("rpc-proxy-2", fallbackURL, vaultAddr, usdcAddr, wbtcAddr, usdtAddr); err == nil {
			providers = append(providers, p2)
		}
	}
	pool := NewRPCPool(providers, reg)
	pool.endpoints[0].URL = primaryURL
	if len(pool.endpoints) > 1 {
		pool.endpoints[1].URL = fallbackURL
	}
	return pool, nil
}

// ActiveName returns the name of the currently active RPC endpoint.
func (p *RPCPool) ActiveName() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.endpoints) == 0 {
		return ""
	}
	return p.endpoints[p.activeIdx].Name
}

// Status returns a copy of all endpoint health states.
func (p *RPCPool) Status() []EndpointState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]EndpointState, len(p.endpoints))
	for i, ep := range p.endpoints {
		out[i] = *ep
	}
	return out
}

// KillPrimary simulates severing rpc-proxy-1 and fails over immediately (<500ms) to rpc-proxy-2.
func (p *RPCPool) KillPrimary() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.endpoints) > 0 {
		p.endpoints[0].Killed = true
		p.endpoints[0].Healthy = false
		p.endpoints[0].ConsecutiveFails = 3
	}
	p.selectBestLocked()
	return p.endpoints[p.activeIdx].Name
}

// RestorePrimary restores rpc-proxy-1 and switches active provider back to primary.
func (p *RPCPool) RestorePrimary() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.endpoints) > 0 {
		p.endpoints[0].Killed = false
		p.endpoints[0].Healthy = true
		p.endpoints[0].ConsecutiveFails = 0
		p.activeIdx = 0
	}
	p.syncMetricsLocked()
	return p.endpoints[p.activeIdx].Name
}

// Probe checks all endpoints and switches active provider if current is down or lagging > 2 blocks.
func (p *RPCPool) Probe(ctx context.Context) {
	p.mu.Lock()
	eps := make([]*EndpointState, len(p.endpoints))
	copy(eps, p.endpoints)
	p.mu.Unlock()

	maxHead := int64(0)
	heads := make([]int64, len(eps))
	errs := make([]error, len(eps))

	for i, ep := range eps {
		if ep.Killed {
			errs[i] = errors.New("endpoint killed")
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, p.probeTimeout)
		h, err := ep.Provider.HeadNumber(probeCtx)
		cancel()
		heads[i] = h
		errs[i] = err
		if err == nil && h > maxHead {
			maxHead = h
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().UTC()
	for i, ep := range p.endpoints {
		ep.LastChecked = now
		if ep.Killed || errs[i] != nil {
			ep.ConsecutiveFails++
			ep.Healthy = false
			continue
		}
		ep.HeadBlock = heads[i]
		if maxHead-heads[i] > 2 {
			ep.ConsecutiveFails++
			ep.Healthy = false
		} else {
			ep.ConsecutiveFails = 0
			ep.Healthy = true
		}
	}
	p.selectBestLocked()
}

func (p *RPCPool) selectBestLocked() {
	if len(p.endpoints) == 0 {
		return
	}
	if !p.endpoints[p.activeIdx].Healthy {
		for i, ep := range p.endpoints {
			if ep.Healthy && !ep.Killed {
				p.activeIdx = i
				break
			}
		}
	}
	p.syncMetricsLocked()
}

func (p *RPCPool) syncMetricsLocked() {
	names := make([]string, len(p.endpoints))
	for i, ep := range p.endpoints {
		names[i] = ep.Name
	}
	p.metricsReg.SetActiveRPC(p.endpoints[p.activeIdx].Name, names...)
}

// Call executes an RPC call against the active endpoint and fails over on error within <500ms.
func (p *RPCPool) Call(ctx context.Context, fn func(context.Context, ChainProvider) error) error {
	p.mu.RLock()
	n := len(p.endpoints)
	startIdx := p.activeIdx
	p.mu.RUnlock()

	if n == 0 {
		return ErrNoHealthyRPC
	}

	var lastErr error
	for offset := 0; offset < n; offset++ {
		idx := (startIdx + offset) % n
		p.mu.RLock()
		ep := p.endpoints[idx]
		killed := ep.Killed
		prov := ep.Provider
		p.mu.RUnlock()
		if killed {
			continue
		}

		callCtx, cancel := context.WithTimeout(ctx, p.probeTimeout)
		err := fn(callCtx, prov)
		cancel()
		if err == nil {
			if offset > 0 {
				p.mu.Lock()
				p.activeIdx = idx
				ep.Healthy = true
				ep.ConsecutiveFails = 0
				p.syncMetricsLocked()
				p.mu.Unlock()
			}
			return nil
		}

		lastErr = err
		p.mu.Lock()
		ep.ConsecutiveFails++
		if ep.ConsecutiveFails >= 1 {
			ep.Healthy = false
			p.selectBestLocked()
		}
		p.mu.Unlock()
	}
	if lastErr == nil {
		return ErrNoHealthyRPC
	}
	return lastErr
}

// HeadNumber fetches the latest block height via the pool.
func (p *RPCPool) HeadNumber(ctx context.Context) (int64, error) {
	var res int64
	err := p.Call(ctx, func(c context.Context, prov ChainProvider) error {
		h, err := prov.HeadNumber(c)
		res = h
		return err
	})
	return res, err
}

// BlockByNumber fetches block header by height via the pool.
func (p *RPCPool) BlockByNumber(ctx context.Context, num int64) (BlockHeader, error) {
	var res BlockHeader
	err := p.Call(ctx, func(c context.Context, prov ChainProvider) error {
		b, err := prov.BlockByNumber(c, num)
		res = b
		return err
	})
	return res, err
}

// DepositLogs fetches EscrowVault deposit logs for a block height via the pool.
func (p *RPCPool) DepositLogs(ctx context.Context, blockNum int64) ([]DepositLog, error) {
	var res []DepositLog
	err := p.Call(ctx, func(c context.Context, prov ChainProvider) error {
		logs, err := prov.DepositLogs(c, blockNum)
		res = logs
		return err
	})
	return res, err
}

// SuggestGasFees returns current base fee and tip via the pool.
func (p *RPCPool) SuggestGasFees(ctx context.Context) (*big.Int, *big.Int, error) {
	var base, tip *big.Int
	err := p.Call(ctx, func(c context.Context, prov ChainProvider) error {
		b, t, err := prov.SuggestGasFees(c)
		base, tip = b, t
		return err
	})
	return base, tip, err
}

// PendingNonce queries the relayer EOA pending nonce via the pool.
func (p *RPCPool) PendingNonce(ctx context.Context, from common.Address) (uint64, error) {
	var n uint64
	err := p.Call(ctx, func(c context.Context, prov ChainProvider) error {
		v, err := prov.PendingNonce(c, from)
		n = v
		return err
	})
	return n, err
}

// BroadcastTx submits an EIP-1559 transaction via the pool.
func (p *RPCPool) BroadcastTx(ctx context.Context, sub TxSubmission) (string, error) {
	var txHash string
	err := p.Call(ctx, func(c context.Context, prov ChainProvider) error {
		h, err := prov.BroadcastTx(c, sub)
		txHash = h
		return err
	})
	return txHash, err
}

// TxReceipt queries transaction receipt via the pool.
func (p *RPCPool) TxReceipt(ctx context.Context, txHash string) (TxReceipt, error) {
	var rec TxReceipt
	err := p.Call(ctx, func(c context.Context, prov ChainProvider) error {
		r, err := prov.TxReceipt(c, txHash)
		rec = r
		return err
	})
	return rec, err
}

// VaultBalance queries on-chain EscrowVault reserves for a given asset.
func (p *RPCPool) VaultBalance(ctx context.Context, asset string) (*big.Int, error) {
	var bal *big.Int
	err := p.Call(ctx, func(c context.Context, prov ChainProvider) error {
		v, err := prov.VaultBalance(c, asset)
		bal = v
		return err
	})
	return bal, err
}

// ethChainProvider implements ChainProvider over go-ethereum ethclient.Client.
type ethChainProvider struct {
	name      string
	client    *ethclient.Client
	vaultAddr common.Address
	tokens    map[string]common.Address
	addrAsset map[common.Address]string
}

// Deposited(address indexed depositor, address indexed token, uint256 amount, uint256 timestamp)
var depositedEventSig = common.HexToHash("0x8752a472e571a816aea92eec8dae9baf628e840f4929fbcc2d155e6233ff68a7")

func newEthChainProvider(name, rawURL, vaultAddr, usdcAddr, wbtcAddr, usdtAddr string) (*ethChainProvider, error) {
	client, err := ethclient.Dial(rawURL)
	if err != nil {
		return nil, err
	}
	usdc := common.HexToAddress(usdcAddr)
	wbtc := common.HexToAddress(wbtcAddr)
	usdt := common.HexToAddress(usdtAddr)
	tokens := map[string]common.Address{
		"ETH":  {},
		"USDC": usdc,
		"WBTC": wbtc,
		"USDT": usdt,
	}
	addrAsset := map[common.Address]string{
		{}:   "ETH",
		usdc: "USDC",
		wbtc: "WBTC",
		usdt: "USDT",
	}
	return &ethChainProvider{
		name:      name,
		client:    client,
		vaultAddr: common.HexToAddress(vaultAddr),
		tokens:    tokens,
		addrAsset: addrAsset,
	}, nil
}

func (e *ethChainProvider) Name() string { return e.name }

func (e *ethChainProvider) HeadNumber(ctx context.Context) (int64, error) {
	n, err := e.client.BlockNumber(ctx)
	if err != nil {
		return 0, err
	}
	return int64(n), nil
}

func (e *ethChainProvider) BlockByNumber(ctx context.Context, num int64) (BlockHeader, error) {
	hdr, err := e.client.HeaderByNumber(ctx, big.NewInt(num))
	if err != nil {
		return BlockHeader{}, err
	}
	if hdr == nil {
		return BlockHeader{}, ErrBlockNotFound
	}
	baseFee := hdr.BaseFee
	if baseFee == nil {
		baseFee = big.NewInt(1_000_000_000)
	}
	return BlockHeader{
		Number:     hdr.Number.Int64(),
		Hash:       hdr.Hash().Hex(),
		ParentHash: hdr.ParentHash.Hex(),
		BaseFee:    baseFee,
	}, nil
}

func (e *ethChainProvider) DepositLogs(ctx context.Context, blockNum int64) ([]DepositLog, error) {
	bn := big.NewInt(blockNum)
	logs, err := e.client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: bn,
		ToBlock:   bn,
		Addresses: []common.Address{e.vaultAddr},
		Topics:    [][]common.Hash{{depositedEventSig}},
	})
	if err != nil {
		return nil, err
	}
	out := make([]DepositLog, 0, len(logs))
	for _, lg := range logs {
		if len(lg.Topics) < 3 || len(lg.Data) < 32 {
			continue
		}
		depositor := common.BytesToAddress(lg.Topics[1].Bytes())
		token := common.BytesToAddress(lg.Topics[2].Bytes())
		asset, ok := e.addrAsset[token]
		if !ok {
			continue
		}
		amount := new(big.Int).SetBytes(lg.Data[:32])
		out = append(out, DepositLog{
			TxHash:       lg.TxHash.Hex(),
			LogIndex:     int32(lg.Index),
			BlockNumber:  int64(lg.BlockNumber),
			BlockHash:    lg.BlockHash.Hex(),
			Depositor:    depositor.Hex(),
			UserID:       uuid.NewSHA1(uuid.NameSpaceOID, []byte("evm:"+depositor.Hex())),
			Asset:        asset,
			RawAmountWei: amount,
		})
	}
	return out, nil
}

func (e *ethChainProvider) SuggestGasFees(ctx context.Context) (*big.Int, *big.Int, error) {
	hdr, err := e.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	baseFee := hdr.BaseFee
	if baseFee == nil {
		baseFee = big.NewInt(1_000_000_000)
	}
	tip, err := e.client.SuggestGasTipCap(ctx)
	if err != nil || tip.Sign() <= 0 {
		tip = big.NewInt(1_500_000_000)
	}
	return baseFee, tip, nil
}

func (e *ethChainProvider) PendingNonce(ctx context.Context, from common.Address) (uint64, error) {
	return e.client.PendingNonceAt(ctx, from)
}

func (e *ethChainProvider) BroadcastTx(ctx context.Context, sub TxSubmission) (string, error) {
	if sub.RawTx == nil {
		return "", errors.New("missing signed RawTx")
	}
	if err := e.client.SendTransaction(ctx, sub.RawTx); err != nil {
		return "", err
	}
	return sub.RawTx.Hash().Hex(), nil
}

func (e *ethChainProvider) TxReceipt(ctx context.Context, txHash string) (TxReceipt, error) {
	rec, err := e.client.TransactionReceipt(ctx, common.HexToHash(txHash))
	if errors.Is(err, ethereum.NotFound) {
		return TxReceipt{TxHash: txHash, Confirmed: false}, nil
	}
	if err != nil {
		return TxReceipt{}, err
	}
	return TxReceipt{
		TxHash:      txHash,
		BlockNumber: rec.BlockNumber.Int64(),
		Confirmed:   true,
		Succeeded:   rec.Status == types.ReceiptStatusSuccessful,
	}, nil
}

func (e *ethChainProvider) VaultBalance(ctx context.Context, asset string) (*big.Int, error) {
	if asset == "ETH" {
		return e.client.BalanceAt(ctx, e.vaultAddr, nil)
	}
	tokenAddr, ok := e.tokens[asset]
	if !ok {
		return nil, fmt.Errorf("unknown token %s", asset)
	}
	// balanceOf(address) selector = 0x70a08231
	callData := make([]byte, 36)
	copy(callData[:4], []byte{0x70, 0xa0, 0x82, 0x31})
	copy(callData[16:36], e.vaultAddr.Bytes())
	res, err := e.client.CallContract(ctx, ethereum.CallMsg{
		To:   &tokenAddr,
		Data: callData,
	}, nil)
	if err != nil {
		return nil, err
	}
	if len(res) < 32 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(res[:32]), nil
}
