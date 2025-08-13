package main

import (
	"contract-template/sdk"
	_ "contract-template/sdk"
	"strconv"
	"strings"
)

func main() {}

// Keys
const (
	keyAsset0            = "pool/asset0"
	keyAsset1            = "pool/asset1"
	keyReserve0          = "pool/reserve0"
	keyReserve1          = "pool/reserve1"
	keyFee0              = "pool/fee0"
	keyFee1              = "pool/fee1"
	keyFeeLastClaimUnix  = "pool/fee_last_claim"
	keyBaseFeeBps        = "pool/base_fee_bps"
	keyFeeClaimIntervalS = "pool/fee_claim_interval_s"
	keyTotalLP           = "pool/total_lp"
	keyLPPrefix          = "lps/" // lps/<address>
)

const (
	defaultBaseFeeBps        = 8     // 0.08%
	defaultFeeClaimIntervalS = 86400 // 1 day
)

// Utilities
func mustParseUint(s string) uint64 {
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

func mustParseInt(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func getStr(key string) string {
	v := sdk.StateGetObject(key)
	if v == nil {
		return ""
	}
	return *v
}

func setStr(key string, val string) {
	sdk.StateSetObject(key, val)
}

func getUint(key string) uint64 {
	v := sdk.StateGetObject(key)
	if v == nil {
		return 0
	}
	n, _ := strconv.ParseUint(*v, 10, 64)
	return n
}

func setUint(key string, val uint64) {
	sdk.StateSetObject(key, strconv.FormatUint(val, 10))
}

func getInt(key string) int64 {
	v := sdk.StateGetObject(key)
	if v == nil {
		return 0
	}
	n, _ := strconv.ParseInt(*v, 10, 64)
	return n
}

func setInt(key string, val int64) {
	sdk.StateSetObject(key, strconv.FormatInt(val, 10))
}

func min64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func sqrt64(x uint64) uint64 {
	if x == 0 {
		return 0
	}
	// Integer sqrt
	z := x
	y := (z + 1) / 2
	for y < z {
		z = y
		y = (y + x/y) / 2
	}
	return z
}

func lpKey(addr sdk.Address) string {
	return keyLPPrefix + addr.String()
}

func assert(cond bool) {
	if !cond {
		panic("assertion failed")
	}
}

func isSystemSender() bool {
	env := sdk.GetEnv()
	return env.Sender.Address.Domain() == sdk.AddressDomainSystem
}

func getAssets() (sdk.Asset, sdk.Asset) {
	a0 := getStr(keyAsset0)
	a1 := getStr(keyAsset1)
	return sdk.Asset(a0), sdk.Asset(a1)
}

// State helpers for LP balances
func getLP(addr sdk.Address) uint64 {
	v := sdk.StateGetObject(lpKey(addr))
	if v == nil {
		return 0
	}
	n, _ := strconv.ParseUint(*v, 10, 64)
	return n
}

func setLP(addr sdk.Address, amount uint64) {
	setUint(lpKey(addr), amount)
}

// Contract initialization
// Payload: "asset0,asset1,baseFeeBps(optional)" e.g. "hbd,hive,8"
//
//go:wasmexport init
func Init(payload *string) *string {
	parts := strings.Split(strings.TrimSpace(*payload), ",")
	assert(len(parts) >= 2)

	// Do not read before write: set unconditionally
	setStr(keyAsset0, parts[0])
	setStr(keyAsset1, parts[1])

	base := uint64(defaultBaseFeeBps)
	if len(parts) >= 3 && parts[2] != "" {
		if v, err := strconv.ParseUint(parts[2], 10, 64); err == nil {
			base = v
		}
	}
	setUint(keyBaseFeeBps, base)
	setUint(keyTotalLP, 0)
	setInt(keyReserve0, 0)
	setInt(keyReserve1, 0)
	setInt(keyFee0, 0)
	setInt(keyFee1, 0)
	setUint(keyFeeClaimIntervalS, defaultFeeClaimIntervalS)
	setStr(keyFeeLastClaimUnix, sdk.GetEnv().Timestamp)

	return nil
}

// Add liquidity
// Payload: "amt0,amt1"
//
//go:wasmexport add_liquidity
func AddLiquidity(payload *string) *string {
	params := strings.Split(strings.TrimSpace(*payload), ",")
	assert(len(params) == 2)
	amt0U, _ := strconv.ParseUint(params[0], 10, 64)
	amt1U, _ := strconv.ParseUint(params[1], 10, 64)

	asset0, asset1 := getAssets()
	// Pull funds from user intents into contract
	if amt0U > 0 {
		sdk.HiveDraw(int64(amt0U), asset0)
	}
	if amt1U > 0 {
		sdk.HiveDraw(int64(amt1U), asset1)
	}

	// Update reserves and mint LP
	r0 := uint64(getInt(keyReserve0))
	r1 := uint64(getInt(keyReserve1))
	totalLP := getUint(keyTotalLP)

	var minted uint64
	if totalLP == 0 {
		// geometric mean
		minted = sqrt64(amt0U * amt1U)
	} else {
		// proportional
		m0 := amt0U * totalLP / r0
		m1 := amt1U * totalLP / r1
		minted = min64(m0, m1)
	}
	assert(minted > 0)

	env := sdk.GetEnv()
	if totalLP == 0 {
		setLP(env.Sender.Address, minted)
	} else {
		setLP(env.Sender.Address, getLP(env.Sender.Address)+minted)
	}
	setUint(keyTotalLP, totalLP+minted)
	setInt(keyReserve0, int64(r0+amt0U))
	setInt(keyReserve1, int64(r1+amt1U))

	return nil
}

// Remove liquidity
// Payload: "lpAmount"
//
//go:wasmexport remove_liquidity
func RemoveLiquidity(payload *string) *string {
	lpToBurnU, _ := strconv.ParseUint(strings.TrimSpace(*payload), 10, 64)
	env := sdk.GetEnv()
	userLP := getLP(env.Sender.Address)
	totalLP := getUint(keyTotalLP)
	assert(lpToBurnU > 0 && lpToBurnU <= userLP && totalLP > 0)

	r0 := uint64(getInt(keyReserve0))
	r1 := uint64(getInt(keyReserve1))

	amt0 := int64(r0 * lpToBurnU / totalLP)
	amt1 := int64(r1 * lpToBurnU / totalLP)

	// book-keep first
	setLP(env.Sender.Address, userLP-lpToBurnU)
	setUint(keyTotalLP, totalLP-lpToBurnU)
	setInt(keyReserve0, int64(r0)-amt0)
	setInt(keyReserve1, int64(r1)-amt1)

	// transfer out
	asset0, asset1 := getAssets()
	if amt0 > 0 {
		sdk.HiveTransfer(env.Sender.Address, amt0, asset0)
	}
	if amt1 > 0 {
		sdk.HiveTransfer(env.Sender.Address, amt1, asset1)
	}
	return nil
}

// Swap
// Payload: "dir,amountIn" where dir is "0to1" or "1to0"
//
//go:wasmexport swap
func Swap(payload *string) *string {
	parts := strings.Split(strings.TrimSpace(*payload), ",")
	assert(len(parts) == 2)
	dir := parts[0]
	amountInU, _ := strconv.ParseUint(parts[1], 10, 64)
	assert(amountInU > 0)

	feeBps := getUint(keyBaseFeeBps)
	feeNumer := (10_000 - feeBps)

	r0 := uint64(getInt(keyReserve0))
	r1 := uint64(getInt(keyReserve1))
	asset0, asset1 := getAssets()

	if dir == "0to1" {
		// draw asset0
		sdk.HiveDraw(int64(amountInU), asset0)
		// apply fee on input
		dxEff := amountInU * feeNumer / 10_000
		// constant product x*y=k, output dy = r1 - k/(r0+dxEff)
		k := r0 * r1
		newX := r0 + dxEff
		assert(newX > 0)
		dy := r1 - (k / newX)
		assert(dy > 0 && dy < r1)

		// update reserves: only effective input increases reserve
		setInt(keyReserve0, int64(r0+dxEff))
		setInt(keyReserve1, int64(r1-uint64(dy)))
		// accrue fee (kept separate from reserves)
		fee := int64(amountInU - dxEff)
		setInt(keyFee0, getInt(keyFee0)+fee)
		// send out asset1
		sdk.HiveTransfer(sdk.GetEnv().Sender.Address, int64(dy), asset1)
	} else if dir == "1to0" {
		sdk.HiveDraw(int64(amountInU), asset1)
		dxEff := amountInU * feeNumer / 10_000
		k := r0 * r1
		newY := r1 + dxEff
		assert(newY > 0)
		dxOut := r0 - (k / newY)
		assert(dxOut > 0 && dxOut < r0)

		// only effective input increases reserve
		setInt(keyReserve1, int64(r1+dxEff))
		setInt(keyReserve0, int64(r0-uint64(dxOut)))
		fee := int64(amountInU - dxEff)
		setInt(keyFee1, getInt(keyFee1)+fee)
		sdk.HiveTransfer(sdk.GetEnv().Sender.Address, int64(dxOut), asset0)
	} else {
		assert(false)
	}
	return nil
}

// Donate liquidity (no LP minted)
// Payload: "amt0,amt1"
//
//go:wasmexport donate
func Donate(payload *string) *string {
	params := strings.Split(strings.TrimSpace(*payload), ",")
	assert(len(params) == 2)
	amt0U, _ := strconv.ParseUint(params[0], 10, 64)
	amt1U, _ := strconv.ParseUint(params[1], 10, 64)
	a0, a1 := getAssets()
	if amt0U > 0 {
		sdk.HiveDraw(int64(amt0U), a0)
		setInt(keyReserve0, getInt(keyReserve0)+int64(amt0U))
	}
	if amt1U > 0 {
		sdk.HiveDraw(int64(amt1U), a1)
		setInt(keyReserve1, getInt(keyReserve1)+int64(amt1U))
	}
	return nil
}

// Claim reserve fees; send HBD fees to system account. Non-HBD conversion is left as a TODO.
//
//go:wasmexport claim_fees
func ClaimFees(_ *string) *string {
	systemFR := sdk.Address("system:fr_balance")
	a0, a1 := getAssets()
	f0 := getInt(keyFee0)
	f1 := getInt(keyFee1)
	if f0 > 0 && a0 == sdk.AssetHbd {
		setInt(keyFee0, 0)
		sdk.HiveTransfer(systemFR, f0, a0)
	}
	if f1 > 0 && a1 == sdk.AssetHbd {
		setInt(keyFee1, 0)
		sdk.HiveTransfer(systemFR, f1, a1)
	}
	// Note: non-HBD conversion to HBD requires router; omitted here.
	setStr(keyFeeLastClaimUnix, sdk.GetEnv().Timestamp)
	return nil
}

// Burn LP balances (permanently reduces total LP, locking proportion of reserves)
// Payload: "lpAmount"
//
//go:wasmexport burn
func Burn(payload *string) *string {
	amt, _ := strconv.ParseUint(strings.TrimSpace(*payload), 10, 64)
	env := sdk.GetEnv()
	bal := getLP(env.Sender.Address)
	assert(amt > 0 && amt <= bal)
	setLP(env.Sender.Address, bal-amt)
	setUint(keyTotalLP, getUint(keyTotalLP)-amt)
	// reserves unchanged
	return nil
}

// Transfer LP tokens to another address
// Payload: "toAddress,amount"
//
//go:wasmexport transfer
func Transfer(payload *string) *string {
	parts := strings.Split(strings.TrimSpace(*payload), ",")
	assert(len(parts) == 2)
	to := sdk.Address(parts[0])
	amt, _ := strconv.ParseUint(parts[1], 10, 64)
	env := sdk.GetEnv()
	fromBal := getLP(env.Sender.Address)
	assert(amt > 0 && amt <= fromBal)
	setLP(env.Sender.Address, fromBal-amt)
	setLP(to, getLP(to)+amt)
	return nil
}

// Safety interface: consensus-only emergency withdrawal by burning LP
// Payload: "lpAmount"
//
//go:wasmexport si_withdraw
func SIWithdraw(payload *string) *string {
	assert(isSystemSender())
	// burn from all LP proportionally is complex; here we burn from caller-specified LP (system must specify address and amount)
	// For simplicity, we accept "address,lpAmount" here.
	parts := strings.Split(strings.TrimSpace(*payload), ",")
	assert(len(parts) == 2)
	addr := sdk.Address(parts[0])
	amt, _ := strconv.ParseUint(parts[1], 10, 64)

	totalLP := getUint(keyTotalLP)
	bal := getLP(addr)
	assert(amt > 0 && amt <= bal && totalLP > 0)

	r0 := uint64(getInt(keyReserve0))
	r1 := uint64(getInt(keyReserve1))
	a0, a1 := getAssets()

	out0 := int64(r0 * amt / totalLP)
	out1 := int64(r1 * amt / totalLP)

	setLP(addr, bal-amt)
	setUint(keyTotalLP, totalLP-amt)
	setInt(keyReserve0, int64(r0)-out0)
	setInt(keyReserve1, int64(r1)-out1)

	// return to provider
	if out0 > 0 {
		sdk.HiveTransfer(addr, out0, a0)
	}
	if out1 > 0 {
		sdk.HiveTransfer(addr, out1, a1)
	}
	return nil
}

// System function: set base fee (bps). Consensus-only.
// Payload: "newBps"
//
//go:wasmexport set_base_fee
func SetBaseFee(payload *string) *string {
	assert(isSystemSender())
	v, _ := strconv.ParseUint(strings.TrimSpace(*payload), 10, 64)
	assert(v <= 10_000)
	setUint(keyBaseFeeBps, v)
	return nil
}
