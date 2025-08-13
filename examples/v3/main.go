package main

import (
	"contract-template/sdk"
	"strconv"
	"strings"
)

// Minimal v3-style AMM with a single active price range and per-position fee growth snapshots.

const (
	v3KeyPrefix      = "v3/"
	v3KeyAsset0      = v3KeyPrefix + "asset0"
	v3KeyAsset1      = v3KeyPrefix + "asset1"
	v3KeyFeeBps      = v3KeyPrefix + "fee_bps"
	v3KeySqrtP       = v3KeyPrefix + "sqrt_price_q32"
	v3KeyLiquidity   = v3KeyPrefix + "liquidity"
	v3KeyActiveLower = v3KeyPrefix + "active_lower_q32"
	v3KeyActiveUpper = v3KeyPrefix + "active_upper_q32"
	v3KeyFeeGrowth0  = v3KeyPrefix + "fee_growth0_q32"
	v3KeyFeeGrowth1  = v3KeyPrefix + "fee_growth1_q32"
	v3KeyFeeAcc0     = v3KeyPrefix + "fee_acc0"
	v3KeyFeeAcc1     = v3KeyPrefix + "fee_acc1"
)

const qShift = 32

func qMul(a, b uint64) uint64 {
	// (a * b) >> qShift, with limited overflow checking
	return (a / (1 << (qShift / 2))) * (b / (1 << (qShift / 2)))
}

func qDiv(a, b uint64) uint64 {
	if b == 0 {
		panic("div by zero")
	}
	return (a << qShift) / b
}

func minU64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func v3getStr(key string) string {
	v := sdk.StateGetObject(key)
	if v == nil {
		return ""
	}
	return *v
}
func v3setStr(key, v string) { sdk.StateSetObject(key, v) }
func v3getU(key string) uint64 {
	s := v3getStr(key)
	if s == "" {
		return 0
	}
	n, _ := strconv.ParseUint(s, 10, 64)
	return n
}
func v3setU(key string, v uint64) { sdk.StateSetObject(key, strconv.FormatUint(v, 10)) }
func v3posKey(addr sdk.Address, lower, upper uint64, suffix string) string {
	return v3KeyPrefix + "pos/" + addr.String() + "/" + strconv.FormatUint(lower, 10) + "/" + strconv.FormatUint(upper, 10) + "/" + suffix
}

//go:wasmexport init_v3
func InitV3(arg *string) *string {
	// args: asset0,asset1,fee_bps,init_sqrt_q32,active_lower_q32,active_upper_q32
	p := strings.Split(strings.TrimSpace(*arg), ",")
	if len(p) < 6 {
		panic("invalid args")
	}
	v3setStr(v3KeyAsset0, p[0])
	v3setStr(v3KeyAsset1, p[1])
	feeBps, _ := strconv.ParseUint(p[2], 10, 64)
	v3setU(v3KeyFeeBps, feeBps)
	sqrtP, _ := strconv.ParseUint(p[3], 10, 64)
	v3setU(v3KeySqrtP, sqrtP)
	lower, _ := strconv.ParseUint(p[4], 10, 64)
	upper, _ := strconv.ParseUint(p[5], 10, 64)
	if !(lower < sqrtP && sqrtP < upper) {
		panic("price not within active range")
	}
	v3setU(v3KeyActiveLower, lower)
	v3setU(v3KeyActiveUpper, upper)
	v3setU(v3KeyLiquidity, 0)
	v3setU(v3KeyFeeGrowth0, 0)
	v3setU(v3KeyFeeGrowth1, 0)
	v3setU(v3KeyFeeAcc0, 0)
	v3setU(v3KeyFeeAcc1, 0)
	return nil
}

func getAssetsV3() (sdk.Asset, sdk.Asset) {
	return sdk.Asset(v3getStr(v3KeyAsset0)), sdk.Asset(v3getStr(v3KeyAsset1))
}

func updatePositionOwed(owner sdk.Address, lower, upper uint64) {
	L := v3getU(v3posKey(owner, lower, upper, "liquidity"))
	if L == 0 {
		return
	}
	fg0 := v3getU(v3KeyFeeGrowth0)
	fg1 := v3getU(v3KeyFeeGrowth1)
	last0 := v3getU(v3posKey(owner, lower, upper, "fg0_last"))
	last1 := v3getU(v3posKey(owner, lower, upper, "fg1_last"))
	if fg0 > last0 {
		delta := fg0 - last0
		owed := v3getU(v3posKey(owner, lower, upper, "owed0"))
		owed += (delta * L) >> qShift
		v3setU(v3posKey(owner, lower, upper, "owed0"), owed)
		v3setU(v3posKey(owner, lower, upper, "fg0_last"), fg0)
	}
	if fg1 > last1 {
		delta := fg1 - last1
		owed := v3getU(v3posKey(owner, lower, upper, "owed1"))
		owed += (delta * L) >> qShift
		v3setU(v3posKey(owner, lower, upper, "owed1"), owed)
		v3setU(v3posKey(owner, lower, upper, "fg1_last"), fg1)
	}
}

// Liquidity math (Q32.32 sqrt prices)
func getLiquidityForAmount0(sqrtA, sqrtB, amount0 uint64) uint64 {
	if sqrtA > sqrtB {
		sqrtA, sqrtB = sqrtB, sqrtA
	}
	num := qMul(qMul(amount0<<qShift, sqrtA), sqrtB) // amount0 * (sqrtA*sqrtB)
	den := (sqrtB - sqrtA)
	return num / den >> qShift
}

func getLiquidityForAmount1(sqrtA, sqrtB, amount1 uint64) uint64 {
	if sqrtA > sqrtB {
		sqrtA, sqrtB = sqrtB, sqrtA
	}
	den := (sqrtB - sqrtA)
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
		prod := qMul(sqrtA, sqrtB) // (sqrtA*sqrtB) >> Q
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

//go:wasmexport mint_v3
func MintV3(arg *string) *string {
	// args: lower_q32,upper_q32,amount0,amount1
	p := strings.Split(strings.TrimSpace(*arg), ",")
	if len(p) != 4 {
		panic("invalid args")
	}
	lower, _ := strconv.ParseUint(p[0], 10, 64)
	upper, _ := strconv.ParseUint(p[1], 10, 64)
	amt0, _ := strconv.ParseUint(p[2], 10, 64)
	amt1, _ := strconv.ParseUint(p[3], 10, 64)

	// enforce single active range for now
	if lower != v3getU(v3KeyActiveLower) || upper != v3getU(v3KeyActiveUpper) {
		panic("range must equal active range")
	}

	sqrtP := v3getU(v3KeySqrtP)
	L0 := getLiquidityForAmount0(sqrtP, upper, amt0)
	L1 := getLiquidityForAmount1(lower, sqrtP, amt1)
	L := minU64(L0, L1)
	if L == 0 {
		panic("zero L")
	}

	a0, a1 := getAssetsV3()
	if amt0 > 0 {
		sdk.HiveDraw(int64(amt0), a0)
	}
	if amt1 > 0 {
		sdk.HiveDraw(int64(amt1), a1)
	}

	env := sdk.GetEnv()
	updatePositionOwed(env.Sender.Address, lower, upper)
	curL := v3getU(v3posKey(env.Sender.Address, lower, upper, "liquidity"))
	v3setU(v3posKey(env.Sender.Address, lower, upper, "liquidity"), curL+L)
	v3setU(v3posKey(env.Sender.Address, lower, upper, "fg0_last"), v3getU(v3KeyFeeGrowth0))
	v3setU(v3posKey(env.Sender.Address, lower, upper, "fg1_last"), v3getU(v3KeyFeeGrowth1))

	v3setU(v3KeyLiquidity, v3getU(v3KeyLiquidity)+L)
	return nil
}

//go:wasmexport burn_v3
func BurnV3(arg *string) *string {
	// args: lower_q32,upper_q32,liquidity
	p := strings.Split(strings.TrimSpace(*arg), ",")
	if len(p) != 3 {
		panic("invalid args")
	}
	lower, _ := strconv.ParseUint(p[0], 10, 64)
	upper, _ := strconv.ParseUint(p[1], 10, 64)
	liq, _ := strconv.ParseUint(p[2], 10, 64)

	env := sdk.GetEnv()
	updatePositionOwed(env.Sender.Address, lower, upper)
	curL := v3getU(v3posKey(env.Sender.Address, lower, upper, "liquidity"))
	if liq == 0 || liq > curL {
		panic("bad liq")
	}
	// Accrue underlying owed for removed liquidity at current price
	sqrtP := v3getU(v3KeySqrtP)
	owed0, owed1 := amountOwedFromLiquidity(liq, lower, upper, sqrtP)
	if owed0 > 0 {
		cur := v3getU(v3posKey(env.Sender.Address, lower, upper, "owed0"))
		v3setU(v3posKey(env.Sender.Address, lower, upper, "owed0"), cur+owed0)
	}
	if owed1 > 0 {
		cur := v3getU(v3posKey(env.Sender.Address, lower, upper, "owed1"))
		v3setU(v3posKey(env.Sender.Address, lower, upper, "owed1"), cur+owed1)
	}
	v3setU(v3posKey(env.Sender.Address, lower, upper, "liquidity"), curL-liq)
	v3setU(v3KeyLiquidity, v3getU(v3KeyLiquidity)-liq)
	return nil
}

//go:wasmexport collect_v3
func CollectV3(arg *string) *string {
	// args: lower_q32,upper_q32
	p := strings.Split(strings.TrimSpace(*arg), ",")
	if len(p) != 2 {
		panic("invalid args")
	}
	lower, _ := strconv.ParseUint(p[0], 10, 64)
	upper, _ := strconv.ParseUint(p[1], 10, 64)
	env := sdk.GetEnv()
	updatePositionOwed(env.Sender.Address, lower, upper)
	owed0 := v3getU(v3posKey(env.Sender.Address, lower, upper, "owed0"))
	owed1 := v3getU(v3posKey(env.Sender.Address, lower, upper, "owed1"))
	if owed0 > 0 {
		v3setU(v3posKey(env.Sender.Address, lower, upper, "owed0"), 0)
		a0, _ := getAssetsV3()
		sdk.HiveTransfer(env.Sender.Address, int64(owed0), a0)
	}
	if owed1 > 0 {
		v3setU(v3posKey(env.Sender.Address, lower, upper, "owed1"), 0)
		_, a1 := getAssetsV3()
		sdk.HiveTransfer(env.Sender.Address, int64(owed1), a1)
	}
	return nil
}

// Governance utilities
func isSystemSender() bool {
	env := sdk.GetEnv()
	return env.Sender.Address.Domain() == sdk.AddressDomainSystem
}

//go:wasmexport set_fee_v3
func SetFeeV3(arg *string) *string {
	if !isSystemSender() {
		panic("only system")
	}
	v, _ := strconv.ParseUint(strings.TrimSpace(*arg), 10, 64)
	if v > 10_000 {
		panic("bad bps")
	}
	v3setU(v3KeyFeeBps, v)
	return nil
}

//go:wasmexport set_active_range_v3
func SetActiveRangeV3(arg *string) *string {
	if !isSystemSender() {
		panic("only system")
	}
	p := strings.Split(strings.TrimSpace(*arg), ",")
	if len(p) != 2 {
		panic("invalid args")
	}
	lower, _ := strconv.ParseUint(p[0], 10, 64)
	upper, _ := strconv.ParseUint(p[1], 10, 64)
	sqrtP := v3getU(v3KeySqrtP)
	if !(lower < sqrtP && sqrtP < upper) {
		panic("price not within new range")
	}
	v3setU(v3KeyActiveLower, lower)
	v3setU(v3KeyActiveUpper, upper)
	return nil
}

//go:wasmexport swap_v3
func SwapV3(arg *string) *string {
	// args: dir,amountIn  dir in {0to1,1to0}
	p := strings.Split(strings.TrimSpace(*arg), ",")
	if len(p) != 2 {
		panic("invalid args")
	}
	dir := p[0]
	amtIn, _ := strconv.ParseUint(p[1], 10, 64)
	feeBps := v3getU(v3KeyFeeBps)
	sqrtP := v3getU(v3KeySqrtP)
	L := v3getU(v3KeyLiquidity)
	if L == 0 {
		panic("no liquidity")
	}
	lower := v3getU(v3KeyActiveLower)
	upper := v3getU(v3KeyActiveUpper)

	fee := amtIn * feeBps / 10_000
	eff := amtIn - fee
	// distribute fee via fee growth per liquidity
	if dir == "0to1" {
		fg0 := v3getU(v3KeyFeeGrowth0) + ((fee << qShift) / L)
		v3setU(v3KeyFeeGrowth0, fg0)
	} else if dir == "1to0" {
		fg1 := v3getU(v3KeyFeeGrowth1) + ((fee << qShift) / L)
		v3setU(v3KeyFeeGrowth1, fg1)
	} else {
		panic("dir")
	}

	var newSqrt uint64
	var out uint64
	if dir == "0to1" {
		// sqrt' = 1 / (1/sqrt + dx/L)
		inv := qDiv(1<<qShift, sqrtP)
		invPlus := inv + qDiv(eff<<qShift, L)
		newSqrt = qDiv(1<<qShift, invPlus)
		if newSqrt < lower {
			newSqrt = lower
		}
		// dy = L * (sqrt - sqrt')
		diff := sqrtP - newSqrt
		out = (L * diff) >> qShift
		v3setU(v3KeySqrtP, newSqrt)
		a0, a1 := getAssetsV3()
		// draw in
		sdk.HiveDraw(int64(amtIn), a0)
		// send out
		sdk.HiveTransfer(sdk.GetEnv().Sender.Address, int64(out), a1)
		v3setU(v3KeyFeeAcc0, v3getU(v3KeyFeeAcc0)+fee)
	} else {
		// sqrt' = sqrt + dy/L
		inc := qDiv(eff<<qShift, L)
		newSqrt = sqrtP + inc
		if newSqrt > upper {
			newSqrt = upper
		}
		// dx = L * (1/sqrt' - 1/sqrt)
		invNew := qDiv(1<<qShift, newSqrt)
		invOld := qDiv(1<<qShift, sqrtP)
		out = (L * (invOld - invNew)) >> qShift
		v3setU(v3KeySqrtP, newSqrt)
		a0, a1 := getAssetsV3()
		sdk.HiveDraw(int64(amtIn), a1)
		sdk.HiveTransfer(sdk.GetEnv().Sender.Address, int64(out), a0)
		v3setU(v3KeyFeeAcc1, v3getU(v3KeyFeeAcc1)+fee)
	}
	return nil
}
