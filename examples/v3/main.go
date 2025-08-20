package main

import (
	"contract-template/sdk"
	"math/bits"
	"strconv"
	"strings"
)

// Minimal v3-style AMM with a single active price range and per-position fee growth snapshots.

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
	KeyFeeAcc0     = "fee_acc0"
	KeyFeeAcc1     = "fee_acc1"
	KeyPaused      = "paused"
)

const qShift = 32

func qMul(a, b uint64) uint64 {
	// (a*b)>>qShift using 128-bit multiply
	hi, lo := bits.Mul64(a, b)
	if qShift == 0 {
		return lo
	}
	return (hi << qShift) | (lo >> qShift)
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
	n, _ := strconv.ParseUint(s, 10, 64)
	return n
}
func setU(key string, v uint64) { sdk.StateSetObject(key, strconv.FormatUint(v, 10)) }
func posKey(addr sdk.Address, lower, upper uint64, suffix string) string {
	return "pos/" + addr.String() + "/" + strconv.FormatUint(lower, 10) + "/" + strconv.FormatUint(upper, 10) + "/" + suffix
}

//go:wasmexport init
func Init(arg *string) *string {
	// args: asset0,asset1,fee_bps,init_sqrt_q32,active_lower_q32,active_upper_q32
	p := strings.Split(strings.TrimSpace(*arg), ",")
	if len(p) < 6 {
		panic("invalid args")
	}
	setStr(KeyAsset0, p[0])
	setStr(KeyAsset1, p[1])
	feeBps, _ := strconv.ParseUint(p[2], 10, 64)
	setU(KeyFeeBps, feeBps)
	sqrtP, _ := strconv.ParseUint(p[3], 10, 64)
	setU(KeySqrtP, sqrtP)
	lower, _ := strconv.ParseUint(p[4], 10, 64)
	upper, _ := strconv.ParseUint(p[5], 10, 64)
	if !(lower < sqrtP && sqrtP < upper) {
		panic("price not within active range")
	}
	setU(KeyActiveLower, lower)
	setU(KeyActiveUpper, upper)
	setU(KeyLiquidity, 0)
	setU(KeyFeeGrowth0, 0)
	setU(KeyFeeGrowth1, 0)
	setU(KeyFeeAcc0, 0)
	setU(KeyFeeAcc1, 0)
	setU(KeyPaused, 0)
	return nil
}

func getAssets() (sdk.Asset, sdk.Asset) {
	return sdk.Asset(getStr(KeyAsset0)), sdk.Asset(getStr(KeyAsset1))
}

func RequireNotPaused() {
	if getU(KeyPaused) != 0 {
		panic("paused")
	}
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
		owed := getU(posKey(owner, lower, upper, "owed0"))
		owed += (delta * L) >> qShift
		setU(posKey(owner, lower, upper, "owed0"), owed)
		setU(posKey(owner, lower, upper, "fg0_last"), fg0)
	}
	if fg1 > last1 {
		delta := fg1 - last1
		owed := getU(posKey(owner, lower, upper, "owed1"))
		owed += (delta * L) >> qShift
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

//go:wasmexport mint
func Mint(arg *string) *string {
	RequireNotPaused()
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
	if lower != getU(KeyActiveLower) || upper != getU(KeyActiveUpper) {
		panic("range must equal active range")
	}

	sqrtP := getU(KeySqrtP)
	L0 := getLiquidityForAmount0(sqrtP, upper, amt0)
	L1 := getLiquidityForAmount1(lower, sqrtP, amt1)
	L := minU64(L0, L1)
	if L == 0 {
		panic("zero L")
	}

	a0, a1 := getAssets()
	if amt0 > 0 {
		sdk.HiveDraw(int64(amt0), a0)
	}
	if amt1 > 0 {
		sdk.HiveDraw(int64(amt1), a1)
	}

	env := sdk.GetEnv()
	updatePositionOwed(env.Sender.Address, lower, upper)
	curL := getU(posKey(env.Sender.Address, lower, upper, "liquidity"))
	setU(posKey(env.Sender.Address, lower, upper, "liquidity"), curL+L)
	setU(posKey(env.Sender.Address, lower, upper, "fg0_last"), getU(KeyFeeGrowth0))
	setU(posKey(env.Sender.Address, lower, upper, "fg1_last"), getU(KeyFeeGrowth1))

	setU(KeyLiquidity, getU(KeyLiquidity)+L)
	return nil
}

//go:wasmexport burn
func Burn(arg *string) *string {
	RequireNotPaused()
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
	curL := getU(posKey(env.Sender.Address, lower, upper, "liquidity"))
	if liq == 0 || liq > curL {
		panic("bad liq")
	}
	// Accrue underlying owed for removed liquidity at current price
	sqrtP := getU(KeySqrtP)
	owed0, owed1 := amountOwedFromLiquidity(liq, lower, upper, sqrtP)
	if owed0 > 0 {
		cur := getU(posKey(env.Sender.Address, lower, upper, "owed0"))
		setU(posKey(env.Sender.Address, lower, upper, "owed0"), cur+owed0)
	}
	if owed1 > 0 {
		cur := getU(posKey(env.Sender.Address, lower, upper, "owed1"))
		setU(posKey(env.Sender.Address, lower, upper, "owed1"), cur+owed1)
	}
	setU(posKey(env.Sender.Address, lower, upper, "liquidity"), curL-liq)
	setU(KeyLiquidity, getU(KeyLiquidity)-liq)
	return nil
}

//go:wasmexport collect
func Collect(arg *string) *string {
	RequireNotPaused()
	// args: lower_q32,upper_q32
	p := strings.Split(strings.TrimSpace(*arg), ",")
	if len(p) != 2 {
		panic("invalid args")
	}
	lower, _ := strconv.ParseUint(p[0], 10, 64)
	upper, _ := strconv.ParseUint(p[1], 10, 64)
	env := sdk.GetEnv()
	updatePositionOwed(env.Sender.Address, lower, upper)
	owed0 := getU(posKey(env.Sender.Address, lower, upper, "owed0"))
	owed1 := getU(posKey(env.Sender.Address, lower, upper, "owed1"))
	if owed0 > 0 {
		setU(posKey(env.Sender.Address, lower, upper, "owed0"), 0)
		a0, _ := getAssets()
		sdk.HiveTransfer(env.Sender.Address, int64(owed0), a0)
	}
	if owed1 > 0 {
		setU(posKey(env.Sender.Address, lower, upper, "owed1"), 0)
		_, a1 := getAssets()
		sdk.HiveTransfer(env.Sender.Address, int64(owed1), a1)
	}
	return nil
}

// Governance utilities
func isSystemSender() bool {
	env := sdk.GetEnv()
	return env.Sender.Address.Domain() == sdk.AddressDomainSystem
}

//go:wasmexport set_fee
func SetFee(arg *string) *string {
	if !isSystemSender() {
		panic("only system")
	}
	v, _ := strconv.ParseUint(strings.TrimSpace(*arg), 10, 64)
	if v > 10_000 {
		panic("bad bps")
	}
	setU(KeyFeeBps, v)
	return nil
}

//go:wasmexport set_active_range
func SetActiveRange(arg *string) *string {
	if !isSystemSender() {
		panic("only system")
	}
	p := strings.Split(strings.TrimSpace(*arg), ",")
	if len(p) != 2 {
		panic("invalid args")
	}
	lower, _ := strconv.ParseUint(p[0], 10, 64)
	upper, _ := strconv.ParseUint(p[1], 10, 64)
	sqrtP := getU(KeySqrtP)
	if !(lower < sqrtP && sqrtP < upper) {
		panic("price not within new range")
	}
	setU(KeyActiveLower, lower)
	setU(KeyActiveUpper, upper)
	return nil
}

//go:wasmexport swap
func Swap(arg *string) *string {
	// args: dir,amountIn(,minOut)
	p := strings.Split(strings.TrimSpace(*arg), ",")
	if len(p) != 2 && len(p) != 3 {
		panic("invalid args")
	}
	dir := p[0]
	amtIn, _ := strconv.ParseUint(p[1], 10, 64)
	minOut := uint64(0)
	if len(p) == 3 && p[2] != "" {
		m, _ := strconv.ParseUint(p[2], 10, 64)
		minOut = m
	}
	RequireNotPaused()
	feeBps := getU(KeyFeeBps)
	sqrtP := getU(KeySqrtP)
	L := getU(KeyLiquidity)
	if L == 0 {
		panic("no liquidity")
	}
	if sqrtP == 0 {
		panic("bad price")
	}
	lower := getU(KeyActiveLower)
	upper := getU(KeyActiveUpper)

	fee := amtIn * feeBps / 10_000
	eff := amtIn - fee
	// distribute fee via fee growth per liquidity
	if dir == "0to1" {
		fg0 := getU(KeyFeeGrowth0) + ((fee << qShift) / L)
		setU(KeyFeeGrowth0, fg0)
	} else if dir == "1to0" {
		fg1 := getU(KeyFeeGrowth1) + ((fee << qShift) / L)
		setU(KeyFeeGrowth1, fg1)
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
		setU(KeySqrtP, newSqrt)
		a0, a1 := getAssets()
		// draw in
		sdk.HiveDraw(int64(amtIn), a0)
		// send out
		if out < minOut {
			panic("slippage")
		}
		sdk.HiveTransfer(sdk.GetEnv().Sender.Address, int64(out), a1)
		setU(KeyFeeAcc0, getU(KeyFeeAcc0)+fee)
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
		setU(KeySqrtP, newSqrt)
		a0, a1 := getAssets()
		sdk.HiveDraw(int64(amtIn), a1)
		if out < minOut {
			panic("slippage")
		}
		sdk.HiveTransfer(sdk.GetEnv().Sender.Address, int64(out), a0)
		setU(KeyFeeAcc1, getU(KeyFeeAcc1)+fee)
	}
	return nil
}

//go:wasmexport set_paused
func SetPaused(arg *string) *string {
	if !isSystemSender() {
		panic("only system")
	}
	v, _ := strconv.ParseUint(strings.TrimSpace(*arg), 10, 64)
	if v != 0 && v != 1 {
		panic("bad pause")
	}
	setU(KeyPaused, v)
	return nil
}
