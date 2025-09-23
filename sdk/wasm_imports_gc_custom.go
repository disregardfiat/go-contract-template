//go:build gc.custom

package sdk

// TinyGo WASM import declarations for runtime builds

//go:wasmimport sdk console.log
func log(s *string) *string

//go:wasmimport sdk db.set_object
func stateSetObject(key *string, value *string) *string

//go:wasmimport sdk db.get_object
func stateGetObject(key *string) *string

//go:wasmimport sdk db.rm_object
func stateDeleteObject(key *string) *string

//go:wasmimport sdk system.get_env
func getEnv(arg *string) *string

//go:wasmimport sdk system.get_env_key
func getEnvKey(arg *string) *string

//go:wasmimport sdk hive.get_balance
func getBalance(arg1 *string, arg2 *string) *string

//go:wasmimport sdk hive.draw
func hiveDraw(arg1 *string, arg2 *string) *string

//go:wasmimport sdk hive.transfer
func hiveTransfer(arg1 *string, arg2 *string, arg3 *string) *string

//go:wasmimport sdk hive.withdraw
func hiveWithdraw(arg1 *string, arg2 *string, arg3 *string) *string

// The following are not implemented yet
//
//go:wasmimport sdk contracts.read
func contractRead(contractId *string, key *string) *string

//go:wasmimport sdk contracts.call
func contractCall(contractId *string, method *string, payload *string, options *string) *string

//go:wasmimport env abort
func abort(msg, file *string, line, column *int32)
