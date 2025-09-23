package main

import (
	"contract-template/sdk"
	"strconv"
	"testing"
)

func sptr(s string) *string { return &s }

func TestV2_Init_Add_Remove_Swap_Fees(t *testing.T) {
	// reset shim and identities
	sdk.ShimReset()
	sdk.ShimSetContractId("contract:v2")
	sdk.ShimSetSender(sdk.Address("hive:alice"))

	// init pool hbd/hive fee 30 bps
	if Init(sptr("hbd,hive,30")) != nil {
		t.Fatal("init returned non-nil error")
	}
	if got := getUint(keyBaseFeeBps); got != 30 {
		t.Fatalf("base fee = %d, want 30", got)
	}
	if getInt(keyReserve0) != 0 || getInt(keyReserve1) != 0 {
		t.Fatal("reserves must start at 0")
	}

	// fund alice
	sdk.ShimSetBalance(sdk.Address("hive:alice"), sdk.AssetHbd, 1_000_000)
	sdk.ShimSetBalance(sdk.Address("hive:alice"), sdk.AssetHive, 2_000_000)

	// add initial liquidity 100k/200k
	if AddLiquidity(sptr("100000,200000")) != nil {
		t.Fatal("add liquidity failed")
	}
	if getUint(keyTotalLP) == 0 {
		t.Fatal("total LP must be > 0")
	}
	if getInt(keyReserve0) != 100000 || getInt(keyReserve1) != 200000 {
		t.Fatalf("reserves mismatch: %d,%d", getInt(keyReserve0), getInt(keyReserve1))
	}
	// balances moved from alice to contract
	if sdk.ShimGetBalance(sdk.Address("hive:alice"), sdk.AssetHbd) != 900000 {
		t.Fatal("alice hbd not debited")
	}
	if sdk.ShimGetBalance(sdk.Address("contract:v2"), sdk.AssetHbd) != 100000 {
		t.Fatal("contract hbd not credited")
	}

	// swap 0->1: bob swaps 10k hbd to receive hive
	sdk.ShimSetSender(sdk.Address("hive:bob"))
	sdk.ShimSetBalance(sdk.Address("hive:bob"), sdk.AssetHbd, 100000)
	a0, a1 := getAssets()
	if a0 != sdk.AssetHbd || a1 != sdk.AssetHive {
		t.Fatal("asset mapping unexpected")
	}
	preR0 := uint64(getInt(keyReserve0))
	preR1 := uint64(getInt(keyReserve1))
	feeBps := getUint(keyBaseFeeBps)
	amtIn := uint64(10_000)
	if Swap(sptr("0to1,"+strconv.FormatUint(amtIn, 10))) != nil {
		t.Fatal("swap failed")
	}
	feeNumer := 10_000 - feeBps
	dxEff := amtIn * feeNumer / 10_000
	// expected dy = r1 - k/(r0+dxEff)
	k := preR0 * preR1
	newX := preR0 + dxEff
	if newX == 0 {
		t.Fatal("newX zero")
	}
	expectedDy := preR1 - (k / newX)
	// check reserves reflect effective input and output (slip fee defaults 0)
	if uint64(getInt(keyReserve0)) != preR0+dxEff {
		t.Fatal("reserve0 not updated by effective input")
	}
	if uint64(getInt(keyReserve1)) != preR1-expectedDy {
		t.Fatal("reserve1 not decreased by expected user output")
	}
	// fee bucket 0 increased by base fee on HBD input
	expectedFee0 := int64(amtIn - dxEff)
	if getInt(keyFee0) != expectedFee0 {
		t.Fatalf("fee0=%d want %d", getInt(keyFee0), expectedFee0)
	}
	// bob asset1 received
	if sdk.ShimGetBalance(sdk.Address("hive:bob"), sdk.AssetHive) != int64(expectedDy) {
		t.Fatalf("bob did not receive expected output: %d", sdk.ShimGetBalance(sdk.Address("hive:bob"), sdk.AssetHive))
	}

	// remove 20% liquidity by alice
	sdk.ShimSetSender(sdk.Address("hive:alice"))
	lpBalStr := getStr(lpKey(sdk.Address("hive:alice")))
	if lpBalStr == "" {
		t.Fatal("missing LP balance")
	}
	lpBal, _ := strconv.ParseUint(lpBalStr, 10, 64)
	burn := lpBal / 5
	preR0 = uint64(getInt(keyReserve0))
	preR1 = uint64(getInt(keyReserve1))
	preTotal := getUint(keyTotalLP)
	if RemoveLiquidity(sptr(strconv.FormatUint(burn, 10))) != nil {
		t.Fatal("remove failed")
	}
	// proportional outputs
	out0 := int64(preR0 * burn / preTotal)
	out1 := int64(preR1 * burn / preTotal)
	if sdk.ShimGetBalance(sdk.Address("hive:alice"), sdk.AssetHbd) != 900000+out0 {
		t.Fatal("alice did not receive token0 on remove")
	}
	if sdk.ShimGetBalance(sdk.Address("hive:alice"), sdk.AssetHive) != 2_000_000-200000+out1 {
		t.Fatal("alice did not receive token1 on remove")
	}
}

func TestV2_Donate_Transfer_Burn_ClaimFees_System(t *testing.T) {
	sdk.ShimReset()
	sdk.ShimSetContractId("contract:v2")
	sdk.ShimSetSender(sdk.Address("hive:lp1"))
	Init(sptr("hbd,hive,8"))
	sdk.ShimSetBalance(sdk.Address("hive:lp1"), sdk.AssetHbd, 1_000_000)
	sdk.ShimSetBalance(sdk.Address("hive:lp1"), sdk.AssetHive, 1_000_000)
	AddLiquidity(sptr("100000,100000"))

	// Donate adds to reserves without LP mint
	preTotal := getUint(keyTotalLP)
	sdk.ShimSetSender(sdk.Address("hive:donor"))
	sdk.ShimSetBalance(sdk.Address("hive:donor"), sdk.AssetHbd, 5000)
	Donate(sptr("5000,0"))
	if getUint(keyTotalLP) != preTotal {
		t.Fatal("total LP changed on donate")
	}

	// Transfer LP to another address
	sdk.ShimSetSender(sdk.Address("hive:lp1"))
	lpOwned := getLP(sdk.Address("hive:lp1"))
	Transfer(sptr("hive:lp2," + strconv.FormatUint(lpOwned/2, 10)))
	if getLP(sdk.Address("hive:lp1")) != lpOwned/2 {
		t.Fatal("sender LP not reduced")
	}
	if getLP(sdk.Address("hive:lp2")) != lpOwned/2 {
		t.Fatal("recipient LP not increased")
	}

	// Burn LP reduces total supply
	preTotal = getUint(keyTotalLP)
	Burn(sptr(strconv.FormatUint(lpOwned/10, 10)))
	if getUint(keyTotalLP) != preTotal-lpOwned/10 {
		t.Fatal("total LP not reduced on burn")
	}

	// Accrue fees by a swap and claim to system FR for HBD side
	sdk.ShimSetSender(sdk.Address("hive:trader"))
	sdk.ShimSetBalance(sdk.Address("hive:trader"), sdk.AssetHbd, 20000)
	Swap(sptr("0to1,10000"))
	if getInt(keyFee0) <= 0 {
		t.Fatal("no fee accrued on 0 side")
	}
	// Claim must be system-only now; sends to system:fr_balance
	preFR := sdk.ShimGetBalance(sdk.Address("system:fr_balance"), sdk.AssetHbd)
	sdk.ShimSetSender(sdk.Address("system:consensus"))
	ClaimFees(nil)
	if sdk.ShimGetBalance(sdk.Address("system:fr_balance"), sdk.AssetHbd) <= preFR {
		t.Fatal("fees not transferred to system FR")
	}
	if getInt(keyFee0) != 0 {
		t.Fatal("fee0 not reset to 0 after claim")
	}

	// System-only ops
	sdk.ShimSetSender(sdk.Address("system:consensus"))
	SetBaseFee(sptr("25"))
	if getUint(keyBaseFeeBps) != 25 {
		t.Fatal("base fee not updated by system")
	}

	// SI withdraw proportionally from lp2
	preR0 := uint64(getInt(keyReserve0))
	preR1 := uint64(getInt(keyReserve1))
	preTotal = getUint(keyTotalLP)
	lp2 := getLP(sdk.Address("hive:lp2")) / 2
	SIWithdraw(sptr("hive:lp2," + strconv.FormatUint(lp2, 10)))
	// reserves reduced
	if uint64(getInt(keyReserve0)) >= preR0 || uint64(getInt(keyReserve1)) >= preR1 {
		t.Fatal("reserves not reduced after SIWithdraw")
	}
	if getUint(keyTotalLP) != preTotal-lp2 {
		t.Fatal("total LP not reduced after SIWithdraw")
	}
}

func TestV2_SlipFee_And_NonHBDFeeRules(t *testing.T) {
	sdk.ShimReset()
	sdk.ShimSetContractId("contract:v2")
	sdk.ShimSetSender(sdk.Address("hive:lp"))
	Init(sptr("hbd,hive,20"))
	sdk.ShimSetSender(sdk.Address("system:consensus"))
	SetSlipParams(sptr("50,500")) // baseline 0.50%, share 5%
	sdk.ShimSetSender(sdk.Address("hive:lp"))
	sdk.ShimSetBalance(sdk.Address("hive:lp"), sdk.AssetHbd, 1_000_000)
	sdk.ShimSetBalance(sdk.Address("hive:lp"), sdk.AssetHive, 1_000_000)
	AddLiquidity(sptr("200000,200000"))

	// Non-HBD input should not accrue base fee
	sdk.ShimSetSender(sdk.Address("hive:trader1"))
	sdk.ShimSetBalance(sdk.Address("hive:trader1"), sdk.AssetHive, 100_000)
	preFee1 := getInt(keyFee1)
	_ = Swap(sptr("1to0,10000"))
	if getInt(keyFee1) != preFee1 {
		t.Fatal("non-HBD input should not accrue base fee")
	}

	// HBD input should accrue base fee to fee0 and slip fee reduces user out (reserve1 decreases less than nominal dy)
	sdk.ShimSetSender(sdk.Address("hive:trader2"))
	sdk.ShimSetBalance(sdk.Address("hive:trader2"), sdk.AssetHbd, 100_000)
	preR0 := uint64(getInt(keyReserve0))
	preR1 := uint64(getInt(keyReserve1))
	_ = Swap(sptr("0to1,10000"))
	if getInt(keyFee0) <= 0 {
		t.Fatal("HBD input should accrue base fee")
	}
	if uint64(getInt(keyReserve0)) <= preR0 {
		t.Fatal("reserve0 should increase")
	}
	if uint64(getInt(keyReserve1)) >= preR1 {
		t.Fatal("reserve1 should decrease")
	}
}

func expectPanic(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic but none occurred")
		}
	}()
	f()
}

func TestV2_Referral_Bounds_And_MinOut(t *testing.T) {
	sdk.ShimReset()
	sdk.ShimSetContractId("contract:v2")
	sdk.ShimSetSender(sdk.Address("hive:lp"))
	Init(sptr("hbd,hive,100")) // 1% base fee
	sdk.ShimSetBalance(sdk.Address("hive:lp"), sdk.AssetHbd, 1_000_000)
	sdk.ShimSetBalance(sdk.Address("hive:lp"), sdk.AssetHive, 1_000_000)
	AddLiquidity(sptr("200000,200000"))

	// refBps out of bounds should panic
	sdk.ShimSetSender(sdk.Address("hive:trader"))
	sdk.ShimSetBalance(sdk.Address("hive:trader"), sdk.AssetHbd, 10000)
	expectPanic(t, func() { _ = Swap(sptr("0to1,1000,,hive:ref,0")) })    // 0 invalid
	expectPanic(t, func() { _ = Swap(sptr("0to1,1000,,hive:ref,1001")) }) // >1000 invalid

	// Lower/upper bound valid
	_ = Swap(sptr("0to1,1000,,hive:ref,1"))
	_ = Swap(sptr("0to1,1000,,hive:ref,1000"))

	// 1to0 minOut applies to net after referral
	sdk.ShimSetSender(sdk.Address("hive:trader2"))
	sdk.ShimSetBalance(sdk.Address("hive:trader2"), sdk.AssetHive, 100000)
	preR0 := uint64(getInt(keyReserve0))
	preR1 := uint64(getInt(keyReserve1))
	amtIn := uint64(5000)
	// compute expected gross out
	k := preR0 * preR1
	gross := preR0 - (k / (preR1 + amtIn))
	ref := gross * 100 / 10000 // 1%
	net := gross - ref
	// should pass when minOut == net
	_ = Swap(sptr("1to0,5000," + strconv.FormatUint(net, 10) + ",hive:ref2,100"))
	// should panic when minOut > net
	expectPanic(t, func() {
		_ = Swap(sptr("1to0,5000," + strconv.FormatUint(net+1, 10) + ",hive:ref2,100"))
	})

	// 0to1 minOut unaffected by referral (since paid from base fee). Just ensure no panic with high refBps.
	sdk.ShimSetSender(sdk.Address("hive:trader3"))
	sdk.ShimSetBalance(sdk.Address("hive:trader3"), sdk.AssetHbd, 10000)
	_ = Swap(sptr("0to1,1000,1,hive:ref3,1000"))
}

func TestV2_BaseFeeZero_Referral_Paths(t *testing.T) {
	sdk.ShimReset()
	sdk.ShimSetContractId("contract:v2")
	sdk.ShimSetSender(sdk.Address("hive:lp"))
	Init(sptr("hbd,hive,0")) // base fee 0
	sdk.ShimSetBalance(sdk.Address("hive:lp"), sdk.AssetHbd, 1_000_000)
	sdk.ShimSetBalance(sdk.Address("hive:lp"), sdk.AssetHive, 1_000_000)
	AddLiquidity(sptr("100000,100000"))

	// 0to1 with referral should not pay beneficiary when base fee = 0
	sdk.ShimSetSender(sdk.Address("hive:trader"))
	sdk.ShimSetBalance(sdk.Address("hive:trader"), sdk.AssetHbd, 10000)
	preRef := sdk.ShimGetBalance(sdk.Address("hive:ref"), sdk.AssetHbd)
	_ = Swap(sptr("0to1,1000,,hive:ref,1000"))
	if sdk.ShimGetBalance(sdk.Address("hive:ref"), sdk.AssetHbd) != preRef {
		t.Fatal("referral should not be paid when base fee is 0 on 0to1")
	}

	// 1to0 with referral still pays from HBD out
	sdk.ShimSetSender(sdk.Address("hive:trader2"))
	sdk.ShimSetBalance(sdk.Address("hive:trader2"), sdk.AssetHive, 10000)
	preRef2 := sdk.ShimGetBalance(sdk.Address("hive:ref2"), sdk.AssetHbd)
	_ = Swap(sptr("1to0,1000,,hive:ref2,1000"))
	if sdk.ShimGetBalance(sdk.Address("hive:ref2"), sdk.AssetHbd) <= preRef2 {
		t.Fatal("referral should be paid from HBD out on 1to0 even with base fee 0")
	}
}

func TestV2_InvalidDir_And_NonSystem_Claim(t *testing.T) {
	sdk.ShimReset()
	sdk.ShimSetContractId("contract:v2")
	sdk.ShimSetSender(sdk.Address("hive:lp"))
	Init(sptr("hbd,hive,8"))
	sdk.ShimSetBalance(sdk.Address("hive:lp"), sdk.AssetHbd, 100000)
	sdk.ShimSetBalance(sdk.Address("hive:lp"), sdk.AssetHive, 100000)
	AddLiquidity(sptr("50000,50000"))

	// invalid dir should panic
	sdk.ShimSetSender(sdk.Address("hive:trader"))
	sdk.ShimSetBalance(sdk.Address("hive:trader"), sdk.AssetHbd, 10000)
	expectPanic(t, func() { _ = Swap(sptr("bad,1000")) })

	// non-system claim should panic
	expectPanic(t, func() { _ = ClaimFees(nil) })

	// system claim should succeed
	sdk.ShimSetSender(sdk.Address("system:consensus"))
	_ = ClaimFees(nil)
}

func TestV2_Swap_Referral_Beneficiary(t *testing.T) {
	sdk.ShimReset()
	sdk.ShimSetContractId("contract:v2")
	sdk.ShimSetSender(sdk.Address("hive:lp"))
	// base fee 100 bps (1%) for simpler math
	Init(sptr("hbd,hive,100"))
	sdk.ShimSetBalance(sdk.Address("hive:lp"), sdk.AssetHbd, 1_000_000)
	sdk.ShimSetBalance(sdk.Address("hive:lp"), sdk.AssetHive, 1_000_000)
	AddLiquidity(sptr("100000,100000"))

	// 0->1 with referral: referral paid from base fee (HBD), not from user output
	sdk.ShimSetSender(sdk.Address("hive:bob"))
	sdk.ShimSetBalance(sdk.Address("hive:bob"), sdk.AssetHbd, 20000)
	preR0 := uint64(getInt(keyReserve0))
	preR1 := uint64(getInt(keyReserve1))
	amtIn := uint64(10_000)
	refBps := uint64(100) // 1%
	// form: dir,amountIn,minOut,beneficiary,refBps (minOut empty)
	if Swap(sptr("0to1,"+strconv.FormatUint(amtIn, 10)+",,hive:ref,100")) != nil {
		t.Fatal("swap referral 0->1 failed")
	}
	// Compute expected values
	feeBps := getUint(keyBaseFeeBps) // 100
	dxEff := amtIn * (10_000 - feeBps) / 10_000
	k := preR0 * preR1
	expectedDy := preR1 - (k / (preR0 + dxEff))
	baseFeeAmt := amtIn - dxEff
	refOut := baseFeeAmt * refBps / 10_000

	// reserves reflect dxEff and expected user output (unchanged by referral)
	if uint64(getInt(keyReserve0)) != preR0+dxEff {
		t.Fatal("reserve0 not increased by dxEff with referral")
	}
	if uint64(getInt(keyReserve1)) != preR1-expectedDy {
		t.Fatal("reserve1 not decreased by expected dy with referral")
	}
	// fee bucket reduced by referral payout
	if getInt(keyFee0) != int64(baseFeeAmt-refOut) {
		t.Fatalf("fee0 unexpected with referral: %d", getInt(keyFee0))
	}
	// beneficiary received HBD referral
	if sdk.ShimGetBalance(sdk.Address("hive:ref"), sdk.AssetHbd) != int64(refOut) {
		t.Fatal("beneficiary did not receive HBD referral share")
	}

	// 1->0 with referral: referral deducted from user HBD out
	sdk.ShimSetSender(sdk.Address("hive:charlie"))
	sdk.ShimSetBalance(sdk.Address("hive:charlie"), sdk.AssetHive, 50000)
	preR0 = uint64(getInt(keyReserve0))
	preR1 = uint64(getInt(keyReserve1))
	amtIn = 10_000
	refBps = 500 // 5%
	if Swap(sptr("1to0,"+strconv.FormatUint(amtIn, 10)+",,hive:ref2,500")) != nil {
		t.Fatal("swap referral 1->0 failed")
	}
	// No base fee, dxEff = amtIn
	dxEff = amtIn
	k = preR0 * preR1
	grossDx := preR0 - (k / (preR1 + dxEff))
	// slip params default 0 -> user total before referral equals grossDx
	refOut2 := grossDx * refBps / 10_000
	userNet := grossDx - refOut2

	// reserves: r1 increases by amtIn, r0 decreases by total out (user + referral)
	if uint64(getInt(keyReserve1)) != preR1+dxEff {
		t.Fatal("reserve1 not increased by dxEff on 1->0 with referral")
	}
	if uint64(getInt(keyReserve0)) != preR0-grossDx {
		t.Fatal("reserve0 not decreased by total output on 1->0 with referral")
	}
	// beneficiary received HBD referral
	if sdk.ShimGetBalance(sdk.Address("hive:ref2"), sdk.AssetHbd) != int64(refOut2) {
		t.Fatal("beneficiary did not receive HBD referral share on 1->0")
	}
	// user received remaining HBD
	if sdk.ShimGetBalance(sdk.Address("hive:charlie"), sdk.AssetHbd) != int64(userNet) {
		t.Fatal("user did not receive net HBD after referral")
	}
}
