//go:build !gc.custom

package sdk

import (
	"encoding/json"
	"strconv"
	"sync"
)

// Host shim for standard Go tests: provides in-memory implementations of wasm imports.

var (
	shimState    = map[string]string{}
	shimBalances = map[string]map[string]int64{}
	shimEnv      = map[string]string{
		"contract_id":                "contract:test",
		"anchor.id":                  "tx:0",
		"anchor.block":               "block:0",
		"anchor.height":              "0",
		"anchor.timestamp":           "0",
		"anchor.tx_index":            "0",
		"anchor.op_index":            "0",
		"msg.sender":                 "hive:alice",
		"msg.required_auths":         "[]",
		"msg.required_posting_auths": "[]",
	}
	shimMu sync.RWMutex
)

// wasmimport-compatible function signatures
func log(s *string) *string { return nil }

func stateSetObject(key *string, value *string) *string {
	shimMu.Lock()
	defer shimMu.Unlock()
	shimState[*key] = *value
	return nil
}

func stateGetObject(key *string) *string {
	shimMu.RLock()
	defer shimMu.RUnlock()
	if v, ok := shimState[*key]; ok {
		vv := v
		return &vv
	}
	empty := ""
	return &empty
}

func stateDeleteObject(key *string) *string {
	shimMu.Lock()
	defer shimMu.Unlock()
	delete(shimState, *key)
	return nil
}

func getEnv(key *string) *string {
	shimMu.RLock()
	defer shimMu.RUnlock()
	if v, ok := shimEnv[*key]; ok {
		vv := v
		return &vv
	}
	empty := ""
	return &empty
}

func getBalance(addr *string, asset *string) *string {
	shimMu.RLock()
	defer shimMu.RUnlock()
	b := shimBalances[*addr]
	val := b[*asset]
	s := strconv.FormatInt(val, 10)
	return &s
}

func hiveDraw(amount *string, asset *string) *string {
	shimMu.Lock()
	defer shimMu.Unlock()
	amt, _ := strconv.ParseInt(*amount, 10, 64)
	sender := shimEnv["msg.sender"]
	contract := shimEnv["contract_id"]
	decBal(sender, *asset, amt)
	incBal(contract, *asset, amt)
	return nil
}

func hiveTransfer(to *string, amount *string, asset *string) *string {
	shimMu.Lock()
	defer shimMu.Unlock()
	amt, _ := strconv.ParseInt(*amount, 10, 64)
	contract := shimEnv["contract_id"]
	decBal(contract, *asset, amt)
	incBal(*to, *asset, amt)
	return nil
}

func hiveWithdraw(to *string, amount *string, asset *string) *string {
	return hiveTransfer(to, amount, asset)
}

// Test helpers
func ShimReset() {
	shimMu.Lock()
	defer shimMu.Unlock()
	shimState = map[string]string{}
	shimBalances = map[string]map[string]int64{}
	shimEnv = map[string]string{
		"contract_id":                "contract:test",
		"anchor.id":                  "tx:0",
		"anchor.block":               "block:0",
		"anchor.height":              "0",
		"anchor.timestamp":           "0",
		"anchor.tx_index":            "0",
		"anchor.op_index":            "0",
		"msg.sender":                 "hive:alice",
		"msg.required_auths":         "[]",
		"msg.required_posting_auths": "[]",
	}
}

func ShimSetEnv(key, val string)  { shimMu.Lock(); shimEnv[key] = val; shimMu.Unlock() }
func ShimSetSender(addr Address)  { ShimSetEnv("msg.sender", addr.String()) }
func ShimSetTimestamp(ts string)  { ShimSetEnv("anchor.timestamp", ts) }
func ShimSetContractId(id string) { ShimSetEnv("contract_id", id) }

func ShimSetBalance(addr Address, asset Asset, amount int64) {
	shimMu.Lock()
	defer shimMu.Unlock()
	if _, ok := shimBalances[addr.String()]; !ok {
		shimBalances[addr.String()] = map[string]int64{}
	}
	shimBalances[addr.String()][asset.String()] = amount
}

func ShimGetBalance(addr Address, asset Asset) int64 {
	shimMu.RLock()
	defer shimMu.RUnlock()
	return shimBalances[addr.String()][asset.String()]
}

func ShimSetAuths(requiredAuths []Address, postingAuths []Address) {
	ra, _ := json.Marshal(requiredAuths)
	pa, _ := json.Marshal(postingAuths)
	ShimSetEnv("msg.required_auths", string(ra))
	ShimSetEnv("msg.required_posting_auths", string(pa))
}

func incBal(addr, asset string, amt int64) {
	if _, ok := shimBalances[addr]; !ok {
		shimBalances[addr] = map[string]int64{}
	}
	shimBalances[addr][asset] += amt
}
func decBal(addr, asset string, amt int64) { incBal(addr, asset, -amt) }




