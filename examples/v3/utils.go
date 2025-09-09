package main

import (
	"contract-template/sdk"
	"math/big"
	"strconv"
	"strings"
)

const (
	KeyAsset0      = "asset0"
	KeyAsset1      = "asset1"
	KeyFeeBps      = "fee_bps"
	KeySqrtP       = "sqrt_price_q32"
	KeyLiquidity   = "liquidity"
	KeyActiveLower = "active_lower_q32"
	KeyActiveUpper = "active_upper_q32"
	KeyFeeGrowth0  = "fee_growth0_q32"
	KeyFeeGrowth1  = "fee_growth1_q32"
)

const qShift = 32

func qMul(a, b uint64) uint64 {
	var prod big.Int
	prod.SetUint64(a)
	prod.Mul(&prod, new(big.Int).SetUint64(b))
	prod.Rsh(&prod, uint(qShift))
	if prod.Sign() < 0 || prod.BitLen() > 64 {
		sdk.Abort("qMul overflow")
	}
	return prod.Uint64()
}

func qDiv(a, b uint64) uint64 {
	if b == 0 {
		sdk.Abort("div by zero")
	}
	var num big.Int
	num.SetUint64(a)
	num.Lsh(&num, uint(qShift))
	den := new(big.Int).SetUint64(b)
	quo := new(big.Int).Quo(&num, den)
	if quo.Sign() < 0 || quo.BitLen() > 64 {
		sdk.Abort("qDiv overflow")
	}
	return quo.Uint64()
}

func minU64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func getStr(key string) string {
	v := sdk.StateGetObject(key)
	if v == nil {
		return ""
	}
	return *v
}
func setStr(key, v string) { sdk.StateSetObject(key, v) }
func getU(key string) uint64 {
	s := getStr(key)
	if s == "" {
		return 0
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}
	return n
}
func setU(key string, v uint64) { sdk.StateSetObject(key, strconv.FormatUint(v, 10)) }
func posKey(addr sdk.Address, lower, upper uint64, suffix string) string {
	return "pos/" + addr.String() + "/" + strconv.FormatUint(lower, 10) + "/" + strconv.FormatUint(upper, 10) + "/" + suffix
}

func getAssets() (sdk.Asset, sdk.Asset) {
	return sdk.Asset(getStr(KeyAsset0)), sdk.Asset(getStr(KeyAsset1))
}

func updatePositionOwed(owner sdk.Address, lower, upper uint64) {
	L := getU(posKey(owner, lower, upper, "liquidity"))
	if L == 0 {
		return
	}
	fg0 := getU(KeyFeeGrowth0)
	fg1 := getU(KeyFeeGrowth1)
	last0 := getU(posKey(owner, lower, upper, "fg0_last"))
	last1 := getU(posKey(owner, lower, upper, "fg1_last"))
	if fg0 > last0 {
		delta := fg0 - last0
		var owedBi big.Int
		owedBi.SetUint64(delta)
		owedBi.Mul(&owedBi, new(big.Int).SetUint64(L))
		owedBi.Rsh(&owedBi, uint(qShift))
		if owedBi.BitLen() > 64 {
			sdk.Abort("owed overflow")
		}
		owed := getU(posKey(owner, lower, upper, "owed0"))
		owed += owedBi.Uint64()
		setU(posKey(owner, lower, upper, "owed0"), owed)
		setU(posKey(owner, lower, upper, "fg0_last"), fg0)
	}
	if fg1 > last1 {
		delta := fg1 - last1
		var owedBi big.Int
		owedBi.SetUint64(delta)
		owedBi.Mul(&owedBi, new(big.Int).SetUint64(L))
		owedBi.Rsh(&owedBi, uint(qShift))
		if owedBi.BitLen() > 64 {
			sdk.Abort("owed overflow")
		}
		owed := getU(posKey(owner, lower, upper, "owed1"))
		owed += owedBi.Uint64()
		setU(posKey(owner, lower, upper, "owed1"), owed)
		setU(posKey(owner, lower, upper, "fg1_last"), fg1)
	}
}

// Liquidity math (Q32.32 sqrt prices)
func getLiquidityForAmount0(sqrtA, sqrtB, amount0 uint64) uint64 {
	if sqrtA > sqrtB {
		sqrtA, sqrtB = sqrtB, sqrtA
	}
	num := qMul(qMul(amount0<<qShift, sqrtA), sqrtB) // safe Q math
	den := (sqrtB - sqrtA)
	if den == 0 {
		sdk.Abort("den zero")
	}
	return num / den
}

func getLiquidityForAmount1(sqrtA, sqrtB, amount1 uint64) uint64 {
	if sqrtA > sqrtB {
		sqrtA, sqrtB = sqrtB, sqrtA
	}
	den := (sqrtB - sqrtA)
	if den == 0 {
		sdk.Abort("den zero")
	}
	return (amount1 << qShift) / den
}

// Amounts owed for a given liquidity share within [sqrtA, sqrtB] at current sqrtP
func amountOwedFromLiquidity(liq, sqrtA, sqrtB, sqrtP uint64) (amt0, amt1 uint64) {
	if sqrtA > sqrtB {
		sqrtA, sqrtB = sqrtB, sqrtA
	}
	if sqrtP <= sqrtA {
		// entirely in token0 side
		num := liq * (sqrtB - sqrtA)
		prod := qMul(sqrtA, sqrtB)
		if prod == 0 {
			return 0, 0
		}
		amt0 = qDiv(num, prod)
		return
	}
	if sqrtP >= sqrtB {
		// entirely in token1 side
		amt1 = (liq * (sqrtB - sqrtA)) >> qShift
		return
	}
	// In range: split
	// amount0 = L * (sqrtB - sqrtP) / (sqrtB*sqrtP)
	num0 := liq * (sqrtB - sqrtP)
	prod0 := qMul(sqrtB, sqrtP)
	if prod0 != 0 {
		amt0 = qDiv(num0, prod0)
	}
	// amount1 = L * (sqrtP - sqrtA)
	amt1 = (liq * (sqrtP - sqrtA)) >> qShift
	return
}

// Governance utilities
func isSystemSender() bool {
	env := sdk.GetEnv()
	return env.Sender.Address.Domain() == sdk.AddressDomainSystem
}

// pause feature removed

// Number formatting utilities (supports up to 256-bit unsigned)
type numFormat struct {
	base         int
	hasHexPrefix bool
	digitCount   int
	leadingZeros int
}

func parseNumberWithFormat(s string) (*big.Int, numFormat, bool) {
	orig := strings.TrimSpace(s)
	if orig == "" {
		return nil, numFormat{}, false
	}
	fmt := numFormat{}
	var numStr string
	// Hex detection with optional 0x/0X prefix
	if len(orig) > 2 && (orig[0:2] == "0x" || orig[0:2] == "0X") {
		fmt.base = 16
		fmt.hasHexPrefix = true
		numStr = orig[2:]
	} else {
		// If contains only hex digits and letters a-f/A-F with any 'x' not at start? We default to decimal unless prefixed
		fmt.base = 10
		numStr = orig
	}
	// Track formatting characteristics
	// Count leading zeros in the numeric part
	leading := 0
	for leading < len(numStr) && numStr[leading] == '0' {
		leading++
	}
	fmt.leadingZeros = leading
	fmt.digitCount = len(numStr)
	// Parse
	bi := new(big.Int)
	if fmt.base == 16 {
		// Empty hex means zero
		if numStr == "" {
			return new(big.Int), fmt, true
		}
		_, ok := bi.SetString(numStr, 16)
		if !ok {
			return nil, numFormat{}, false
		}
	} else {
		_, ok := bi.SetString(numStr, 10)
		if !ok {
			return nil, numFormat{}, false
		}
	}
	// Only unsigned supported
	if bi.Sign() < 0 {
		return nil, numFormat{}, false
	}
	return bi, fmt, true
}

func formatNumberWithFormat(bi *big.Int, fmt numFormat) string {
	if fmt.base == 16 {
		d := bi.Text(16)
		// lower-case hex; left-pad to preserve original digit count
		if len(d) < fmt.digitCount {
			pad := make([]byte, fmt.digitCount-len(d))
			for i := range pad {
				pad[i] = '0'
			}
			d = string(pad) + d
		}
		if fmt.hasHexPrefix {
			return "0x" + d
		}
		return d
	}
	// Decimal: preserve total digit count (including leading zeros)
	d := bi.Text(10)
	if len(d) < fmt.digitCount {
		pad := make([]byte, fmt.digitCount-len(d))
		for i := range pad {
			pad[i] = '0'
		}
		d = string(pad) + d
	}
	return d
}
