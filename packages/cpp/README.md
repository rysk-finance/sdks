# ryskv12 (C++)

C++ sdk to interact with [Rysk v12](https://rysk.finance).

Like the other sdks here, this one does not talk to the api itself. It builds
argument lists for the `ryskV12` cli and spawns it, so the cli keeps ownership
of the signing key, the websocket and the eip712 signing, and every sdk agrees
on flags by construction.

C++17, posix, and **no third party dependencies** — not even for json or tests.

Integration guides: [V12 websocket auction](docs/v12.md) · [Premium RFQ](docs/premium.md).

## Building

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

As a subproject:

```cmake
add_subdirectory(sdks/packages/cpp)
target_link_libraries(your_app PRIVATE ryskv12::ryskv12)
```

`RYSKV12_BUILD_TESTS` defaults on for a top level build and off as a subproject.

The cli binary is not bundled. Download it from
[releases](https://github.com/rysk-finance/sdks/releases) into your working
directory as `ryskV12`, or call `setup()` once at startup.

## Quoting

```cpp
#include "ryskv12/client.hpp"
#include "ryskv12/nonce.hpp"

using namespace ryskv12;

Rysk::Options options;
options.cli_path = "./ryskV12";
options.strict_version = true;  // throw rather than warn on an old cli
Rysk sdk(Env::Testnet, private_key, options);
// the premium api: Env does not choose it, and without a url the cli uses production
const std::string url = "https://insti-testnet.rysk.finance";

NonceCounter nonces;

Quote quote;
quote.asset_address = request.asset;
quote.chain_id      = request.chain_id;
quote.expiry        = request.expiry;
quote.is_put        = request.is_put;
quote.is_taker_buy  = request.is_taker_buy.value_or(false);
quote.maker         = maker;
quote.nonce         = nonces.next();
quote.price         = "1250000000000000000";
quote.quantity      = request.quantity;
quote.strike        = request.strike;
quote.valid_until   = now_seconds + 300;
quote.usd           = request.usd;
quote.collateral_asset = request.collateral_asset;
quote.domain        = request.type_data_domain;

// one quote or a whole strip, in one api call
std::ofstream("batch.json") << sdk.premium_quote_batch({{request_id, quote}});
Output result = sdk.run(sdk.premium_quote_batch_args("batch.json", url));
if (result.exit_code != 0) std::cerr << result.err << "\n";
```

Every `*_args` method is pure: it returns what the cli would be given without
running anything, so the whole surface is testable without a binary. A domain
the cli could not sign throws `ryskv12::Error` before anything is spawned.

### Taking (premium rfq)

A request's taker is a pool; the sdk's key has to be one of the pool's on chain
rfq signers. A request's `validUntil` is in **milliseconds**; see the cli README
for its fields.

```cpp
sdk.run(sdk.premium_pools_args(url));
// a json array of requests as the api takes them; answers with no body
sdk.run(sdk.premium_request_args("requests.json", chain_id, nonce_a, url));
sdk.run(sdk.premium_taker_requests_args(pool, url));        // the pool's live requests
sdk.run(sdk.premium_request_quotes_args(request_id, url));  // the quotes on one
sdk.run(sdk.premium_cancel_request_args(request_id, chain_id, nonce_b, url));
// take a quote: prints the settling transaction's hash
Output taken = sdk.run(sdk.premium_execute_args(quote_id, nonce_c, url));
```

## Approving and depositing

Leave the asset out and the cli falls back to the chain's strike asset:

```cpp
sdk.run(sdk.approve_args(84532, "1000000", rpc_url));
```

Pass one to approve or deposit any erc20:

```cpp
sdk.run(sdk.approve_args(84532, "1000000", rpc_url, asset));
```

## Connections

`connect` holds a websocket open. `spawn` it, and `send` it the signed commands
(`quote_args`, `transfer_args`, `balances_args`, `positions_args`,
`will_redeem_args`): each one
goes in on the cli's stdin and is signed and sent by that same process, so
nothing new is started per quote. `kill()` disconnects.

`self_redeem_args(chain_id, nonce)` redeems your own expired otokens. It is a
plain http call to this env's api, so `run` it rather than `send` it.

The cli writes one json-rpc message per line. A failed call carries `error`
instead of `result`, so read that first:

```cpp
Process maker = sdk.spawn(sdk.connect_args("maker"));
std::thread maker_reader([&] { maker.read_lines([](const std::string& line) { /* ... */ }); });

Process rfqs = sdk.spawn(sdk.connect_args("rfqs/" + asset));
rfqs.read_lines([&](const std::string& line) {
  auto res = JsonRpcResponse::parse(line);
  if (!res) return;
  if (res->error) {
    std::cerr << "rpc " << res->id << " failed: " << res->error->message << "\n";
    return;
  }
  if (auto request = Request::from_json(*res->result)) {
    maker.send(sdk.quote_args(res->id, build_quote(*request)));
  }
});

maker.kill();
maker_reader.join();
```

`send` is safe from several threads. A command the cli rejects comes back as a
line of its own: a json-rpc error under the id the server would have used. The
connection stays up.

`from_json` returns `nullopt` for a payload of the wrong shape — a missing
required field, or one of the wrong type. That is what the `is_request` style
predicates in the other sdks are for; here the accessors simply refuse to
coerce, so `1` never passes as `"1"`.

## Nonces

A nonce is spent once per address, and the api keys them on (address, nonce)
alone, so quotes, cancels and confirmations from one signing key all draw from
the same sequence. `NonceCounter` persists to a file so a restart cannot rewind
into nonces the api has already seen.

## Layout

| Path | What |
| --- | --- |
| `include/ryskv12/client.hpp` | the arg builders and process spawning |
| `include/ryskv12/models.hpp` | Request, Quote, Transfer, responses |
| `include/ryskv12/json.hpp` | the json subset those two need |
| `include/ryskv12/nonce.hpp` | the file backed nonce counter |
| `tests/harness.hpp` | the test harness, small enough to not be a dependency |

`src/fetch_script.cpp` embeds `scripts/fetch_latest_release.sh` so an installed
library needs no repo checkout. `embed_test` fails if the two drift apart.

## Development

From the repo root:

```sh
make dev-bin    # builds the cli into every sdk package
make test-cpp   # cmake build + ctest
```

`tests/cli_test.cpp` drives the real binary against a fake api built on posix
sockets. It skips itself when no cli is present.
