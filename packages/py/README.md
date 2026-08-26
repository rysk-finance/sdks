# Rysk V12 Python SDK

Python wrapper for ryskV12 cli

## Setup

1.  **Ensure Python 3.11+ is installed.**
2.  **Install the required dependencies:**
    ```bash
    poetry add ryskV12
    ```
3.  **Download the `ryskV12` CLI:**
    Navigate to https://github.com/rysk-finance/ryskV12/releases and download the latest release in the root directory of your project as `ryskV12`.

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

```python

def response_handler(response: bytes):
    print(f"Received response: {response.strip()}")


channel_id = "rfqs_listener"
uri = "/rfqs/0x..."  # Example websocket endpoint (replace with actual asset address)

proc = rysk_sdk.execute_async(rysk_sdk.connect_args(channel_id, uri), response_handler)
```

### Approve USDC Spending

```python
chain_id = 84532
amount = "1000000"
rpc_url= "https://rpc..."

proc = rysk_sdk.execute(rysk_sdk.approve_args(chain_id, amount, rpc_url))
```

### List USDC Balances

```python
maker_channel = "maker-channel"
account = "0xabc"

proc = rysk_sdk.execute(rysk_sdk.balances_args(maker_channel, account))
```

### Deposit / Withdraw

```python
from ryskV12.models import Transfer

maker_channel = "maker-channel"
transfer_details = Transfer(
    amout="500000",
    asset="0x...",  # The asset address
    chain_id=84532,
    is_deposit=True,
    nonce="some-unique-nonce",
)
proc = rysk_sdk.execute(rysk_sdk.transfer_args(maker_channel, transfer_details))
```

### List Positions

```python
maker_channel = "maker-channel"
account = "0xabc"

proc = rysk_sdk.execute(rysk_sdk.positions_args(maker_channel, account))
```

### Send a Quote

```python
from ryskV12.models import Quote

maker_channel = "maker-channel"
request_id = "some-uuid-from-server"
quote_details = Quote(
    assetAddress="0x...",
    chainId=84532,
    expiry=1678886400,
    isPut=False,
    isTakerBuy=True,
    maker="0x...",
    nonce="another-unique-nonce",
    price="0.01",
    quantity="1",
    strike="1000000",
    validUntil=1678886460,
    usd="0x...",
    collateralAsset="0x...",
    # optional: asset the premium is paid in. Sent with the quote, not signed.
    premiumAsset=request.premiumAsset,
    # optional: sign against the domain the request asks for. Omit to use the
    # default domain for the chain.
    domain=request.typeDataDomain,
)

proc = rysk_sdk.execute(rysk_sdk.quote_args(maker_channel, request_id, quote_details))
```

`domain` needs `name`, `version` and `verifyingContract` together; its `chainId`
always comes from the quote, so a domain for another chain - or one carrying a
`salt` - raises instead of being signed against the wrong domain.

### Premium RFQ (maker)

The premium RFQ api is plain HTTP, so these need no connection and no channel: each one runs the CLI
once and prints the api's response on stdout. `url` is optional — leave it out for production, pass
it for a local or staging api.

```python
from dataclasses import replace

# requests this maker may quote, each with the domain to sign against
proc = rysk_sdk.execute(rysk_sdk.premium_requests_args(maker))

# sign and post one quote for a request
quote = replace(
    quote_details,
    maker=maker,
    nonce="42",                          # decimal uint64, one counter per key
    price="1250000000000000000",         # 1e18
    validUntil=int(time.time()) + 300,   # SECONDS, now+2min .. now+10min
    domain=request.typeDataDomain,       # the pool's option handler
)
proc = rysk_sdk.execute(rysk_sdk.premium_quote_args(request.id, quote))

# a whole strip in one api call
with open("batch.json", "w") as f:
    f.write(rysk_sdk.premium_quote_batch([(request.id, quote_a), (request.id, quote_b)]))
rysk_sdk.execute(rysk_sdk.premium_quote_batch_args("batch.json"))

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

## Examples

Two runnable maker loops live in `tests/`, one per transport:

| File | What it does |
| --- | --- |
| `tests/run_example.py` | Websocket flow: holds the maker connection open, listens for RFQs on an asset, prices them and sends signed quotes back through the maker channel. Also reads balances and positions, and disconnects both channels on `SIGINT`. |
| `tests/run_premium_example.py` | Premium RFQ flow: polls the requests this maker may quote, prices them, posts the batch in one api call, matches quote ids back from the listing, refreshes each quote before its 10 minute window closes, and pulls everything on `SIGINT`. |

```sh
RYSK_SDK_PK=<hex private key> RYSK_MAKER=0x<maker address> python tests/run_example.py

RYSK_SDK_PK=<hex private key> RYSK_MAKER=0x<maker address> \
  PREMIUM_URL=https://insti-testnet.rysk.finance python tests/run_premium_example.py
```

Both keep their nonce counter in a `.rysk-nonce` file, because a nonce is spent once per address and a
counter that rewinds after a restart starts failing every write. `price_request` is the only part meant
to be replaced — everything else is the plumbing the api expects.
