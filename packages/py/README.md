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

### Instantiation

```python
from ryskV12.client import Rysk, Env

private_key = "YOUR_PRIVATE_KEY"
env = Env.TESTNET
rysk_sdk = Rysk(env=env, private_key=private_key, v12_cli_path="/path/to/ryskV12")  # Optional CLI path
```

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

## Example

```python
import asyncio
import json
import time
from ryskV12.models import Quote, Request, is_json_rpc_response, is_request
from ryskV12.client import Rysk, Env

private_key = ""
public_address = ""
asset = "0xb67bfa7b488df4f2efa874f4e59242e9130ae61f"

def price_it(public_address: str, req: Request) -> Quote:
    # ...your magic goes here
    price = 4
    return Quote(
        assetAddress=req.asset,
        chainId=req.chainId,
        expiry=req.expiry,
        isPut=req.isPut,
        isTakerBuy=False,
        maker=public_address,
        nonce=str(int(time.time() * 1000)),
        price=f"{price}000000000000000000",
        quantity=req.quantity,
        strike=req.strike,
        validUntil=int(time.time()) + 30,
        usd=req.usd,
        collateralAsset=req.collateralAsset,
        premiumAsset=req.premiumAsset,
        domain=req.typeDataDomain,
    )

async def process_rfqs():
    rysk_sdk = Rysk(env=Env.TESTNET, private_key=private_key)
    maker_chan = "maker__py"
    rfq_chan = f'{asset}__py'
    try:
        asyncio.create_task(rysk_sdk.execute_async(rysk_sdk.connect_args(maker_chan, "maker")))
        def process_rfq(payload: bytes):
            # print(payload)
            if payload == b'\n':
                return
            try:
                data = json.loads(payload)
                if is_json_rpc_response(data):
                    request_id = data["id"]
                    result = data["result"]
                    if is_request(result):
                        quote = price_it(public_address, Request.from_json(json.dumps(result)))
                        cmd = rysk_sdk.quote_args(maker_chan, request_id, quote)
                        proc = rysk_sdk.execute(cmd)
                        print(proc.stdout.readlines())
                        print(proc.stderr.readlines())

            except Exception as e:
                print("error")
                print(e)

        await rysk_sdk.execute_async(rysk_sdk.connect_args(rfq_chan, f"rfqs/{asset}"), process_rfq)
    except Exception as e:
        print(e)
    finally:
        rysk_sdk.execute(rysk_sdk.disconnect_args(maker_chan))
        rysk_sdk.execute(rysk_sdk.disconnect_args(rfq_chan))

def handle_sig(sig, frame):
    os.remove(f"/tmp/{asset}__py.sock")
    os.remove("/tmp/maker__py.sock")
    sys.exit(0)

if __name__ == "__main__":
    signal.signal(signal.SIGINT, handle_sig)
    asyncio.run(process_rfqs())

```
