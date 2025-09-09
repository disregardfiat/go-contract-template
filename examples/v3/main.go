package main

import (
	"contract-template/sdk"
	"math/big"
	"strconv"
	"strings"
)

// Minimal v3-style AMM with a single active price range and per-position fee growth snapshots.

//go:wasmexport init
func Init(arg *string) *string {
	// args: asset0,asset1,base_fee_bps,init_sqrt_q32,active_lower_q32,active_upper_q32
	p := strings.Split(strings.TrimSpace(*arg), ",")
	if len(p) < 6 {
		sdk.Abort("invalid args")
	}
	setStr(KeyAsset0, p[0])
	setStr(KeyAsset1, p[1])
	baseFeeBps, err := strconv.ParseUint(p[2], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}
	setU(KeyFeeBps, baseFeeBps)
	sqrtP, err := strconv.ParseUint(p[3], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}
	setU(KeySqrtP, sqrtP)
	lower, err := strconv.ParseUint(p[4], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}
	upper, err := strconv.ParseUint(p[5], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}
	if lower >= upper || !(lower < sqrtP && sqrtP < upper) {
		sdk.Abort("invalid range or price")
	}
	setU(KeyActiveLower, lower)
	setU(KeyActiveUpper, upper)
	setU(KeyLiquidity, 0)
	setU(KeyFeeGrowth0, 0)
	setU(KeyFeeGrowth1, 0)
	return nil
}

//go:wasmexport mint
func Mint(arg *string) *string {
	//RequireNotPaused()
	// args: lower_q32,upper_q32,max_amount0,max_amount1
	p := strings.Split(strings.TrimSpace(*arg), ",")
	if len(p) != 4 {
		sdk.Abort("invalid args")
	}
	lower, err := strconv.ParseUint(p[0], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}
	upper, err := strconv.ParseUint(p[1], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}
	maxAmt0, err := strconv.ParseUint(p[2], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}
	maxAmt1, err := strconv.ParseUint(p[3], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}

	// enforce single active range for now
	if lower != getU(KeyActiveLower) || upper != getU(KeyActiveUpper) {
		sdk.Abort("range must equal active range")
	}

	sqrtP := getU(KeySqrtP)
	L0 := getLiquidityForAmount0(sqrtP, upper, maxAmt0)
	L1 := getLiquidityForAmount1(lower, sqrtP, maxAmt1)
	L := minU64(L0, L1)
	if L == 0 {
		sdk.Abort("zero L")
	}

	req0, req1 := amountOwedFromLiquidity(L, lower, upper, sqrtP)
	if req0 > maxAmt0 || req1 > maxAmt1 {
		sdk.Abort("slippage")
	}

	a0, a1 := getAssets()
	if req0 > 0 {
		sdk.HiveDraw(int64(req0), a0)
	}
	if req1 > 0 {
		sdk.HiveDraw(int64(req1), a1)
	}

	env := sdk.GetEnv()
	updatePositionOwed(env.Sender.Address, lower, upper)
	curL := getU(posKey(env.Sender.Address, lower, upper, "liquidity"))
	setU(posKey(env.Sender.Address, lower, upper, "liquidity"), curL+L)
	setU(posKey(env.Sender.Address, lower, upper, "fg0_last"), getU(KeyFeeGrowth0))
	setU(posKey(env.Sender.Address, lower, upper, "fg1_last"), getU(KeyFeeGrowth1))

	totalL := getU(KeyLiquidity)
	setU(KeyLiquidity, totalL+L)
	return nil
}

//go:wasmexport burn
func Burn(arg *string) *string {
	//RequireNotPaused()
	// args: lower_q32,upper_q32,liquidity
	p := strings.Split(strings.TrimSpace(*arg), ",")
	if len(p) != 3 {
		sdk.Abort("invalid args")
	}
	lower, err := strconv.ParseUint(p[0], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}
	upper, err := strconv.ParseUint(p[1], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}
	liq, err := strconv.ParseUint(p[2], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}

	env := sdk.GetEnv()
	updatePositionOwed(env.Sender.Address, lower, upper)
	curL := getU(posKey(env.Sender.Address, lower, upper, "liquidity"))
	if liq == 0 || liq > curL {
		sdk.Abort("bad liq")
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
	totalL := getU(KeyLiquidity)
	if totalL < liq {
		sdk.Abort("total L underflow")
	}
	setU(KeyLiquidity, totalL-liq)
	return nil
}

//go:wasmexport collect
func Collect(arg *string) *string {
	//RequireNotPaused()
	// args: lower_q32,upper_q32
	p := strings.Split(strings.TrimSpace(*arg), ",")
	if len(p) != 2 {
		sdk.Abort("invalid args")
	}
	lower, err := strconv.ParseUint(p[0], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}
	upper, err := strconv.ParseUint(p[1], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}
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

//go:wasmexport set_fee
func SetFee(arg *string) *string {
	if !isSystemSender() {
		sdk.Abort("only system")
	}
	v, err := strconv.ParseUint(strings.TrimSpace(*arg), 10, 64)
	if err != nil || v > 10_000 {
		sdk.Abort("bad bps")
	}
	setU(KeyFeeBps, v)
	return nil
}

//go:wasmexport set_active_range
func SetActiveRange(arg *string) *string {
	if !isSystemSender() {
		sdk.Abort("only system")
	}
	p := strings.Split(strings.TrimSpace(*arg), ",")
	if len(p) != 2 {
		sdk.Abort("invalid args")
	}
	lower, err := strconv.ParseUint(p[0], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}
	upper, err := strconv.ParseUint(p[1], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}
	if lower >= upper {
		sdk.Abort("invalid range")
	}
	sqrtP := getU(KeySqrtP)
	if !(lower < sqrtP && sqrtP < upper) {
		sdk.Abort("price not within new range")
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
		sdk.Abort("invalid args")
	}
	dir := p[0]
	amtIn, err := strconv.ParseUint(p[1], 10, 64)
	if err != nil {
		sdk.Abort("parse error")
	}
	minOut := uint64(0)
	if len(p) == 3 && p[2] != "" {
		m, err := strconv.ParseUint(p[2], 10, 64)
		if err != nil {
			sdk.Abort("parse error")
		}
		minOut = m
	}
	//RequireNotPaused()
	feeBps := getU(KeyFeeBps)
	sqrtP := getU(KeySqrtP)
	L := getU(KeyLiquidity)
	if L == 0 {
		sdk.Abort("no liquidity")
	}
	if sqrtP == 0 {
		sdk.Abort("bad price")
	}
	lower := getU(KeyActiveLower)
	upper := getU(KeyActiveUpper)

	fee := amtIn * feeBps / 10_000
	if fee >= amtIn {
		sdk.Abort("fee >= in")
	}
	eff := amtIn - fee
	// distribute fee via fee growth per liquidity
	if dir == "0to1" {
		fg0 := getU(KeyFeeGrowth0)
		var feeBi big.Int
		feeBi.SetUint64(fee)
		feeBi.Lsh(&feeBi, uint(qShift))
		feeBi.Div(&feeBi, new(big.Int).SetUint64(L))
		if feeBi.BitLen() > 64 {
			sdk.Abort("fg overflow")
		}
		fg0 += feeBi.Uint64()
		setU(KeyFeeGrowth0, fg0)
	} else if dir == "1to0" {
		fg1 := getU(KeyFeeGrowth1)
		var feeBi big.Int
		feeBi.SetUint64(fee)
		feeBi.Lsh(&feeBi, uint(qShift))
		feeBi.Div(&feeBi, new(big.Int).SetUint64(L))
		if feeBi.BitLen() > 64 {
			sdk.Abort("fg overflow")
		}
		fg1 += feeBi.Uint64()
		setU(KeyFeeGrowth1, fg1)
	} else {
		sdk.Abort("dir")
	}

	var newSqrt uint64
	var out uint64
	if dir == "0to1" {
		// sqrt' = 1 / (1/sqrt + dx/L)
		inv := qDiv(1<<qShift, sqrtP)
		var addBi big.Int
		addBi.SetUint64(eff)
		addBi.Lsh(&addBi, uint(qShift))
		addBi.Div(&addBi, new(big.Int).SetUint64(L))
		if addBi.BitLen() > 64 {
			sdk.Abort("add overflow")
		}
		invPlus := inv + addBi.Uint64()
		newSqrt = qDiv(1<<qShift, invPlus)
		if newSqrt < lower {
			newSqrt = lower
		}
		// dy = L * (sqrt - sqrt')
		diff := sqrtP - newSqrt
		var outBi big.Int
		outBi.SetUint64(L)
		outBi.Mul(&outBi, new(big.Int).SetUint64(diff))
		outBi.Rsh(&outBi, uint(qShift))
		if outBi.BitLen() > 64 {
			sdk.Abort("out overflow")
		}
		out = outBi.Uint64()
		setU(KeySqrtP, newSqrt)
		a0, a1 := getAssets()
		// draw in
		sdk.HiveDraw(int64(amtIn), a0)
		// send out
		if out < minOut {
			sdk.Abort("slippage")
		}
		sdk.HiveTransfer(sdk.GetEnv().Sender.Address, int64(out), a1)
	} else {
		// sqrt' = sqrt + dy/L
		var incBi big.Int
		incBi.SetUint64(eff)
		incBi.Lsh(&incBi, uint(qShift))
		incBi.Div(&incBi, new(big.Int).SetUint64(L))
		if incBi.BitLen() > 64 {
			sdk.Abort("inc overflow")
		}
		inc := incBi.Uint64()
		newSqrt = sqrtP + inc
		if newSqrt > upper || newSqrt < sqrtP { // overflow check
			newSqrt = upper
		}
		// dx = L * (1/sqrt' - 1/sqrt)
		invNew := qDiv(1<<qShift, newSqrt)
		invOld := qDiv(1<<qShift, sqrtP)
		diff := invOld - invNew
		var outBi big.Int
		outBi.SetUint64(L)
		outBi.Mul(&outBi, new(big.Int).SetUint64(diff))
		outBi.Rsh(&outBi, uint(qShift))
		if outBi.BitLen() > 64 {
			sdk.Abort("out overflow")
		}
		out = outBi.Uint64()
		setU(KeySqrtP, newSqrt)
		a0, a1 := getAssets()
		sdk.HiveDraw(int64(amtIn), a1)
		if out < minOut {
			sdk.Abort("slippage")
		}
		sdk.HiveTransfer(sdk.GetEnv().Sender.Address, int64(out), a0)
	}
	return nil
}

//go:wasmexport format_number
func FormatNumber(arg *string) *string {
	// Accept a single number (decimal or 0x-hex). Validate it fits in 256 bits.
	// Return it formatted in the same base and width as received (preserving 0x and leading zeros).
	if arg == nil {
		msg := "nil arg"
		sdk.Abort(msg)
	}
	in := strings.TrimSpace(*arg)
	bi, fmt, ok := parseNumberWithFormat(in)
	if !ok {
		sdk.Abort("bad number")
	}
	if bi.BitLen() > 256 {
		sdk.Abort("number > 256 bits")
	}
	out := formatNumberWithFormat(bi, fmt)
	return &out
}
