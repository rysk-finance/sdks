# ryskv12 (C++)

C++ sdk to interact with [Rysk v12](https://rysk.finance).

Like the other sdks here, this one does not talk to the api itself. It builds
argument lists for the `ryskV12` cli and spawns it, so the cli keeps ownership
of the signing key, the websocket and the eip712 signing, and every sdk agrees
on flags by construction.

C++17, posix, and **no third party dependencies** — not even for json or tests.

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

Output result = sdk.run(sdk.premium_quote_args(request_id, quote));
if (result.exit_code != 0) std::cerr << result.err << "\n";
```

Every `*_args` method is pure: it returns what the cli would be given without
running anything, so the whole surface is testable without a binary. A domain
the cli could not sign throws `ryskv12::Error` before anything is spawned.

## Approving and depositing

Leave the asset out and the cli falls back to the chain's strike asset:

```cpp
sdk.run(sdk.approve_args(84532, "1000000", rpc_url));
```

Pass one to approve or deposit any erc20:

```cpp
sdk.run(sdk.approve_args(84532, "1000000", rpc_url, asset));
```

## Reading what comes back

The cli writes one json-rpc message per line. A failed call carries `error`
instead of `result`, so read that first:

```cpp
sdk.run_lines(sdk.connect_args(channel, "maker"), [](const std::string& line) {
  auto res = JsonRpcResponse::parse(line);
  if (!res) return;
  if (res->error) {
    std::cerr << "rpc " << res->id << " failed: " << res->error->message << "\n";
    return;
  }
  if (auto request = Request::from_json(*res->result)) {
    // quote it
  }
});
```

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
