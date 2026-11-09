# Premium RFQ integration (C++)

The HTTP premium RFQ in C++, for both sides. This page is the code;
[`docs/premium.md`](../../../docs/premium.md) is the protocol (fields, units,
funds, errors). Every call is one short CLI process (`run`): no `connect`, no
`send`.

The worked example is the `testnet` test at the bottom of
[`tests/cli_test.cpp`](../tests/cli_test.cpp): it runs this whole flow against
the real testnet api.

## Setup

```cpp
#include <algorithm>
#include <cctype>
#include <chrono>
#include <fstream>

#include "ryskv12/client.hpp"
#include "ryskv12/nonce.hpp"

using namespace ryskv12;

const std::optional<std::string> URL = "https://insti-testnet.rysk.finance";  // std::nullopt for production

Rysk maker(Env::Testnet, MAKER_KEY);
Rysk taker(Env::Testnet, TAKER_KEY);   // an rfq signer on the pool
NonceCounter nonces;                   // one per key

std::string ok(const Rysk& sdk, const std::vector<std::string>& args) {
  Output out = sdk.run(args);
  if (out.exit_code != 0) throw std::runtime_error(args[1] + ": " + out.err);
  return out.out.erase(out.out.find_last_not_of(" \t\r\n") + 1);
}

// the listing commands print a json array
Json::Array as_list(const Rysk& sdk, const std::vector<std::string>& args) {
  auto parsed = Json::parse(ok(sdk, args));
  return parsed && parsed->is_array() ? *parsed->as_array() : Json::Array{};
}

// ids and nonces come back as numbers or strings
std::string text(const Json& v) { return v.as_string().value_or(v.dump()); }

std::string lower(std::string s) {
  for (char& c : s) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
  return s;
}

std::int64_t now_ms() {
  using namespace std::chrono;
  return duration_cast<milliseconds>(system_clock::now().time_since_epoch()).count();
}
```

Check the exit code, not the output: a quote post answers `200` even when it
rejects quotes, and the CLI exits non zero when it does.

## 1. Taker: open a request

The taker is a pool. Build the request from the pool's own fields
([fields](../../../docs/premium.md#1-the-taker-opens-a-request)):

```cpp
Json pool;
for (const Json& p : as_list(taker, taker.premium_pools_args(URL)))
  if (lower(text(p["address"])) == POOL) pool = p;
std::int64_t chain_id = pool["chainId"].as_int().value_or(0);

Json::Object request{
    {"taker", pool["address"]},
    {"asset", pool["underlying"]},
    {"chainId", Json(chain_id)},
    {"isPut", Json(text(pool["type"]) == "Put")},
    {"collateralAsset", pool["asset"]},
    {"premiumAsset", pool["asset"]},
    {"usd", Json(USD)},                                 // the chain's strike asset
    {"isTakerBuy", Json(false)},                        // the pool writes, the maker pays
    {"strike", Json("350000000000")},                   // 1e8
    {"quantity", Json("1000000000000000000")},          // 1e18
    {"expiry", Json(EXPIRY)},                           // unix SECONDS
    {"validUntil", Json(now_ms() + 300000)},            // unix MILLISECONDS
    {"isPremium", Json(true)},
    {"makers", Json(Json::Array{Json(MAKER)})},         // optional
};
std::ofstream("requests.json") << Json(Json::Array{Json(std::move(request))}).dump();

ok(taker, taker.premium_request_args("requests.json", chain_id, nonces.next(), URL));
```

It answers with no body. Find the request's id through
`premium_taker_requests_args(text(pool["address"]), URL)`.

## 2. Maker: find requests

Poll; the api pushes nothing, and allows 30 requests a second per ip.

```cpp
std::vector<Request> requests;
for (const Json& r : as_list(maker, maker.premium_requests_args(MAKER, URL)))
  if (auto parsed = Request::from_json(r)) requests.push_back(*parsed);  // nullopt: not a request
```

## 3. Maker: quote

Repeat the request's terms exactly, add your own fields, and sign against the
request's `type_data_domain`
([rules](../../../docs/premium.md#3-the-maker-quotes)). One file posts one quote
or a whole strip:

```cpp
Quote quote_for(const Request& r) {
  Quote q;
  q.asset_address = r.asset;
  q.chain_id = r.chain_id;
  q.expiry = r.expiry;
  q.is_put = r.is_put;
  q.is_taker_buy = r.is_taker_buy.value_or(false);
  q.strike = r.strike;
  q.quantity = r.quantity;
  q.usd = r.usd;
  q.collateral_asset = r.collateral_asset;
  q.maker = MAKER;
  q.nonce = nonces.next();
  q.price = my_price(r);                       // 1e18 per option, non zero
  q.valid_until = now_ms() / 1000 + 300;       // SECONDS, now+2min .. now+10min
  q.domain = r.type_data_domain;               // the pool's option handler
  return q;
}

std::vector<std::pair<std::string, Quote>> batch;
for (const Request& r : requests) batch.emplace_back(r.id.value_or(""), quote_for(r));
std::ofstream("quotes.json") << maker.premium_quote_batch(batch);
ok(maker, maker.premium_quote_batch_args("quotes.json", URL));
```

`premium_quote_batch` throws `ryskv12::Error` on a domain the CLI could not
sign (a `salt`, another chain) before anything is written. The post returns no
ids: match your live quotes on nonce.

```cpp
std::string quote_id;
for (const Json& q : as_list(maker, maker.premium_quotes_args(MAKER, URL)))
  if (text(q["nonce"]) == batch[0].second.nonce) quote_id = text(q["id"]);

ok(maker, maker.premium_quote_status_args(quote_id, URL));
ok(maker, maker.premium_trades_args(MAKER, CHAIN_ID, std::nullopt, URL));  // settled fills
ok(maker, maker.premium_cancel_args(quote_id, CHAIN_ID, nonces.next(), URL));
```

A quote lives until taken, cancelled or past `valid_until`; requote inside the
10 minute window to keep a price standing.

To learn which quotes were filled, poll the settled trades, newest first. A
row carries the option, quantity and `txHash`, but not the quote id.

## 4. Taker: take a quote

```cpp
// prices are 1e18 decimal strings: longer is bigger, then compare as text
auto cheaper = [](const Json& a, const Json& b) {
  std::string x = text(a["price"]), y = text(b["price"]);
  return x.size() != y.size() ? x.size() < y.size() : x < y;
};
Json::Array quotes = as_list(taker, taker.premium_request_quotes_args(request_id, URL));
if (quotes.empty()) throw std::runtime_error("no quotes yet");
const Json& best = *std::max_element(quotes.begin(), quotes.end(), cheaper);
std::string tx = ok(taker, taker.premium_execute_args(text(best["id"]), nonces.next(), URL));  // tx hash
```

Or withdraw the request and every quote on it:

```cpp
ok(taker, taker.premium_cancel_request_args(request_id, CHAIN_ID, nonces.next(), URL));
```

## Funds

When the pool writes (`isTakerBuy: false`) the maker pays the premium from its
wallet, pulled by the option handler. Approve it once, or execute reverts
([amounts](../../../docs/premium.md#funds)):

```cpp
auto args = maker.approve_args(CHAIN_ID, amount, RPC_URL, premium_asset);
args.insert(args.end(), {"--spender", option_handler});
ok(maker, args);
```

`option_handler` is the request's `type_data_domain->verifying_contract`.

## Nonces

Quote, cancel, request and confirmation nonces share one keyspace per address.
`NonceCounter` persists each one before handing it out; use one per key.
Errors and what they mean: [protocol](../../../docs/premium.md#errors-worth-knowing).
