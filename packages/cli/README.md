# Rysk V12 CLI

A command-line interface (CLI) for interacting with the Rysk v12 protocol via WebSockets.

This CLI allows you to connect to a WebSocket server, send signed messages for actions like approving token spending, initiating transfers, and sending quotes. A running `connect` takes its commands on stdin and signs and sends them itself, so nothing is spawned per message.

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
| `RYSK_URL` | `self-redeem` | Supplies `--url`, the v12 api (`https://v12.rysk.finance` by default). |

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
- `--asset`: The asset to approve; defaults to the chain's strike asset.
- `--spender`: Who may spend it; defaults to the chain's mmarket, which maker deposits go through. A v12
  taker approves its collateral to the chain's margin pool instead.

---

### `balances`

Retrieves USDC balances for the specified account.

```json
["balances", "--account", "<0xabc>"]
```

Flags

- `--account` (**required**): The address to query data for.

---

### `connect`

Establishes a WebSocket connection. Every message from the server is printed to stdout, one per line.

```bash
./ryskV12 connect --url <websocket_url>
```

Flags

- `--url` (**required**): WebSocket URL to connect to.

Commands for the connection - `balances`, `positions`, `quote`, `transfer` and `will_redeem` for a maker,
`inventory`, `request` and `confirm` for a taker - are written to its
stdin, one json array of args per line, exactly as they would follow `./ryskV12` on the command line.
They run in order inside the connect process, so nothing is spawned per message:

```bash
echo '["balances", "--account", "0xabc"]' | ./ryskV12 connect --url wss://<base_url>/maker
```

A command that fails is reported on stdout as a json-rpc error, under the id the server would have
answered with (the `--rfq_id` for a quote, the command name otherwise), and the connection carries on.
Every message, the server's or the CLI's, is exactly one line. Closing stdin stops commands
but not the connection, so a listen-only `connect` can run with stdin at `/dev/null`. Kill the process
(`SIGTERM`) to disconnect. Run in a background job of an interactive shell, redirect stdin
(`< /dev/null` or a fifo), or reading the terminal stops the process.

Run on its own, a connection command prints its signed json-rpc message instead of sending it.

Endpoints:

- `wss://<base_url>/rfqs/<asset_address>` listen for rfqs for the specified asset
- `wss://<base_url>/maker` endpoint to send quotes and transfer requests
- `wss://<base_url>/taker` endpoint to request rfqs and confirm them

The v12 taker commands, on a `/taker` connection (see [`docs/v12.md`](../../docs/v12.md) for the flow):

```json
["inventory"]
["request", "--asset", "0x…", "--chain_id", "8453", "--expiry", "<listed expiry>", "--strike", "<listed strike, 1e8>", "--quantity", "<1e18>", "--taker", "<0xtaker>", "--usd", "0x…", "--collateral", "0x…"]
["confirm", "--quote", "<the request's answer, as json>", "--nonce", "<n>"]
```

`request` is answered with the auction's winning quote, under id `request`. `confirm` reads the gas fee
from `/api/gas` on the connect's host (override with `--api_url`), signs the confirmation with
`RYSK_PRIVATE_KEY` and is answered with the settling transaction's hash, under id `confirm`.

---

### `positions`

Retrieves positions (oToken details) for the specified account
```json
["positions", "--account", "<0xabc>"]
```

Flags

- `--account` (**required**): The address to query data for.

Endpoints:

- `wss://<base_url>/rfqs/<asset_address>` listen for rfqs for the specified asset
- `wss://<base_url>/maker` endpoint to send quotes and transfer requests

---

### `quote`

Sends a signed quote for options trading through the WebSocket. Written to a `connect`'s stdin as a json array, shown here as command line args.

```bash
quote --rfq_id <rfq_id> --chain_id <chain_id> --expiry <expiry_timestamp>  --maker <maker_address> --nonce <nonce> --price <price> --quantity <quantity> --strike <strike> --valid_until <valid_until_timestamp> --collateral <collateral> --private_key <private_key> --is_put --is_taker_buy
```

Flags

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
quote ... \
  --domain_name rysk \
  --domain_version 0.0.0 \
  --domain_verifying_contract 0x0ff34dd648b68f09b199b60b91442e750fd13fdc
```

---

### `premium`

Maker and taker actions against the premium RFQ api (`https://premium.rysk.finance`, override with `--url`).
These are plain HTTP calls, so they need no `connect`: each subcommand does one
request and prints the api's response on stdout as it came. Set `RYSK_PREMIUM_URL` instead of passing
`--url` every time; a host that answers but has no RFQ routes is reported as such rather than as a
missing id.

```bash
./ryskV12 premium requests --maker <0xmaker>
./ryskV12 premium quotes --maker <0xmaker>
./ryskV12 premium quote-status --id <quote_id>
./ryskV12 premium quote --batch <file|-> --private_key <pk>
./ryskV12 premium cancel --id <quote_id> --chain_id <chain_id> --nonce <nonce> --private_key <pk>
```

| Subcommand | Route |
| --- | --- |
| `requests` | `GET /api/requests/maker?address=` — the requests this maker may quote, each with the `typeDataDomain` to sign against |
| `quotes` | `GET /api/quotes?address=` — this maker's live quotes, and the only place quote ids come from |
| `quote-status` | `GET /api/quotes/{id}` — one quote, whatever its status |
| `quote` | `POST /api/quotes` — sign and post a batch of quotes in one call |
| `cancel` | `DELETE /api/quotes/{id}` — pull a quote, authenticated with EIP712 headers |

#### `premium quote`

Signs and posts the quotes in `--batch <file>` (or `--batch -` for stdin): a json array, one entry per
quote. Each entry is the option terms exactly as the request carried them, your own `maker`, `nonce`,
`price` and `validUntil`, plus the `requestId` and the request's `typeDataDomain` as `domain`. One
invalid entry fails the command before anything is posted.

Only `requestId`, `maker`, `price`, `nonce`, `validUntil` and `signature` are sent — the api rebuilds
the signed terms from the stored request, so a term that does not match it just yields a signature
that will not verify.

The domain is the pool's option handler, not the Rysk contract the websocket `quote` command uses:
`domain.verifyingContract` is required; `name` defaults to `PremiumOptionHandler` and `version` to `1`.

`validUntil` is unix **seconds** and has to sit strictly between now+2min and now+10min — the api
rejects anything else, and the command checks it before signing. Request `validUntil` values are
milliseconds; passing one here is rejected as such.

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

#### Taker

A request's taker is always a pool, and the signing key has to be one of the pool's on chain RFQ
signers.

```bash
./ryskV12 premium pools
./ryskV12 premium request --batch <file|-> --chain_id <chain_id> --nonce <nonce> --private_key <pk>
./ryskV12 premium taker-requests --taker <0xpool>
./ryskV12 premium request-quotes --id <request_id>
./ryskV12 premium cancel-request --id <request_id> --chain_id <chain_id> --nonce <nonce> --private_key <pk>
./ryskV12 premium execute --quote_id <quote_id> --nonce <nonce> --private_key <pk>
```

| Subcommand | Route |
| --- | --- |
| `pools` | `GET /api/pools` — the pools requests can be opened on, with their option handler and assets |
| `request` | `POST /api/requests` — open the requests in a json array, sent as written, authenticated with EIP712 headers |
| `taker-requests` | `GET /api/requests/taker?address=` — a pool's live requests |
| `request-quotes` | `GET /api/requests/{id}/quotes` — the live quotes on one request |
| `cancel-request` | `DELETE /api/requests/{id}` — cancel a request and every quote on it, authenticated with EIP712 headers |
| `execute` | `POST /api/execute` — take a quote: sign the confirmation and have the api settle it on chain |

`request` answers `201` with no body; the ids are server assigned, so find a request through
`taker-requests`. Each entry carries `asset` (the pool's underlying), `chainId`, `expiry` (unix
seconds), `isPut`, `isTakerBuy`, `strike` (1e8), `quantity` (1e18), `taker` (the pool), `usd` (a
registered strike asset), `collateralAsset` and `premiumAsset` (pool assets), `isPremium: true`,
optionally `makers` to restrict who may quote, and `validUntil` in unix **milliseconds**.

`execute` takes only the quote id: it reads the quote, its request and the pool's option handler from
the api, signs the `Confirmation` under the handler's domain and prints the settling transaction's
hash. The trade is final once that transaction is mined. When the taker sells (`isTakerBuy` false)
the option handler pulls the premium from the maker, so the maker needs an allowance for it.

#### Nonces

`nonce` is a decimal uint64 and is spent once per address, whatever it was used for: quote nonces and
the auth nonces of `cancel`, `request` and `cancel-request` share one keyspace, and `execute`'s
confirmation nonce is checked the same way. Draw them from a single persisted counter per
signing key — the CLI holds no state between invocations.

### `transfer`

Requests a transfer (deposit or withdrawal) through the WebSocket. Written to a `connect`'s stdin as a json array, shown here as command line args.

```bash
transfer --chain_id <chain_id> --user <user_address> --asset <asset_address> --amount <amount> --nonce <nonce> --private_key <private_key> --is_deposit
```

Flags

- `--chain_id` (**required**): The ID of the blockchain for the transfer.
- `--user` (**required**): THe address of the account executing the deposit.
- `--asset` (**required**): The address of the asset being transferred.
- `--amount` (**required**): The amount to transfer.
- `--is_deposit`: present if deposit, not for withdrawal.
- `--nonce` (**required**): A unique nonce for signing (stringified u64).
- `--private_key` (**required**): The private key for signing.

---

### `will_redeem`

Opts an otoken in or out of redemption at expiry. Written to a `connect`'s stdin on the maker endpoint, shown here as command line args.

```bash
will_redeem --chain_id <chain_id> --account <maker_address> --otoken <otoken_address> --nonce <nonce> --private_key <private_key> --will_redeem
```

Flags

- `--chain_id` (**required**): The ID of the blockchain.
- `--account` (**required**): The maker address; has to match the signing key.
- `--otoken` (**required**): The otoken to opt in or out.
- `--will_redeem`: present to have the otoken redeemed at expiry, left out to keep the balance.
- `--nonce` (**required**): An unused nonce for this account (stringified u64).
- `--private_key` (**required**): The private key for signing.

---

### `self-redeem`

Redeems the signer's own expired otokens through the v12 api (`POST /api/redeem/self`). A plain HTTP
call, so it needs no `connect`. The redeemer is whoever signs the auth headers, so it can only ever
redeem for the caller. Prints the api's response.

```bash
./ryskV12 self-redeem --chain_id <chain_id> --nonce <nonce> [--expiry <unix_seconds>] [--exclude_assets <0xa,0xb>]
```

Flags

- `--chain_id` (**required**): The chain to redeem on.
- `--nonce` (**required**): An unused nonce for this account (decimal uint64), spent by the auth headers.
- `--expiry`: Cutoff in unix seconds; otokens expiring at or before it are redeemed. Defaults to now.
- `--exclude_assets`: Comma separated asset addresses to leave unredeemed.
- `--url`: The v12 api, `https://v12.rysk.finance` by default (or `RYSK_URL`).
- `--private_key` (**required**): The private key of the account redeeming.
