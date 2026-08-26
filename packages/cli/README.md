# Rysk V12 CLI

A command-line interface (CLI) for interacting with the Rysk v12 protocol via WebSockets.

This CLI allows you to connect to a WebSocket server, send signed messages for actions like approving token spending, initiating transfers, and sending quotes. It utilizes Unix sockets for inter-process communication, enabling you to send commands to a running WebSocket connection from other processes.

## Prerequisites

- **Go (Golang) installed:** This project is written in Go and requires a Go development environment to build.
- **Ethereum Node Access:** For the `approve` command, you'll need access to an Ethereum node (e.g., via Infura, Alchemy, or a local node) corresponding to the specified `rpc_url`.

## Installation

1.  **Clone the repository (if the code is in one):**

    ```bash
    git clone <repository_url>
    cd <repository_directory>
    ```

2.  **Build the CLI:**
    ```bash
    go build -o ryskV12
    ```
    This will create an executable file named `ryskV12` in the current directory.

    `./build.sh` cross compiles the release set - linux/amd64, linux/arm64,
    darwin/amd64, darwin/arm64 - from any of those hosts, since the CLI needs no
    cgo. It pins `CGO_ENABLED=0` so every binary is statically linked and runs on
    any distro, glibc or musl.

    The macOS binaries are not signed or notarized. A `curl` download (what the
    SDKs do) is not quarantined, but one saved by a browser is, and macOS will
    refuse it until you clear the flag:
    ```bash
    xattr -d com.apple.quarantine ryskV12
    ```

## Environment

| Variable | Used by | Purpose |
| --- | --- | --- |
| `RYSK_PRIVATE_KEY` | every command that signs | Supplies `--private_key`, so the key stays out of `argv` where `ps` and shell history can see it. The flag still works and takes precedence. |
| `RYSK_PREMIUM_URL` | `premium` | Supplies `--url`, so a staging or local api is pointed at once instead of on every call. |

```sh
export RYSK_PRIVATE_KEY=<hex private key>
./ryskV12 premium quotes --maker 0x...
```

## Usage

The `ryskV12` CLI provides the following commands:

### `approve`

Approves spending of the default strike asset for a given account.

```bash
./ryskV12 approve --chain_id <chain_id> --rpc_url <rpc_url> --amount <amount> --private_key <private_key>
```

Flags

- `--chain_id` (**required**): The ID of the blockchain.
- `--rpc_url` (**required**): The URL of the Ethereum RPC endpoint.
- `--amount` (**required**): The amount of the asset to approve for spending.
- `--private_key` (**required**): The private key of the Ethereum account performing the approval.

---

### `balances`

Retrieves USDC balances for the specified account.

```bash
./ryskV12 balances --channel_id <channel_id> --account <0xabc>
```

Flags

- `--account` (**required**): The address to query data for.
- `--channel_id` (**required**): Unique ID for the connection and named pipe (/tmp/<channel_id>).

---

### `connect`

Establishes a WebSocket connection and runs in daemon mode with a named pipe.

```bash
./ryskV12 connect --channel_id <channel_id> --url <websocket_url>
```

Flags

- `--channel_id` (**required**): Unique ID for the connection and named pipe (/tmp/<channel_id>).
- `--url` (**required**): WebSocket URL to connect to.

Endpoints:

- `wss://<base_url>/rfqs/<asset_address>` listen for rfqs for the specified asset
- `wss://<base_url>/maker` endpoint to send quotes and transfer requests

---

### `positions`

Retrieves positions (oToken details) for the specified account
```bash
./ryskV12 positions --channel_id <channel_id> --account <0xabc>
```

Flags

- `--account` (**required**): The address to query data for.
- `--channel_id` (**required**): Unique ID for the connection and named pipe (/tmp/<channel_id>).

Endpoints:

- `wss://<base_url>/rfqs/<asset_address>` listen for rfqs for the specified asset
- `wss://<base_url>/maker` endpoint to send quotes and transfer requests

---

### `quote`

Sends a signed quote for options trading through the WebSocket.

```bash
./ryskV12 quote --channel_id <channel_id> --rfq_id <rfq_id> --chain_id <chain_id> --expiry <expiry_timestamp>  --maker <maker_address> --nonce <nonce> --price <price> --quantity <quantity> --strike <strike> --valid_until <valid_until_timestamp> --collateral <collateral> --private_key <private_key> --is_put --is_taker_buy
```

Flags

- `--channel_id` (**required**): The unique ID of the WebSocket connection.
- `--rfq_id` (**required**): The unique ID of the rfq you are quoting for.
- `--chain_id` (**required**): The ID of the blockchain.
- `--expiry` (**required**): Option expiry timestamp.
- `--is_put`: present for put, not for call.
- `--is_taker_buy`: present if maker sells, not if maker buys.
- `--maker` (**required**): Address of the quote maker.
- `--nonce` (**required**): Unique nonce for the quote (stringified u64).
- `--price` (**required**): Option price.
- `--quantity` (**required**): Option quantity.
- `--strike` (**required**): Option strike price.
- `--valid_until` (**required**): Quote validity timestamp.
- `--collateral` (**required**): Accepted collateral asset.
- `--private_key` (**required**): Private key for signing.
- `--premium_asset`: Asset the premium is paid in. Left out of the quote when
  not set, and never part of the signature.

The EIP712 domain the quote is signed against defaults to `name: rysk`,
`version: 0.0.0`, `chainId: --chain_id` and the Rysk contract on that chain.
The domain flags are all or nothing: pass all three or none. `chainId` always
follows `--chain_id`.

- `--domain_name`: EIP712 domain name.
- `--domain_version`: EIP712 domain version.
- `--domain_verifying_contract`: EIP712 domain verifying contract.

```bash
./ryskV12 quote ... \
  --domain_name rysk \
  --domain_version 0.0.0 \
  --domain_verifying_contract 0x0ff34dd648b68f09b199b60b91442e750fd13fdc
```

---

### `premium`

Maker actions against the premium RFQ api (`https://premium.rysk.finance`, override with `--url`).
These are plain HTTP calls, so they need no `connect` and no channel: each subcommand does one
request and prints the api's response on stdout as it came. Set `RYSK_PREMIUM_URL` instead of passing
`--url` every time; a host that answers but has no RFQ routes is reported as such rather than as a
missing id.

```bash
./ryskV12 premium requests --maker <0xmaker>
./ryskV12 premium quotes --maker <0xmaker>
./ryskV12 premium quote-status --id <quote_id>
./ryskV12 premium quote --request_id <id> ... --domain_verifying_contract <0xoption_handler>
./ryskV12 premium cancel --id <quote_id> --chain_id <chain_id> --nonce <nonce> --private_key <pk>
```

| Subcommand | Route |
| --- | --- |
| `requests` | `GET /api/requests/maker?address=` — the requests this maker may quote, each with the `typeDataDomain` to sign against |
| `quotes` | `GET /api/quotes?address=` — this maker's live quotes, and the only place quote ids come from |
| `quote-status` | `GET /api/quotes/{id}` — one quote, whatever its status |
| `quote` | `POST /api/quotes` — sign and post one quote, or a batch |
| `cancel` | `DELETE /api/quotes/{id}` — pull a quote, authenticated with EIP712 headers |

#### `premium quote`

Takes the option terms as flags, exactly as the request carried them: `--request_id`, `--asset`,
`--chain_id`, `--expiry`, `--is_put`, `--is_taker_buy`, `--strike`, `--quantity`, `--usd`,
`--collateral`, plus your own `--maker`, `--nonce`, `--price`, `--valid_until` and `--private_key`.

Only `requestId`, `maker`, `price`, `nonce`, `validUntil` and `signature` are sent — the api rebuilds
the signed terms from the stored request, so a term that does not match it just yields a signature
that will not verify.

The domain is the pool's option handler, not the Rysk contract the websocket `quote` command uses:

- `--domain_verifying_contract` (**required**): the request's `typeDataDomain.verifyingContract`.
- `--domain_name`: defaults to `PremiumOptionHandler`.
- `--domain_version`: defaults to `1`.

`--valid_until` is unix **seconds** and has to sit strictly between now+2min and now+10min — the api
rejects anything else, and the command checks it before signing. Request `validUntil` values are
milliseconds; passing one here is rejected as such.

Batch a whole strip into one call with `--batch <file>` (or `--batch -` for stdin), a json array of
quotes. Each entry is a quote plus `requestId` and `domain`; `--batch` cannot be combined with the
per quote flags, and one invalid entry fails the command before anything is posted:

```json
[
  {
    "requestId": "b7c2-uuid",
    "assetAddress": "0x...",
    "chainId": 84532,
    "expiry": 1767225600,
    "isPut": true,
    "isTakerBuy": false,
    "strike": "300000000000",
    "quantity": "1000000000000000000",
    "usd": "0x...",
    "collateralAsset": "0x...",
    "maker": "0x...",
    "nonce": "42",
    "price": "1250000000000000000",
    "validUntil": 1767139500,
    "domain": { "verifyingContract": "0x..." }
  }
]
```

Posting quotes always answers `200`, with rejections in a `failures` array. The command prints the
response either way and exits non zero when `failures` is not empty, so a rejected quote cannot be
mistaken for a stored one. An empty response body is treated as a failure too — the api recovers
panics without writing a status.

#### Nonces

`nonce` is a decimal uint64 and is spent once, whatever it was used for: quote nonces and the
`cancel` auth nonce share one keyspace per address. Draw them from a single persisted counter per
signing key — the CLI holds no state between invocations.

### `transfer`

Requests a transfer (deposit or withdrawal) through the WebSocket.

```bash
./ryskV12 transfer --channel_id <channel_id> --chain_id <chain_id> --user <user_address> --asset <asset_address> --amount <amount> --nonce <nonce> --private_key <private_key> --is_deposit
```

Flags

- `--channel_id` (**required**): The unique ID of the WebSocket connection (matches connect --channel_id).
- `--chain_id` (**required**): The ID of the blockchain for the transfer.
- `--user` (**required**): THe address of the account executing the deposit.
- `--asset` (**required**): The address of the asset being transferred.
- `--amount` (**required**): The amount to transfer.
- `--is_deposit`: present if deposit, not for withdrawal.
- `--nonce` (**required**): A unique nonce for signing (stringified u64).
- `--private_key` (**required**): The private key for signing.
