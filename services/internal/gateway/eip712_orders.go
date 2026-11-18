package gateway

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	// OrderEIP712DomainName is the EIP-712 domain name for signed order submissions (Upgrade H).
	OrderEIP712DomainName = "SettleCoreExchange"
	// OrderEIP712DomainVersion is the EIP-712 domain version.
	OrderEIP712DomainVersion = "1"
)

var (
	orderDomainTypeHash = crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	orderTypeHash       = crypto.Keccak256Hash([]byte("Order(string market,uint8 side,uint256 price,uint256 qty,uint8 tif,uint64 nonce,uint256 deadline)"))
)

// SignedOrderPayload captures the canonical EIP-712 typed-data fields for an order.
type SignedOrderPayload struct {
	Market   string
	Side     uint8
	Price1e8 int64
	Qty1e8   int64
	TIF      uint8
	Nonce    uint64
	Deadline int64
}

// OrderEIP712Verifier verifies secp256k1 EIP-712 typed-data signatures on orders.
type OrderEIP712Verifier struct {
	ChainID         *big.Int
	ContractAddress common.Address
	DomainSeparator common.Hash
}

// NewOrderEIP712Verifier constructs an OrderEIP712Verifier pinned to chainId and verifyingContract.
func NewOrderEIP712Verifier(chainID int64, verifyingContract common.Address) *OrderEIP712Verifier {
	cid := big.NewInt(chainID)
	nameHash := crypto.Keccak256Hash([]byte(OrderEIP712DomainName))
	verHash := crypto.Keccak256Hash([]byte(OrderEIP712DomainVersion))

	buf := make([]byte, 0, 5*32)
	buf = append(buf, orderDomainTypeHash.Bytes()...)
	buf = append(buf, nameHash.Bytes()...)
	buf = append(buf, verHash.Bytes()...)
	buf = append(buf, common.LeftPadBytes(cid.Bytes(), 32)...)
	buf = append(buf, common.LeftPadBytes(verifyingContract.Bytes(), 32)...)
	return &OrderEIP712Verifier{
		ChainID:         cid,
		ContractAddress: verifyingContract,
		DomainSeparator: crypto.Keccak256Hash(buf),
	}
}

// StructHash computes keccak256(abi.encode(ORDER_TYPEHASH, ...)).
func (v *OrderEIP712Verifier) StructHash(p SignedOrderPayload) common.Hash {
	marketHash := crypto.Keccak256Hash([]byte(p.Market))
	priceBig := big.NewInt(p.Price1e8)
	qtyBig := big.NewInt(p.Qty1e8)
	nonceBig := new(big.Int).SetUint64(p.Nonce)
	deadlineBig := big.NewInt(p.Deadline)

	buf := make([]byte, 0, 8*32)
	buf = append(buf, orderTypeHash.Bytes()...)
	buf = append(buf, marketHash.Bytes()...)
	buf = append(buf, common.LeftPadBytes([]byte{p.Side}, 32)...)
	buf = append(buf, common.LeftPadBytes(priceBig.Bytes(), 32)...)
	buf = append(buf, common.LeftPadBytes(qtyBig.Bytes(), 32)...)
	buf = append(buf, common.LeftPadBytes([]byte{p.TIF}, 32)...)
	buf = append(buf, common.LeftPadBytes(nonceBig.Bytes(), 32)...)
	buf = append(buf, common.LeftPadBytes(deadlineBig.Bytes(), 32)...)
	return crypto.Keccak256Hash(buf)
}

// TypedDataDigest computes keccak256("\x19\x01" || domainSeparator || structHash).
func (v *OrderEIP712Verifier) TypedDataDigest(p SignedOrderPayload) common.Hash {
	sh := v.StructHash(p)
	buf := make([]byte, 0, 2+32+32)
	buf = append(buf, 0x19, 0x01)
	buf = append(buf, v.DomainSeparator.Bytes()...)
	buf = append(buf, sh.Bytes()...)
	return crypto.Keccak256Hash(buf)
}

// SignOrder signs a SignedOrderPayload and returns a 0x-prefixed 65-byte EIP-712 signature.
func (v *OrderEIP712Verifier) SignOrder(priv *ecdsa.PrivateKey, p SignedOrderPayload) (string, error) {
	if priv == nil {
		return "", errors.New("nil signing private key")
	}
	digest := v.TypedDataDigest(p)
	sig, err := crypto.Sign(digest.Bytes(), priv)
	if err != nil {
		return "", err
	}
	if sig[64] < 27 {
		sig[64] += 27
	}
	return hexutil.Encode(sig), nil
}

// RecoverOrderSigner recovers the signer address from a SignedOrderPayload and hex signature.
func (v *OrderEIP712Verifier) RecoverOrderSigner(p SignedOrderPayload, sigHex string) (common.Address, error) {
	trimmed := strings.TrimSpace(sigHex)
	if !strings.HasPrefix(trimmed, "0x") && !strings.HasPrefix(trimmed, "0X") {
		trimmed = "0x" + trimmed
	}
	sig, err := hexutil.Decode(trimmed)
	if err != nil {
		return common.Address{}, fmt.Errorf("invalid hex signature: %w", err)
	}
	if len(sig) != 65 {
		return common.Address{}, fmt.Errorf("invalid signature length %d (want 65)", len(sig))
	}
	norm := make([]byte, 65)
	copy(norm, sig)
	if norm[64] >= 27 {
		norm[64] -= 27
	}
	digest := v.TypedDataDigest(p)
	pub, err := crypto.SigToPub(digest.Bytes(), norm)
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(*pub), nil
}
