package main

import (
	"contract-template/sdk"
	"math/bits"
	"strconv"
)

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
	keyPaused            = "pool/paused"
	keyReentrancy        = "pool/reentrancy"
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

// sqrt128 returns floor(sqrt(hi:lo)) where hi:lo is a 128-bit unsigned integer
func sqrt128(hi, lo uint64) uint64 {
	var low, high uint64 = 0, ^uint64(0) >> 1
	var ans uint64
	for low <= high {
		mid := (low + high) >> 1
		mh, ml := bits.Mul64(mid, mid)
		if mh < hi || (mh == hi && ml <= lo) {
			ans = mid
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return ans
}

func parseUintStrict(s string) uint64 {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		panic("bad uint")
	}
	return v
}

func requireNotPaused() { assert(getUint(keyPaused) == 0) }

func enterReentrancy() { assert(getUint(keyReentrancy) == 0); setUint(keyReentrancy, 1) }
func exitReentrancy()  { setUint(keyReentrancy, 0) }

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

	//This may not return nil. It might be ""
	if v == nil {
		return 0
	}
	n, _ := strconv.ParseUint(*v, 10, 64)
	return n
}

func setLP(addr sdk.Address, amount uint64) {
	setUint(lpKey(addr), amount)
}
