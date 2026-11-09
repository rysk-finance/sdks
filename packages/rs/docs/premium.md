# Premium RFQ integration (Rust)

The HTTP premium RFQ in Rust, for both sides. This page is the code;
[`docs/premium.md`](../../../docs/premium.md) is the protocol (fields, units,
funds, errors). Every call is one short CLI process: no `connect`, no `send`.

The worked example is the `testnet_open_an_rfq_quote_cancel_requote_and_take_the_trade`
test at the bottom of [`tests/cli.rs`](../tests/cli.rs): it runs this whole
flow against the real testnet api.

## Setup

```rust
use std::error::Error;
use std::time::{SystemTime, UNIX_EPOCH};
use ryskv12::{Env, NonceCounter, Quote, Request, Rysk};
use serde_json::{json, Value};

const URL: Option<&str> = Some("https://insti-testnet.rysk.finance"); // None for production

let maker = Rysk::new(Env::Testnet, MAKER_KEY)?;
let taker = Rysk::new(Env::Testnet, TAKER_KEY)?; // an rfq signer on the pool
let mut nonces = NonceCounter::default();         // one per key

fn run(sdk: &Rysk, args: &[String]) -> Result<String, Box<dyn Error>> {
    let out = sdk.execute(args)?.wait_with_output()?;
    if !out.status.success() {
        return Err(format!("{}: {}", args[1], String::from_utf8_lossy(&out.stderr).trim()).into());
    }
    Ok(String::from_utf8_lossy(&out.stdout).trim().to_string())
}

fn as_json(sdk: &Rysk, args: &[String]) -> Result<Vec<Value>, Box<dyn Error>> {
    let out = run(sdk, args)?;
    Ok(serde_json::from_str(if out.is_empty() { "[]" } else { &out })?)
}

// ids and nonces come back as strings or numbers
fn text(v: &Value) -> String {
    v.as_str().map(String::from).unwrap_or_else(|| v.to_string())
}

fn now_ms() -> i64 {
    SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_millis() as i64
}
```

The snippets run inside a `fn main() -> Result<(), Box<dyn Error>>`, with
`serde_json` as a dependency. Check the exit code, not the output: a quote post
answers `200` even when it rejects quotes, and the CLI exits non zero when it
does.

## 1. Taker: open a request

The taker is a pool. Build the request from the pool's own fields
([fields](../../../docs/premium.md#1-the-taker-opens-a-request)):

```rust
let pool = as_json(&taker, &taker.premium_pools_args(URL))?
    .into_iter()
    .find(|p| p["address"].as_str().is_some_and(|a| a.eq_ignore_ascii_case(POOL)))
    .ok_or("pool not listed")?;
let chain_id = pool["chainId"].as_i64().ok_or("pool chainId")?;

std::fs::write("requests.json", json!([{
    "taker": pool["address"],
    "asset": pool["underlying"],
    "chainId": chain_id,
    "isPut": pool["type"] == "Put",
    "collateralAsset": pool["asset"],
    "premiumAsset": pool["asset"],
    "usd": USD,                                   // the chain's strike asset
    "isTakerBuy": false,                          // the pool writes, the maker pays
    "strike": "350000000000",                     // 1e8
    "quantity": "1000000000000000000",            // 1e18
    "expiry": EXPIRY,                             // unix SECONDS
    "validUntil": now_ms() + 300_000,             // unix MILLISECONDS
    "isPremium": true,
    "makers": [MAKER],                            // optional
}]).to_string())?;

run(&taker, &taker.premium_request_args("requests.json", chain_id, &nonces.next()?, URL))?;
```

It answers with no body. Find the request's id through
`premium_taker_requests_args(&text(&pool["address"]), URL)`.

## 2. Maker: find requests

Poll; the api pushes nothing, and allows 30 requests a second per ip.

```rust
let requests: Vec<Request> = as_json(&maker, &maker.premium_requests_args(MAKER, URL))?
    .into_iter()
    .filter_map(|r| serde_json::from_value(r).ok()) // anything not a request drops out
    .collect();
```

## 3. Maker: quote

Repeat the request's terms exactly, add your own fields, and sign against the
request's `type_data_domain`
([rules](../../../docs/premium.md#3-the-maker-quotes)). One file posts one quote
or a whole strip:

```rust
fn quote_for(r: &Request, nonces: &mut NonceCounter) -> std::io::Result<Quote> {
    Ok(Quote {
        asset_address: r.asset.clone(), chain_id: r.chain_id, expiry: r.expiry,
        is_put: r.is_put, is_taker_buy: r.is_taker_buy.unwrap_or(false),
        strike: r.strike.clone(), quantity: r.quantity.clone(),
        usd: r.usd.clone(), collateral_asset: r.collateral_asset.clone(),
        maker: MAKER.into(),
        nonce: nonces.next()?,
        price: my_price(r),                       // 1e18 per option, non zero
        valid_until: now_ms() / 1000 + 300,       // SECONDS, now+2min .. now+10min
        domain: r.type_data_domain.clone(),       // the pool's option handler
        premium_asset: None,
    })
}

let mut batch = Vec::new();
for r in &requests {
    batch.push((r.id.clone().unwrap_or_default(), quote_for(r, &mut nonces)?));
}
std::fs::write("quotes.json", maker.premium_quote_batch(&batch)?)?;
run(&maker, &maker.premium_quote_batch_args("quotes.json", URL))?;
```

`premium_quote_batch` returns `Error::Domain` on a domain the CLI could not
sign (a `salt`, another chain) before anything is written. The post returns no
ids: match your live quotes on nonce.

```rust
let book = as_json(&maker, &maker.premium_quotes_args(MAKER, URL))?;
let (_, mine) = &batch[0];
let quote_id = book.iter()
    .find(|q| text(&q["nonce"]) == mine.nonce)
    .map(|q| text(&q["id"]))
    .ok_or("quote not on the book")?;

run(&maker, &maker.premium_quote_status_args(&quote_id, URL))?;
run(&maker, &maker.premium_trades_args(MAKER, Some(CHAIN_ID), None, URL))?; // settled fills
run(&maker, &maker.premium_cancel_args(&quote_id, CHAIN_ID, &nonces.next()?, URL))?;
```

A quote lives until taken, cancelled or past `valid_until`; requote inside the
10 minute window to keep a price standing.

To learn which quotes were filled, poll the settled trades, newest first. A
row carries the option, quantity and `txHash`, but not the quote id.

## 4. Taker: take a quote

```rust
let quotes = as_json(&taker, &taker.premium_request_quotes_args(&request_id, URL))?;
let best = quotes.iter()
    .max_by_key(|q| text(&q["price"]).parse::<u128>().unwrap_or(0))
    .ok_or("no quotes")?;
let tx = run(&taker, &taker.premium_execute_args(&text(&best["id"]), &nonces.next()?, URL))?; // tx hash
```

Or withdraw the request and every quote on it:

```rust
run(&taker, &taker.premium_cancel_request_args(&request_id, CHAIN_ID, &nonces.next()?, URL))?;
```

## Funds

When the pool writes (`isTakerBuy: false`) the maker pays the premium from its
wallet, pulled by the option handler. Approve it once, or execute reverts
([amounts](../../../docs/premium.md#funds)):

```rust
let mut approve = maker.approve_args(CHAIN_ID, amount, RPC_URL, Some(premium_asset));
approve.extend(["--spender".to_string(), option_handler.to_string()]);
run(&maker, &approve)?;
```

`option_handler` is the request's `type_data_domain.verifying_contract`.

## Nonces

Quote, cancel, request and confirmation nonces share one keyspace per address.
`NonceCounter` persists each one before handing it out (so `next()` returns an
`io::Result`); use one per key.
Errors and what they mean: [protocol](../../../docs/premium.md#errors-worth-knowing).
