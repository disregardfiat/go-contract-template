## AMM v2 (HBD-anchored, slip-adjusted fees)

### Overview

This example implements a constant-product AMM where every pool is anchored to HBD. Key behaviors:

- **Initialization**: `init` with payload `asset0,asset1,baseFeeBps` (e.g., `hbd,hive,8`).
- **Liquidity**:
  - `add_liquidity amt0,amt1`: mints LP on geometric mean (first add) or proportionally.
  - `remove_liquidity lpAmount`: burns LP and returns the proportional share.
  - `donate amt0,amt1`: increases reserves without minting LP.
- **Swaps**: `swap dir,amountIn[,minOut]` with `dir` in `{0to1,1to0}`.
  - Applies a base fee only when the input side is HBD.
  - Adds an optional slip-adjusted fee (portion of slippage above a baseline) kept in reserves for LPs.
  - Optional referral/beneficiary: either `swap dir,amountIn,beneficiary,refBps` or `swap dir,amountIn,minOut,beneficiary,refBps`.
    - **refBps**: 1–1000 (0.01%–10.00%).
    - For `0to1` (HBD input): referral is paid in HBD from the base fee, not affecting user output.
    - For `1to0` (HBD output): referral is a portion of the HBD output, reducing user output accordingly.
- **Fees**:
  - Base fee is tracked per-side but only HBD fees are claimable.
  - `claim_fees`: consensus-only; withdraws HBD fees to `system:fr_balance`.
- **LP management**: `transfer` LP, `burn` LP (reduces supply without withdrawing reserves).
- **Safety & system params**:
  - `si_withdraw address,lpAmount`: consensus-only proportional withdrawal for emergencies.
  - `set_base_fee newBps`: consensus-only base fee update.
  - `set_slip_params baselineBps,shareBps`: consensus-only slip fee controls.

The contract integrates with native assets via the SDK’s `HiveDraw`, `HiveTransfer`, and `HiveWithdraw`. Internally, simple token adapter helpers are used to keep I/O abstract.

### Tests

- **Unit tests (host shim)**
  - File: `main_test.go`
  - Covers initialization, adding/removing liquidity, swaps with HBD-only base fee accrual, donation, LP transfer/burn, and fee claiming. Also validates slip-fee behavior and that non-HBD input does not accrue base fees.
  - Run from this directory:
    - `go test -v`

- **Node WASM e2e test (go-vsc-node harness)**
  - File: `go-vsc-node/modules/wasm/e2e/amm_e2e_test.go`
  - Compiles this package to TinyGo WASM, then executes: `init`, `add_liquidity`, HBD→volatile swap, sets slip params, volatile→HBD swap, and `claim_fees` (which withdraws HBD out of the contract). Validates balances, intents, and RC under the node harness.
  - From the repository root, run:
    - `cd go-vsc-node && go test ./modules/wasm/e2e -run TestAMM_Wasm_Init_Add_Swap_Claim -v`
  - Referral paths are covered in unit tests in this folder.

### Capturing logs

To capture the full e2e test log into `test.log` in this folder, run from the workspace root:

```bash
cd go-vsc-node && go test ./modules/wasm/e2e -run TestAMM_Wasm_Init_Add_Swap_Claim -v | tee ../go-contract-template/examples/v2-amm/test.log
```

The checked-in `test.log` corresponds to the most recent successful run of the above command.



