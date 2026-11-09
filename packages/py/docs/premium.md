# Premium RFQ integration (Python)

The HTTP premium RFQ in Python, for both sides. This page is the code;
[`docs/premium.md`](../../../docs/premium.md) is the protocol (fields, units,
funds, errors). Every call is one short CLI process: no `connect`, no `send`.

The worked example is the `testnet` test at the bottom of
[`tests/integration/test_premium_cli.py`](../tests/integration/test_premium_cli.py):
it runs this whole flow against the real testnet api.

## Setup

```python
import json, time
from ryskV12.client import Rysk, Env
from ryskV12.models import Quote, Request, is_request
from ryskV12.nonce import NonceCounter

URL = "https://insti-testnet.rysk.finance"   # None for production

maker = Rysk(Env.TESTNET, private_key=MAKER_KEY)
taker = Rysk(Env.TESTNET, private_key=TAKER_KEY)   # an rfq signer on the pool
nonces = NonceCounter()                            # one per key

def run(sdk: Rysk, args) -> str:
    proc = sdk.execute(args)
    out, err = proc.communicate()
    if proc.returncode != 0:
        raise RuntimeError(f"{args[1]}: {err.strip()}")
    return out.strip()

def as_json(sdk: Rysk, args):
    return json.loads(run(sdk, args) or "[]")
```

Check the exit code, not the output: a quote post answers `200` even when it
rejects quotes, and the CLI exits non zero when it does.

## 1. Taker: open a request

The taker is a pool. Build the request from the pool's own fields
([fields](../../../docs/premium.md#1-the-taker-opens-a-request)):

```python
pool = next(p for p in as_json(taker, taker.premium_pools_args(URL)) if p["address"].lower() == POOL)

with open("requests.json", "w") as f:
    json.dump([{
        "taker": pool["address"],
        "asset": pool["underlying"],
        "chainId": pool["chainId"],
        "isPut": pool["type"] == "Put",
        "collateralAsset": pool["asset"],
        "premiumAsset": pool["asset"],
        "usd": USD,                                   # the chain's strike asset
        "isTakerBuy": False,                          # the pool writes, the maker pays
        "strike": "350000000000",                     # 1e8
        "quantity": "1000000000000000000",            # 1e18
        "expiry": EXPIRY,                             # unix SECONDS
        "validUntil": int(time.time() * 1000) + 300_000,  # unix MILLISECONDS
        "isPremium": True,
        "makers": [MAKER],                            # optional
    }], f)

run(taker, taker.premium_request_args("requests.json", pool["chainId"], nonces.next(), URL))
```

It answers with no body. Find the request's id through
`premium_taker_requests_args(pool["address"], URL)`.

## 2. Maker: find requests

Poll; the api pushes nothing, and allows 30 requests a second per ip.

```python
requests = [
    Request.from_json(r)
    for r in as_json(maker, maker.premium_requests_args(MAKER, URL))
    if is_request(r)
]
```

## 3. Maker: quote

Repeat the request's terms exactly, add your own fields, and sign against the
request's `typeDataDomain`
([rules](../../../docs/premium.md#3-the-maker-quotes)). One file posts one quote
or a whole strip:

```python
def quote_for(r: Request) -> Quote:
    return Quote(
        assetAddress=r.asset, chainId=r.chainId, expiry=r.expiry,
        isPut=r.isPut, isTakerBuy=bool(r.isTakerBuy),
        strike=r.strike, quantity=r.quantity,
        usd=r.usd, collateralAsset=r.collateralAsset,
        maker=MAKER,
        nonce=nonces.next(),
        price=my_price(r),                    # 1e18 per option, non zero
        validUntil=int(time.time()) + 300,    # SECONDS, now+2min .. now+10min
        domain=r.typeDataDomain,              # the pool's option handler
    )

batch = [(r.id, quote_for(r)) for r in requests]
with open("quotes.json", "w") as f:
    f.write(maker.premium_quote_batch(batch))
run(maker, maker.premium_quote_batch_args("quotes.json", URL))
```

`premium_quote_batch` raises on a domain the CLI could not sign (a `salt`,
another chain) before anything is written. The post returns no ids: match your
live quotes on nonce.

```python
book = as_json(maker, maker.premium_quotes_args(MAKER, URL))
_, mine = batch[0]
quote_id = next(str(q["id"]) for q in book if str(q["nonce"]) == mine.nonce)

run(maker, maker.premium_quote_status_args(quote_id, URL))
run(maker, maker.premium_cancel_args(quote_id, CHAIN_ID, nonces.next(), URL))
```

A quote lives until taken, cancelled or past `validUntil`; requote inside the
10 minute window to keep a price standing.

## 4. Taker: take a quote

```python
quotes = as_json(taker, taker.premium_request_quotes_args(request_id, URL))
best = max(quotes, key=lambda q: int(q["price"]))
tx = run(taker, taker.premium_execute_args(str(best["id"]), nonces.next(), URL))  # tx hash
```

Or withdraw the request and every quote on it:

```python
run(taker, taker.premium_cancel_request_args(request_id, CHAIN_ID, nonces.next(), URL))
```

## Funds

When the pool writes (`isTakerBuy: False`) the maker pays the premium from its
wallet, pulled by the option handler. Approve it once, or execute reverts
([amounts](../../../docs/premium.md#funds)):

```python
run(maker, maker.approve_args(CHAIN_ID, amount, RPC_URL, premium_asset) + ["--spender", option_handler])
```

`option_handler` is the request's `typeDataDomain.verifyingContract`.

## Nonces

Quote, cancel, request and confirmation nonces share one keyspace per address.
`NonceCounter` persists each one before handing it out; use one per key.
Errors and what they mean: [protocol](../../../docs/premium.md#errors-worth-knowing).
