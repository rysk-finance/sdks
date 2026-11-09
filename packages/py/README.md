# Rysk V12 Python SDK

Python wrapper for ryskV12 cli

Integration guides: [V12 websocket auction](docs/v12.md) · [Premium RFQ](docs/premium.md).

## Setup

1.  **Ensure Python 3.11+ is installed.**
2.  **Install the required dependencies:**
    ```bash
    poetry add ryskV12
    ```
3.  **Get the `ryskV12` CLI:** call `Rysk(...).setup()` (see below), or download the newest `cli-v*`
    release from https://github.com/rysk-finance/sdks/releases into your project as `ryskV12`.

## Run

### The private key never reaches argv

`execute` and `execute_async` hand the key to the CLI through `RYSK_PRIVATE_KEY` in the child's
environment, so it does not show up in `ps` or a shell history, and the `*_args` builders do not
contain it. If you spawn the CLI yourself, set that variable too, or the CLI will stop with
`Required flag "private_key" not set`.

### Instantiation

```python
from ryskV12.client import Rysk, Env

private_key = "YOUR_PRIVATE_KEY"
rysk_sdk = Rysk(env=Env.TESTNET, private_key=private_key, v12_cli_path="./ryskV12")

# strict_version raises instead of warning when the cli is older than this sdk
rysk_sdk = Rysk(env=Env.TESTNET, private_key=private_key, strict_version=True)

# and if you have no cli yet, setup() downloads one and returns its path
cli_path = Rysk(env=Env.TESTNET, private_key=private_key).setup()
```

The constructor checks the CLI's version (major, minor and patch) and warns when it is older than the
SDK needs, because commands added since then fail with `flag provided but not defined`. A locally
built CLI reports no version number and is left alone.

### Create a Connection

`execute_async` spawns `connect` and returns the process straight away, handing each line the server
sends to your callback in the background. `await proc.wait()` for the exit code.

```python

def response_handler(response: bytes):
    print(f"Received response: {response.strip()}")


uri = "rfqs/0x..."  # Example websocket endpoint (replace with actual asset address)

proc = await rysk_sdk.execute_async(rysk_sdk.connect_args(uri), response_handler)
```

### Send Commands Into a Connection

Balances, positions, transfers and quotes are written into a running `connect` with `send`, which
signs and sends them over its websocket - no process per command. The server's answers arrive on that
connection's callback, and so does a command the CLI rejects: a json-rpc error under the id the server
would have used, which `is_error_data` catches with the server's own.

```python
maker = await rysk_sdk.execute_async(rysk_sdk.connect_args("maker"), response_handler)
rysk_sdk.send(maker, rysk_sdk.balances_args("0xabc"))
```

`send` also takes a process from `execute`.

### Disconnect

```python
maker.terminate()
```

### Approve Spending

Omit the asset and the chain's strike asset is approved, as before:

```python
chain_id = 84532
amount = "1000000"
rpc_url= "https://rpc..."

proc = rysk_sdk.execute(rysk_sdk.approve_args(chain_id, amount, rpc_url))
```

Pass one to approve any other erc20:

```python
proc = rysk_sdk.execute(
    rysk_sdk.approve_args(chain_id, amount, rpc_url, "0xabc...")
)
```

### List USDC Balances

```python
account = "0xabc"

rysk_sdk.send(maker, rysk_sdk.balances_args(account))
```

### Deposit / Withdraw

```python
from ryskV12.models import Transfer

transfer_details = Transfer(
    user="0xabc",  # the maker
    amount="500000",
    asset="0x...",  # The asset address
    chain_id=84532,
    is_deposit=True,
    nonce="some-unique-nonce",
)
rysk_sdk.send(maker, rysk_sdk.transfer_args(transfer_details))
```

### List Positions

```python
account = "0xabc"

rysk_sdk.send(maker, rysk_sdk.positions_args(account))
```

### Redemption

```python
# opt an otoken in (True) or out (False) of redemption at expiry, over the maker connection
rysk_sdk.send(maker, rysk_sdk.will_redeem_args(84532, account, "0xotoken", True, "44"))

# redeem your own expired otokens now - a plain http call to this env's api
rysk_sdk.execute(rysk_sdk.self_redeem_args(84532, "45"))
# or up to an expiry, skipping some assets
rysk_sdk.execute(rysk_sdk.self_redeem_args(84532, "46", expiry=1767225600, exclude_assets=["0xasset"]))
```

### Send a Quote

```python
from ryskV12.models import Quote

request_id = "some-uuid-from-server"  # the rfq id; request is the rfq itself
quote_details = Quote(
    assetAddress="0x...",
    chainId=84532,
    expiry=1678886400,
    isPut=False,
    isTakerBuy=True,
    maker="0x...",
    nonce="another-unique-nonce",
    price="1250000000000000000",  # USD per option, 1e18
    quantity="1000000000000000000",  # 1e18
    strike="350000000000",  # 1e8
    validUntil=1678886460,
    usd="0x...",
    collateralAsset="0x...",
    # optional: asset the premium is paid in. Sent with the quote, not signed.
    premiumAsset=request.premiumAsset,
    # optional: sign against the domain the request asks for. Omit to use the
    # default domain for the chain.
    domain=request.typeDataDomain,
)

rysk_sdk.send(maker, rysk_sdk.quote_args(request_id, quote_details))
```

`domain` needs `name`, `version` and `verifyingContract` together; its `chainId`
always comes from the quote, so a domain for another chain - or one carrying a
`salt` - raises instead of being signed against the wrong domain.

### Premium RFQ (maker)

The premium RFQ api is plain HTTP, so these need no connection: each one runs the CLI
once and prints the api's response on stdout. `url` is optional — leave it out for production, pass
it for a local or staging api.

```python
from dataclasses import replace

# requests this maker may quote, each with the domain to sign against
proc = rysk_sdk.execute(rysk_sdk.premium_requests_args(maker))

# sign and post quotes: one or a whole strip, in one api call
quote = replace(
    quote_details,
    maker=maker,
    nonce="42",                          # decimal uint64, one counter per key
    price="1250000000000000000",         # 1e18
    validUntil=int(time.time()) + 300,   # SECONDS, now+2min .. now+10min
    domain=request.typeDataDomain,       # the pool's option handler
)
with open("batch.json", "w") as f:
    f.write(rysk_sdk.premium_quote_batch([(request.id, quote)]))
proc = rysk_sdk.execute(rysk_sdk.premium_quote_batch_args("batch.json"))

# your live quotes (the only source of quote ids), one quote, and a cancel
rysk_sdk.execute(rysk_sdk.premium_quotes_args(maker))
rysk_sdk.execute(rysk_sdk.premium_quote_status_args(quote_id))
rysk_sdk.execute(rysk_sdk.premium_cancel_args(quote_id, chain_id, "43"))
```

Terms have to be the request's — the api rebuilds the signed message from the stored request, so an
altered term just yields a signature that will not verify. `domain` needs `verifyingContract` (the
CLI defaults name and version); a domain with a `salt` or another chain's `chainId` raises here
rather than being signed.

Posting quotes always answers `200`, with rejections in a `failures` array. The CLI prints the
response and exits non zero when that array is not empty, so check the return code, not the status.

Nonces are spent once and share one keyspace per address across quotes and cancels — draw them from a
single persisted counter.

### Premium RFQ (taker)

A request's taker is a pool; the SDK's key has to be one of the pool's on chain RFQ signers.

```python
rysk_sdk.execute(rysk_sdk.premium_pools_args())

# a json array of requests as the api takes them; answers with no body
with open("requests.json", "w") as f:
    json.dump([request], f)
rysk_sdk.execute(rysk_sdk.premium_request_args("requests.json", chain_id, "44"))

rysk_sdk.execute(rysk_sdk.premium_taker_requests_args(pool))         # the pool's live requests
rysk_sdk.execute(rysk_sdk.premium_request_quotes_args(request_id))   # the quotes on one
rysk_sdk.execute(rysk_sdk.premium_cancel_request_args(request_id, chain_id, "45"))

# take a quote: prints the settling transaction's hash
rysk_sdk.execute(rysk_sdk.premium_execute_args(quote_id, "46"))
```

See the CLI README for the request fields; note a request's `validUntil` is in **milliseconds**.

## Tests

```sh
poetry install
poetry run pytest                 # unit tests
make dev-bin && poetry run pytest # + the integration tests, which need a cli
```

`tests/integration` runs the real CLI against a fake api, so the arg builders and the binary cannot
drift apart. Those tests skip themselves when no CLI is present.

The `testnet` test at the bottom of `tests/integration/test_premium_cli.py` is the full premium flow end to end
against the real testnet api — a taker opens an rfq, the maker quotes, cancels and requotes, and
the taker takes the trade on chain. Read it as the worked example. It skips unless the e2e keys are
set; `make e2e` from the repo root loads them from `.env`.
