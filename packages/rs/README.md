# ryskv12

Rust sdk to interact with [Rysk v12](https://rysk.finance).

Like the typescript and python sdks, this one does not talk to the api itself.
It builds argument lists for the `ryskV12` cli and spawns it, so the cli keeps
ownership of the signing key, the websocket and the eip712 signing, and all
three sdks agree on flags by construction.

Integration guides: [V12 websocket auction](docs/v12.md) · [Premium RFQ](docs/premium.md).

## Install

```toml
[dependencies]
ryskv12 = "5"
```

The cli binary is not bundled. Either download it from
[releases](https://github.com/rysk-finance/sdks/releases) into your working
directory as `ryskV12`, or let the sdk fetch it once at startup:

```rust
let sdk = ryskv12::Rysk::new(ryskv12::Env::Testnet, private_key)?;
sdk.setup()?; // blocking: downloads ./ryskV12 and makes it executable
```

## Quoting

```rust
use ryskv12::{Env, NonceCounter, Quote, Rysk, TypedDataDomain};

let sdk = Rysk::builder(Env::Testnet, private_key)
    .cli_path("./ryskV12")
    .strict_version(true) // fail rather than warn on an old cli
    .build()?;

let mut nonces = NonceCounter::default();

let quote = Quote {
    asset_address: request.asset.clone(),
    chain_id: request.chain_id,
    expiry: request.expiry,
    is_put: request.is_put,
    is_taker_buy: request.is_taker_buy.unwrap_or(false),
    maker: maker.clone(),
    nonce: nonces.next()?,
    price: "1250000000000000000".into(),
    quantity: request.quantity.clone(),
    strike: request.strike.clone(),
    valid_until: now_seconds + 300,
    usd: request.usd.clone(),
    collateral_asset: request.collateral_asset.clone(),
    premium_asset: None,
    domain: request.type_data_domain.clone(),
};

// quotes are posted as a batch: a json array, from a file or `-` for stdin
std::fs::write("quotes.json", sdk.premium_quote_batch(&[(request_id, quote)])?)?;
let args = sdk.premium_quote_batch_args("quotes.json", None);
let status = sdk.execute_lines(&args, |line| println!("{line}"))?;
```

Every `*_args` method is pure: it returns what the cli would be given without
running anything, so you can assert on the whole surface without a binary.

### Taking (premium rfq)

A request's taker is a pool; the sdk's key has to be one of the pool's on chain
rfq signers. A request's `validUntil` is in **milliseconds**; see the cli README
for its fields.

```rust
sdk.premium_pools_args(None);
// a json array of requests as the api takes them; answers with no body
sdk.premium_request_args("requests.json", chain_id, &nonces.next()?, None);
sdk.premium_taker_requests_args(&pool, None); // the pool's live requests
sdk.premium_request_quotes_args(&request_id, None); // the quotes on one
sdk.premium_cancel_request_args(&request_id, chain_id, &nonces.next()?, None);
// take a quote: prints the settling transaction's hash
sdk.premium_execute_args(&quote_id, &nonces.next()?, None);
```

## The websocket flow

Spawn `connect` once per endpoint and keep it. Websocket commands - quotes,
transfers, balances, positions, `will_redeem_args` - are written to its stdin with `ryskv12::send`: the
running cli signs each one and sends it, no process per command.

```rust
use std::io::{BufRead, BufReader};

let mut maker = sdk.execute(&sdk.connect_args("maker"))?;
let mut commands = maker.stdin.take().unwrap();
let replies = BufReader::new(maker.stdout.take().unwrap());

ryskv12::send(&mut commands, &sdk.balances_args(&maker_address))?;
ryskv12::send(&mut commands, &sdk.quote_args(&rfq_id, &quote)?)?;
for line in replies.lines() {
    println!("{}", line?); // one json-rpc message per line
}
```

Redeeming your own expired otokens is a plain http call to this env's api, run
on its own rather than sent into a connection:

```rust
let out = sdk.execute(&sdk.self_redeem_args(84532, "45", None, &[], None))?.wait_with_output()?;

maker.kill()?; // disconnect
```

A listen-only connection, like `connect_args(&format!("rfqs/{asset}"))`, can go
through `execute_lines` instead. A command the cli rejects comes back on
stdout as a json-rpc error under the id the server would have used; the
connection stays up.

## Approving and depositing

Leave the asset out and the cli falls back to the chain's strike asset:

```rust
sdk.execute(&sdk.approve_args(84532, "1000000", rpc_url, None))?;
```

Pass one to approve or deposit any erc20:

```rust
sdk.execute(&sdk.approve_args(84532, "1000000", rpc_url, Some(asset)))?;
```

## Reading what comes back

The cli writes one json-rpc message per line. A failed call carries `error`
instead of `result`, so read that first:

```rust
use ryskv12::{JsonRpcResponse, Request};

let res = JsonRpcResponse::from_json(line)?;
if let Some(err) = &res.error {
    eprintln!("rpc {} failed: {}", res.id, err.message);
} else if let Some(request) = res.result_as::<Request>() {
    // quote it
}
```

`result_as` is this sdk's answer to the `is_request` / `is_quote_notification`
predicates the other two hand-write: deserialisation already checks the shape.

## Nonces

A nonce is spent once per address, and the api keys them on (address, nonce)
alone, so quotes, cancels and confirmations from one signing key all draw from
the same sequence. `NonceCounter` persists to a file so a restart cannot
rewind into nonces the api has already seen.

## Development

From the repo root:

```sh
make dev-bin   # builds the cli into every sdk package
make test-rs   # cargo test
```

The integration tests in `tests/cli.rs` drive the real binary against a fake
api. They skip themselves when no cli is present.
