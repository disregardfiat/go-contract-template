package main

import (
	"contract-template/sdk"
	_ "contract-template/sdk"
	"math/bits"
	"strconv"
	"strings"
)

func main() {}

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
		base = parseUintStrict(parts[2])
	}
	setUint(keyBaseFeeBps, base)
	// default slip fee params
	setUint(keySlipBaselineBps, defaultSlipBaselineBps)
	setUint(keySlipShareBps, defaultSlipShareBps)
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
	amt0U := parseUintStrict(params[0])
	amt1U := parseUintStrict(params[1])

	asset0, asset1 := getAssets()
	// Pull funds from user intents into contract
	if amt0U > 0 {
		drawAsset(int64(amt0U), asset0)
	}
	if amt1U > 0 {
		drawAsset(int64(amt1U), asset1)
	}

	// Update reserves and mint LP
	r0 := uint64(getInt(keyReserve0))
	r1 := uint64(getInt(keyReserve1))
	totalLP := getUint(keyTotalLP)

	var minted uint64
	if totalLP == 0 {
		// geometric mean using 128-bit product
		hi, lo := bits.Mul64(amt0U, amt1U)
		minted = sqrt128(hi, lo)
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
		transferAsset(env.Sender.Address, amt0, asset0)
	}
	if amt1 > 0 {
		transferAsset(env.Sender.Address, amt1, asset1)
	}
	return nil
}

// Swap
// Payload: "dir,amountIn" where dir is "0to1" or "1to0"
// @todo: add MinAmount to receive
//
//go:wasmexport swap
func Swap(payload *string) *string {
	parts := strings.Split(strings.TrimSpace(*payload), ",")
	assert(len(parts) == 2 || len(parts) == 3 || len(parts) == 4 || len(parts) == 5)
	dir := parts[0]
	amountInU := parseUintStrict(parts[1])
	minOutU := uint64(0)
	var beneficiary sdk.Address
	refBpsU := uint64(0)
	if len(parts) == 3 {
		// legacy form: dir,amountIn,minOut
		if parts[2] != "" {
			minOutU = parseUintStrict(parts[2])
		}
	} else if len(parts) == 4 {
		// new form: dir,amountIn,beneficiary,refBps
		beneficiary = sdk.Address(parts[2])
		refBpsU = parseUintStrict(parts[3])
		assert(refBpsU >= 1 && refBpsU <= 1000)
	} else if len(parts) == 5 {
		// new form with minOut: dir,amountIn,minOut,beneficiary,refBps
		if parts[2] != "" {
			minOutU = parseUintStrict(parts[2])
		}
		beneficiary = sdk.Address(parts[3])
		refBpsU = parseUintStrict(parts[4])
		assert(refBpsU >= 1 && refBpsU <= 1000)
	}
	assert(amountInU > 0)

	feeBps := getUint(keyBaseFeeBps) // base fee
	baselineSlipBps := getUint(keySlipBaselineBps)
	shareSlipBps := getUint(keySlipShareBps)

	r0 := uint64(getInt(keyReserve0))
	r1 := uint64(getInt(keyReserve1))
	assert(r0 > 0 && r1 > 0)
	asset0, asset1 := getAssets()

	if dir == "0to1" {
		// input is asset0
		drawAsset(int64(amountInU), asset0)

		// base fee applies only if input is HBD
		dxEff := amountInU
		if isHbd(asset0) && feeBps > 0 {
			dxEff = amountInU * (10_000 - feeBps) / 10_000
		}
		if dxEff <= 0 {
			dxEff = 1
		}

		// constant product x*y=k, output dy = r1 - k/(r0+dxEff)
		k := r0 * r1
		newX := r0 + dxEff
		assert(newX > 0)
		assert(k > 0)
		dy := r1 - (k / newX)
		assert(dy > 0 && dy < r1)

		// slippage-adjusted extra fee to LPs (reduce user output and keep in reserves)
		dyUser := uint64(dy)
		if shareSlipBps > 0 {
			dyNominal := (r1 * dxEff) / r0
			if dyNominal > 0 {
				slipBps := (dyNominal - uint64(dy)) * 10_000 / dyNominal
				if slipBps > baselineSlipBps {
					excess := slipBps - baselineSlipBps
					// outExtra = dy * excessBps * shareBps / 1e8
					outExtra := uint64(dy) * excess * shareSlipBps / 10_000 / 10_000
					if outExtra >= dyUser {
						outExtra = dyUser - 1
					}
					dyUser -= outExtra
				}
			}
		}

		assert(dyUser >= minOutU)

		// update reserves: only effective input increases reserve
		setInt(keyReserve0, int64(r0+dxEff))
		setInt(keyReserve1, int64(r1-dyUser))

		// accrue base fee to HBD-side fee bucket only, with optional referral payout from base fee
		if isHbd(asset0) {
			fee := uint64(amountInU - dxEff)
			if fee > 0 {
				// optional referral share (paid in HBD) out of base fee
				refOut := uint64(0)
				if refBpsU > 0 {
					refOut = fee * refBpsU / 10_000
					if refOut > 0 {
						transferAsset(beneficiary, int64(refOut), asset0)
					}
				}
				feeRemain := int64(fee - refOut)
				if feeRemain > 0 {
					setInt(keyFee0, getInt(keyFee0)+feeRemain)
				}
			}
		}

		// send out asset1 to user
		transferAsset(sdk.GetEnv().Sender.Address, int64(dyUser), asset1)
	} else if dir == "1to0" {
		// input is asset1 (volatile side)
		drawAsset(int64(amountInU), asset1)

		// base fee applies only if input is HBD (it is not), so no base fee
		dxEff := amountInU
		k := r0 * r1
		newY := r1 + dxEff
		assert(newY > 0)
		assert(k > 0)
		dxOut := r0 - (k / newY)
		assert(dxOut > 0 && dxOut < r0)

		// slippage-adjusted extra fee to LPs (reduce user output and keep in reserves)
		dxUserTotal := uint64(dxOut)
		if shareSlipBps > 0 {
			dxNominal := (r0 * dxEff) / r1
			if dxNominal > 0 {
				slipBps := (dxNominal - uint64(dxOut)) * 10_000 / dxNominal
				if slipBps > baselineSlipBps {
					excess := slipBps - baselineSlipBps
					outExtra := uint64(dxOut) * excess * shareSlipBps / 10_000 / 10_000
					if outExtra >= dxUserTotal {
						outExtra = dxUserTotal - 1
					}
					dxUserTotal -= outExtra
				}
			}
		}

		// optional referral share (paid in HBD) deducted from user output
		refOut := uint64(0)
		if refBpsU > 0 {
			refOut = dxUserTotal * refBpsU / 10_000
			if refOut >= dxUserTotal {
				refOut = dxUserTotal - 1
			}
		}

		dxUserNet := dxUserTotal - refOut
		assert(dxUserNet >= minOutU)

		// only effective input increases reserve; reserve0 decreases by TOTAL HBD output (user + referral)
		setInt(keyReserve1, int64(r1+dxEff))
		setInt(keyReserve0, int64(r0-dxUserTotal))

		// no non-HBD fee accrual here

		// send to beneficiary first if any, then to user
		if refOut > 0 {
			transferAsset(beneficiary, int64(refOut), asset0)
		}
		transferAsset(sdk.GetEnv().Sender.Address, int64(dxUserNet), asset0)
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
		drawAsset(int64(amt0U), a0)
		setInt(keyReserve0, getInt(keyReserve0)+int64(amt0U))
	}
	if amt1U > 0 {
		drawAsset(int64(amt1U), a1)
		setInt(keyReserve1, getInt(keyReserve1)+int64(amt1U))
	}
	return nil
}

// Claim reserve fees; send HBD fees to system account. Non-HBD conversion is left as a TODO.
//
//go:wasmexport claim_fees
func ClaimFees(_ *string) *string {
	assert(isSystemSender())
	dao := sdk.Address("system:fr_balance")
	a0, a1 := getAssets()
	f0 := getInt(keyFee0)
	f1 := getInt(keyFee1)
	if f0 > 0 && a0 == sdk.AssetHbd {
		setInt(keyFee0, 0)
		sdk.HiveWithdraw(dao, f0, a0)
	}
	if f1 > 0 && a1 == sdk.AssetHbd {
		setInt(keyFee1, 0)
		sdk.HiveWithdraw(dao, f1, a1)
	}
	// Note: non-HBD conversion to HBD requires router; omitted here.
	setStr(keyFeeLastClaimUnix, sdk.GetEnv().Timestamp) // This might be a txid instead
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
		transferAsset(addr, out0, a0)
	}
	if out1 > 0 {
		transferAsset(addr, out1, a1)
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

// System function: set slip fee parameters (bps). Consensus-only.
// Payload: "baselineBps,shareBps"
//
//go:wasmexport set_slip_params
func SetSlipParams(payload *string) *string {
	assert(isSystemSender())
	parts := strings.Split(strings.TrimSpace(*payload), ",")
	assert(len(parts) == 2)
	baseline := parseUintStrict(parts[0])
	share := parseUintStrict(parts[1])
	assert(baseline <= 10_000 && share <= 10_000)
	setUint(keySlipBaselineBps, baseline)
	setUint(keySlipShareBps, share)
	return nil
}
