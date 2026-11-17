package evm

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	// SigningTierHot uses only HOT_SIGNER_KEY.
	SigningTierHot = "HOT"
	// SigningTierColdCosigned requires HOT_SIGNER_KEY + COLD_SIGNER_KEY.
	SigningTierColdCosigned = "COLD_COSIGNED"
)

// EIP-712 constants matching EscrowVault.sol & ADR-004.
const (
	EIP712DomainName    = "SettleCoreEscrowVault"
	EIP712DomainVersion = "1"
)

var (
	eip712DomainTypeHash = crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	withdrawalTypeHash   = crypto.Keccak256Hash([]byte("Withdrawal(bytes32 withdrawalId,address recipient,address token,uint256 amount,uint256 nonce,uint256 deadline)"))
)

// WithdrawalVoucher matches IEscrowVault.WithdrawalVoucher in Solidity.
type WithdrawalVoucher struct {
	WithdrawalID [32]byte
	Recipient    common.Address
	Token        common.Address
	Amount       *big.Int
	Nonce        *big.Int
	Deadline     *big.Int
}

// EIP712Signer computes EIP-712 digests and secp256k1 signatures for EscrowVault withdrawals.
type EIP712Signer struct {
	ChainID         *big.Int
	VaultAddress    common.Address
	DomainSeparator common.Hash
}

// NewEIP712Signer builds an EIP712Signer pinned to chainId and verifyingContract.
func NewEIP712Signer(chainID int64, vaultAddr common.Address) *EIP712Signer {
	cid := big.NewInt(chainID)
	nameHash := crypto.Keccak256Hash([]byte(EIP712DomainName))
	verHash := crypto.Keccak256Hash([]byte(EIP712DomainVersion))

	buf := make([]byte, 0, 5*32)
	buf = append(buf, eip712DomainTypeHash.Bytes()...)
	buf = append(buf, nameHash.Bytes()...)
	buf = append(buf, verHash.Bytes()...)
	buf = append(buf, common.LeftPadBytes(cid.Bytes(), 32)...)
	buf = append(buf, common.LeftPadBytes(vaultAddr.Bytes(), 32)...)
	domainSep := crypto.Keccak256Hash(buf)

	return &EIP712Signer{
		ChainID:         cid,
		VaultAddress:    vaultAddr,
		DomainSeparator: domainSep,
	}
}

// StructHash computes keccak256(abi.encode(WITHDRAWAL_TYPEHASH, ...)).
func (s *EIP712Signer) StructHash(v WithdrawalVoucher) common.Hash {
	amt := big.NewInt(0)
	if v.Amount != nil {
		amt = v.Amount
	}
	nonce := big.NewInt(0)
	if v.Nonce != nil {
		nonce = v.Nonce
	}
	deadline := big.NewInt(0)
	if v.Deadline != nil {
		deadline = v.Deadline
	}

	buf := make([]byte, 0, 7*32)
	buf = append(buf, withdrawalTypeHash.Bytes()...)
	buf = append(buf, v.WithdrawalID[:]...)
	buf = append(buf, common.LeftPadBytes(v.Recipient.Bytes(), 32)...)
	buf = append(buf, common.LeftPadBytes(v.Token.Bytes(), 32)...)
	buf = append(buf, common.LeftPadBytes(amt.Bytes(), 32)...)
	buf = append(buf, common.LeftPadBytes(nonce.Bytes(), 32)...)
	buf = append(buf, common.LeftPadBytes(deadline.Bytes(), 32)...)
	return crypto.Keccak256Hash(buf)
}

// TypedDataDigest computes keccak256("\x19\x01" || domainSeparator || structHash).
func (s *EIP712Signer) TypedDataDigest(v WithdrawalVoucher) common.Hash {
	sh := s.StructHash(v)
	buf := make([]byte, 0, 2+32+32)
	buf = append(buf, 0x19, 0x01)
	buf = append(buf, s.DomainSeparator.Bytes()...)
	buf = append(buf, sh.Bytes()...)
	return crypto.Keccak256Hash(buf)
}

// SignVoucher signs a WithdrawalVoucher with an ECDSA private key, producing a 65-byte
// Ethereum signature (r || s || v where v in {27, 28}).
func (s *EIP712Signer) SignVoucher(priv *ecdsa.PrivateKey, v WithdrawalVoucher) ([]byte, error) {
	if priv == nil {
		return nil, errors.New("nil signing private key")
	}
	digest := s.TypedDataDigest(v)
	sig, err := crypto.Sign(digest.Bytes(), priv)
	if err != nil {
		return nil, err
	}
	if sig[64] < 27 {
		sig[64] += 27
	}
	return sig, nil
}

// RecoverVoucherSigner recovers the signer address from a WithdrawalVoucher and 65-byte signature.
func (s *EIP712Signer) RecoverVoucherSigner(v WithdrawalVoucher, sig []byte) (common.Address, error) {
	if len(sig) != 65 {
		return common.Address{}, fmt.Errorf("invalid signature length %d", len(sig))
	}
	norm := make([]byte, 65)
	copy(norm, sig)
	if norm[64] >= 27 {
		norm[64] -= 27
	}
	digest := s.TypedDataDigest(v)
	pub, err := crypto.SigToPub(digest.Bytes(), norm)
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(*pub), nil
}

// ParseHexPrivateKey parses a 0x-prefixed or raw hex secp256k1 private key.
func ParseHexPrivateKey(hexKey string) (*ecdsa.PrivateKey, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(hexKey), "0x")
	return crypto.HexToECDSA(trimmed)
}

// ClassifyTier determines whether a withdrawal requires HOT or COLD_COSIGNED signing
// based on per-tx cap and rolling 24h daily cap.
func ClassifyTier(rawAmount, perTxCap, dailyCap, dailyUsed *big.Int) string {
	if rawAmount == nil {
		return SigningTierHot
	}
	if perTxCap != nil && perTxCap.Sign() > 0 && rawAmount.Cmp(perTxCap) > 0 {
		return SigningTierColdCosigned
	}
	if dailyCap != nil && dailyCap.Sign() > 0 {
		used := big.NewInt(0)
		if dailyUsed != nil {
			used = dailyUsed
		}
		if new(big.Int).Add(used, rawAmount).Cmp(dailyCap) > 0 {
			return SigningTierColdCosigned
		}
	}
	return SigningTierHot
}
