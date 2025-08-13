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
	// check reserves reflect effective input and output
	if uint64(getInt(keyReserve0)) != preR0+dxEff {
		t.Fatal("reserve0 not updated by effective input")
	}
	if uint64(getInt(keyReserve1)) != preR1-expectedDy {
		t.Fatal("reserve1 not decreased by output")
	}
	// fee bucket 0 increased by fee on input
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
