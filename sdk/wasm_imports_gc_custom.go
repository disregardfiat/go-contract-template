//go:build gc.custom

package sdk

// These declarations are only included for TinyGo builds with -gc=custom.

//go:wasmimport sdk console.log
func log(s *string) *string

//go:wasmimport sdk db.setObject
func stateSetObject(key *string, value *string) *string

//go:wasmimport sdk db.getObject
func stateGetObject(key *string) *string

//go:wasmimport sdk db.delObject
func stateDeleteObject(key *string) *string

//go:wasmimport sdk system.getEnv
func getEnv(arg *string) *string

//go:wasmimport sdk hive.getbalance
func getBalance(arg1 *string, arg2 *string) *string

//go:wasmimport sdk hive.draw
func hiveDraw(arg1 *string, arg2 *string) *string

//go:wasmimport sdk hive.transfer
func hiveTransfer(arg1 *string, arg2 *string, arg3 *string) *string

//go:wasmimport sdk hive.withdraw
func hiveWithdraw(arg1 *string, arg2 *string, arg3 *string) *string
