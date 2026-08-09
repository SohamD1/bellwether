# Base Sepolia synthetic demo

This workflow uses **synthetic fixture data and produces synthetic demonstration results**. Do not interpret its metrics as evidence of forecasting performance.

## Create the local dataset

From the repository root, write the deterministic 72-row dataset (24 fixture markets, three point-in-time updates each):

```powershell
New-Item -ItemType Directory -Force ./.demo | Out-Null
go run ./cmd/bellwether-demo --output ./.demo/bellwether-synthetic.parquet
```

The command prints the logical dataset ID and output path. Substitute that ID below; the output directory deliberately retains the `synthetic` label.

```powershell
$datasetId = "DATASET_ID_PRINTED_ABOVE"
$gitSha = git rev-parse HEAD
Push-Location ./python
uv run bellwether-experiment --data ../.demo/bellwether-synthetic.parquet --config ../experiment.example.toml --output-dir ../.demo/synthetic-results --git-sha $gitSha --data-snapshot-id $datasetId
Pop-Location
```

Every dataset row and every resulting experiment artifact is synthetic.

## Deploy the testnet contracts

Base Sepolia uses chain ID `84532`, public RPC `https://sepolia.base.org`, and explorer `https://sepolia-explorer.base.org`.

Import the deployer into Foundry's encrypted keystore once. `cast` prompts securely for the key and an encryption password; never put a private key in a command, shell variable, `.env` file, or repository file.

```powershell
cast wallet import bellwether-base-sepolia
```

Set `DEPLOYER_ADDRESS` below to the address of that account. From `contracts`, dry-run the exact deployment without submitting transactions:

```powershell
forge script script/Deploy.s.sol:Deploy --rpc-url https://sepolia.base.org --chain-id 84532 --account bellwether-base-sepolia --sender DEPLOYER_ADDRESS
```

Review the simulation and contract addresses. To approve wallet use and broadcast, rerun with `--broadcast`; Foundry prompts to unlock the named encrypted account at execution time:

```powershell
forge script script/Deploy.s.sol:Deploy --rpc-url https://sepolia.base.org --chain-id 84532 --account bellwether-base-sepolia --sender DEPLOYER_ADDRESS --broadcast
```

Inspect submitted transactions and deployed addresses on the Base Sepolia explorer. The public RPC is rate-limited and may throttle backfills or repeated simulations; use a dedicated Base Sepolia RPC provider if that becomes unreliable.

These contracts are no-value testnet fixtures, not audited production contracts. Obtain an independent security audit before adapting or deploying any related contract to mainnet.
