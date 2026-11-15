package ledger

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/shopspring/decimal"
)

const (
	// CLOBDecimals is the fixed-point decimal scale (1e8) used by the C++17 CLOB.
	CLOBDecimals int32 = 8
	// CLOBScale is 10^8 internal ticks per 1 whole unit.
	CLOBScale int64 = 100_000_000
	// MinCLOBValue is the smallest valid positive tick value (1 tick = 10^-8).
	MinCLOBValue int64 = 1
	// MaxCLOBValue is the hard ingress cap (10^15 ticks = 10,000,000.00000000 whole units).
	MaxCLOBValue int64 = 1_000_000_000_000_000
	// SixDecimalLotStep is the CLOB tick modulus (100 ticks = 10^-6) for 6-decimal quote tokens.
	SixDecimalLotStep int64 = 100
)

// Supported asset symbols.
const (
	AssetETH  = "ETH"
	AssetUSDC = "USDC"
	AssetWBTC = "WBTC"
	AssetUSDT = "USDT"
)

// Supported trading pairs.
const (
	MarketETHUSDC = "ETH/USDC"
	MarketBTCUSDC = "BTC/USDC"
)

var (
	// ErrInvalidAsset indicates an unsupported token symbol.
	ErrInvalidAsset = errors.New("unsupported asset")
	// ErrInvalidMarket indicates an unsupported market pair.
	ErrInvalidMarket = errors.New("unsupported market")
	// ErrInvalidPrecision indicates decimal precision, tick/lot modulus, or bounds violation.
	ErrInvalidPrecision = errors.New("ERR_INVALID_DECIMAL_PRECISION")
	// ErrNumericOverflow indicates intermediate or final fixed-point overflow.
	ErrNumericOverflow = errors.New("fixed-point numeric overflow")
)

var (
	bigCLOBScale = big.NewInt(CLOBScale)
	big10        = big.NewInt(10)
	bigTenPow10  = new(big.Int).Exp(big10, big.NewInt(10), nil) // 10^(18-8) for ETH
	big100       = big.NewInt(100)                              // 10^(8-6) for USDC/USDT
	bigBpsDiv    = big.NewInt(10_000)
	bigBpsRound  = big.NewInt(9_999)
	bigMaxInt64  = big.NewInt(1<<63 - 1)
)

// AllAssets lists the four custody assets supported by SettleCore.
var AllAssets = []string{AssetETH, AssetUSDC, AssetWBTC, AssetUSDT}

// AssetDecimals returns the native on-chain decimal precision for an asset.
func AssetDecimals(asset string) (int32, error) {
	switch strings.ToUpper(asset) {
	case AssetETH:
		return 18, nil
	case AssetWBTC:
		return 8, nil
	case AssetUSDC, AssetUSDT:
		return 6, nil
	default:
		return 0, fmt.Errorf("%w: %s", ErrInvalidAsset, asset)
	}
}

// MarketAssets returns the base asset and quote asset for a supported market ID.
func MarketAssets(marketID string) (baseAsset string, quoteAsset string, err error) {
	switch strings.ToUpper(marketID) {
	case MarketETHUSDC:
		return AssetETH, AssetUSDC, nil
	case MarketBTCUSDC:
		return AssetWBTC, AssetUSDC, nil
	default:
		return "", "", fmt.Errorf("%w: %s", ErrInvalidMarket, marketID)
	}
}

// ParseDecimalString parses a base-10 decimal string strictly without floats.
func ParseDecimalString(raw string) (decimal.Decimal, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return decimal.Zero, fmt.Errorf("%w: empty decimal string", ErrInvalidPrecision)
	}
	d, err := decimal.NewFromString(trimmed)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: %v", ErrInvalidPrecision, err)
	}
	return d, nil
}

// HumanToCLOBInt64 converts a human-readable decimal into 1e8 CLOB int64 ticks,
// validating positive bounds [1, 10^15], exact 1e8 integer representation, and
// 100-tick lot modulus for 6-decimal assets (USDC, USDT).
func HumanToCLOBInt64(asset string, d decimal.Decimal) (int64, error) {
	dec, err := AssetDecimals(asset)
	if err != nil {
		return 0, err
	}
	if !d.IsPositive() {
		return 0, fmt.Errorf("%w: value must be positive", ErrInvalidPrecision)
	}

	scaled := d.Shift(CLOBDecimals)
	if !scaled.IsInteger() {
		return 0, fmt.Errorf("%w: %s exceeds 8 decimal places", ErrInvalidPrecision, d.String())
	}

	bi := scaled.BigInt()
	if bi.Sign() <= 0 || bi.Cmp(big.NewInt(MaxCLOBValue)) > 0 {
		return 0, fmt.Errorf("%w: value out of [1, 10^15] tick bounds", ErrInvalidPrecision)
	}
	if !bi.IsInt64() {
		return 0, fmt.Errorf("%w: value overflows int64", ErrNumericOverflow)
	}
	ticks := bi.Int64()
	if ticks < MinCLOBValue || ticks > MaxCLOBValue {
		return 0, fmt.Errorf("%w: value out of [1, 10^15] tick bounds", ErrInvalidPrecision)
	}

	// For 6-decimal tokens (USDC, USDT), enforce 100-tick modulus (10^-6 whole units)
	// so every settled quote amount maps cleanly to integer on-chain base units.
	if dec == 6 && ticks%SixDecimalLotStep != 0 {
		return 0, fmt.Errorf("%w: %s requires 6-decimal lot step (100 CLOB ticks)", ErrInvalidPrecision, asset)
	}

	return ticks, nil
}

// PriceToCLOBInt64 validates and converts an order price for a market to 1e8 ticks.
func PriceToCLOBInt64(marketID string, price decimal.Decimal) (int64, error) {
	_, quote, err := MarketAssets(marketID)
	if err != nil {
		return 0, err
	}
	return HumanToCLOBInt64(quote, price)
}

// QtyToCLOBInt64 validates and converts an order base quantity for a market to 1e8 ticks.
func QtyToCLOBInt64(marketID string, qty decimal.Decimal) (int64, error) {
	base, _, err := MarketAssets(marketID)
	if err != nil {
		return 0, err
	}
	return HumanToCLOBInt64(base, qty)
}

// CLOBInt64ToHuman converts 1e8 fixed-point ticks back to a human decimal.Decimal.
func CLOBInt64ToHuman(ticks int64) decimal.Decimal {
	return decimal.New(ticks, -CLOBDecimals)
}

// HumanToWei converts a human decimal amount into exact native on-chain base units (*big.Int).
func HumanToWei(asset string, d decimal.Decimal) (*big.Int, error) {
	dec, err := AssetDecimals(asset)
	if err != nil {
		return nil, err
	}
	if d.IsNegative() {
		return nil, fmt.Errorf("%w: negative amount", ErrInvalidPrecision)
	}
	scaled := d.Shift(dec)
	if !scaled.IsInteger() {
		return nil, fmt.Errorf("%w: %s exceeds %d native decimals for %s", ErrInvalidPrecision, d.String(), dec, asset)
	}
	return scaled.BigInt(), nil
}

// WeiToHuman converts an on-chain native integer amount (*big.Int) to a human decimal.Decimal.
func WeiToHuman(asset string, wei *big.Int) (decimal.Decimal, error) {
	dec, err := AssetDecimals(asset)
	if err != nil {
		return decimal.Zero, err
	}
	if wei == nil || wei.Sign() < 0 {
		return decimal.Zero, fmt.Errorf("%w: nil or negative wei amount", ErrInvalidPrecision)
	}
	return decimal.NewFromBigInt(wei, -dec), nil
}

// CLOBInt64ToWei converts 1e8 CLOB ticks into exact native on-chain base units (*big.Int).
func CLOBInt64ToWei(asset string, ticks int64) (*big.Int, error) {
	if ticks < 0 {
		return nil, fmt.Errorf("%w: negative ticks", ErrInvalidPrecision)
	}
	dec, err := AssetDecimals(asset)
	if err != nil {
		return nil, err
	}
	v := big.NewInt(ticks)
	switch dec {
	case 18:
		return new(big.Int).Mul(v, bigTenPow10), nil
	case 8:
		return v, nil
	case 6:
		if ticks%SixDecimalLotStep != 0 {
			return nil, fmt.Errorf("%w: ticks %d not aligned to 6-decimal lot step", ErrInvalidPrecision, ticks)
		}
		return new(big.Int).Div(v, big100), nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrInvalidAsset, asset)
	}
}

// WeiToCLOBInt64 converts native on-chain wei to 1e8 CLOB ticks plus any sub-CLOB dust wei.
func WeiToCLOBInt64(asset string, wei *big.Int) (ticks int64, dustWei *big.Int, err error) {
	if wei == nil || wei.Sign() < 0 {
		return 0, nil, fmt.Errorf("%w: nil or negative wei", ErrInvalidPrecision)
	}
	dec, err := AssetDecimals(asset)
	if err != nil {
		return 0, nil, err
	}
	var q, r *big.Int
	switch dec {
	case 18:
		q = new(big.Int)
		r = new(big.Int)
		q.QuoRem(wei, bigTenPow10, r)
	case 8:
		q = new(big.Int).Set(wei)
		r = big.NewInt(0)
	case 6:
		q = new(big.Int).Mul(wei, big100)
		r = big.NewInt(0)
	default:
		return 0, nil, fmt.Errorf("%w: %s", ErrInvalidAsset, asset)
	}
	if !q.IsInt64() {
		return 0, nil, fmt.Errorf("%w: wei overflows int64 ticks", ErrNumericOverflow)
	}
	return q.Int64(), r, nil
}

// ComputeQuoteNotional1e8 computes floor(price_1e8 * qty_1e8 / 1e8) using arbitrary-precision
// integer arithmetic matching the C++17 __int128_t implementation in ADR-001.
func ComputeQuoteNotional1e8(price1e8, qty1e8 int64) (int64, error) {
	if price1e8 < 0 || qty1e8 < 0 {
		return 0, fmt.Errorf("%w: negative price or qty", ErrInvalidPrecision)
	}
	prod := new(big.Int).Mul(big.NewInt(price1e8), big.NewInt(qty1e8))
	notional := new(big.Int).Div(prod, bigCLOBScale)
	if notional.Cmp(bigMaxInt64) > 0 {
		return 0, fmt.Errorf("%w: quote notional overflows int64", ErrNumericOverflow)
	}
	return notional.Int64(), nil
}

// ComputeFeeCeil1e8 computes ceil(notional_1e8 * fee_bps / 10000) matching ADR-001.
func ComputeFeeCeil1e8(notional1e8, feeBps int64) (int64, error) {
	if notional1e8 < 0 || feeBps < 0 {
		return 0, fmt.Errorf("%w: negative notional or feeBps", ErrInvalidPrecision)
	}
	if notional1e8 == 0 || feeBps == 0 {
		return 0, nil
	}
	num := new(big.Int).Mul(big.NewInt(notional1e8), big.NewInt(feeBps))
	num.Add(num, bigBpsRound)
	fee := new(big.Int).Div(num, bigBpsDiv)
	if fee.Cmp(bigMaxInt64) > 0 {
		return 0, fmt.Errorf("%w: fee overflows int64", ErrNumericOverflow)
	}
	return fee.Int64(), nil
}
