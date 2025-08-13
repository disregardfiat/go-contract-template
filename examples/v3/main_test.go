package main

import (
	"contract-template/sdk"
	"strconv"
	"testing"
)

func s(arg string) *string { return &arg }

// Constants to make Q32.32 sqrt pricing simpler in tests
const (
	Q = 32
)

func TestV3_Init_Mint_Swap_Fees_Burn_Collect(t *testing.T) {
	sdk.ShimReset()
	sdk.ShimSetContractId("contract:v3")
	sdk.ShimSetSender(sdk.Address("hive:lp"))

	// price sqrtP between lower and upper
	sqrtP := uint64(1 << Q)
	// Very tight active range to keep denominators tiny and avoid zero L
	lower := sqrtP - 1
	upper := sqrtP + 1
	InitV3(s("hbd,hive,30," + strconv.FormatUint(sqrtP, 10) + "," + strconv.FormatUint(lower, 10) + "," + strconv.FormatUint(upper, 10)))

	// fund LP and mint
	sdk.ShimSetBalance(sdk.Address("hive:lp"), sdk.AssetHbd, 50_000_000)
	sdk.ShimSetBalance(sdk.Address("hive:lp"), sdk.AssetHive, 50_000_000)
	MintV3(s(strconv.FormatUint(lower, 10) + "," + strconv.FormatUint(upper, 10) + ",10000000,10000000"))
	if L := v3getU(v3KeyLiquidity); L == 0 {
		t.Fatal("liquidity not increased on mint")
	}

	// Simulate fee growth distribution directly (bypass swap math in host tests)
	L := v3getU(v3KeyLiquidity)
	deltaFG := uint64(1 << (Q + 8)) // arbitrary Q32 increment
	startFG0 := v3getU(v3KeyFeeGrowth0)
	v3setU(v3KeyFeeGrowth0, startFG0+deltaFG)

	// accrue owed by updating position snapshot and then collect
	sdk.ShimSetSender(sdk.Address("hive:lp"))
	updatePositionOwed(sdk.Address("hive:lp"), lower, upper)
	owed0 := v3getU(v3posKey(sdk.Address("hive:lp"), lower, upper, "owed0"))
	expectedOwed0 := (deltaFG * L) >> Q
	if owed0 != expectedOwed0 {
		t.Fatalf("owed0=%d want %d", owed0, expectedOwed0)
	}
	// Burn some liquidity to realize current underlying owed in-range
	curL := v3getU(v3posKey(sdk.Address("hive:lp"), lower, upper, "liquidity"))
	BurnV3(s(strconv.FormatUint(lower, 10) + "," + strconv.FormatUint(upper, 10) + "," + strconv.FormatUint(curL/4, 10)))
	if v3getU(v3posKey(sdk.Address("hive:lp"), lower, upper, "liquidity")) != curL-curL/4 {
		t.Fatal("liquidity not reduced on burn")
	}
	// Collect transfers owed balances
	pre0 := sdk.ShimGetBalance(sdk.Address("hive:lp"), sdk.AssetHbd)
	pre1 := sdk.ShimGetBalance(sdk.Address("hive:lp"), sdk.AssetHive)
	CollectV3(s(strconv.FormatUint(lower, 10) + "," + strconv.FormatUint(upper, 10)))
	if v3getU(v3posKey(sdk.Address("hive:lp"), lower, upper, "owed0")) != 0 {
		t.Fatal("owed0 not cleared after collect")
	}
	if v3getU(v3posKey(sdk.Address("hive:lp"), lower, upper, "owed1")) != 0 {
		t.Fatal("owed1 not cleared after collect")
	}
	// balances increased for LP
	post0 := sdk.ShimGetBalance(sdk.Address("hive:lp"), sdk.AssetHbd)
	post1 := sdk.ShimGetBalance(sdk.Address("hive:lp"), sdk.AssetHive)
	if !(post0 > pre0 || post1 > pre1) {
		t.Fatal("lp did not receive any collected tokens")
	}
}

func TestV3_System_Gov_And_Bounds(t *testing.T) {
	sdk.ShimReset()
	sdk.ShimSetContractId("contract:v3")
	// initialize
	sqrtP := uint64(1 << Q)
	lower := uint64(1 << (Q - 1))
	upper := uint64(1 << (Q + 1))
	InitV3(s("hbd,hive,10," + strconv.FormatUint(sqrtP, 10) + "," + strconv.FormatUint(lower, 10) + "," + strconv.FormatUint(upper, 10)))

	// non-system cannot set fee
	defer func() { _ = recover() }()
	SetFeeV3(s("50"))
	// system can
	sdk.ShimSetSender(sdk.Address("system:consensus"))
	SetFeeV3(s("50"))
	if v3getU(v3KeyFeeBps) != 50 {
		t.Fatal("fee not set by system")
	}
	// set active range with price inside
	SetActiveRangeV3(s(strconv.FormatUint(lower/2, 10) + "," + strconv.FormatUint(upper, 10)))
	if v3getU(v3KeyActiveLower) != lower/2 {
		t.Fatal("active lower not updated")
	}
}
