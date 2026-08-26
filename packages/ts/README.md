# Rysk V12 Node client

Node wrapper for ryskV12 cli

## Setup

The package comes with a postinstall script that automatically pulls the [latest release of `ryskV12 cli`](https://github.com/rysk-finance/ryskV12/releases).
If the script fails to do so please navigate to https://github.com/rysk-finance/ryskV12/releases and download the latest release in your project directory as `ryskV12`.

## Run

### Process events

`execute` returns a `CliWebSocket`, an `EventEmitter` with WebSocket-style handlers:

| Event | Handler | Carries |
| --- | --- | --- |
| `open` | `onopen` | the process was spawned |
| `message` | `onmessage` | a chunk of the CLI's stdout |
| `stderr` | `onstderr` | a chunk of the CLI's stderr — the CLI logs there, so this is not a failure |
| `close` | `onclose` | the exit code; a non-zero one is how a command reports failure |
| `error` | `onerror` | the process itself failed to spawn or run |

### Core `execute` method

The `execute` method spawns a subprocess and returns it.
You can then attach listeners to the process to capture `stdout`, `stderr` and events like "close" and "error".

```ts
// ...
  public execute(args: Array<string> = []): ChildProcessWithoutNullStreams {
    return spawn(this._cli_path, args, {
      shell: true,
      stdio: ["pipe", "pipe", "pipe"],
    });
  }
//...
```

### The private key never reaches argv

`execute` hands the key to the CLI through `RYSK_PRIVATE_KEY` in the child's environment, so it does
not show up in `ps` or a shell history, and the `*Args` builders do not contain it. If you spawn the
CLI yourself instead of through `execute`, set that variable too, or the CLI will stop with
`Required flag "private_key" not set`.

### Instantiation

```ts
const privateKey = "0xYOUR_PRIVATE_KEY";
const env = Env.TESTNET; // Env.LOCAL | Env.TESTNET | Env.MAINNET
const ryskSDK = new Rysk(env, privateKey, "/path/to/ryskV12"); // Optional CLI path

// A fourth argument makes a CLI older than this SDK throw instead of warning:
const strictSDK = new Rysk(env, privateKey, "./ryskV12", true);
```

The constructor checks the CLI's version (major, minor and patch) and warns when it is older than the
SDK needs, because commands added since then fail with `flag provided but not defined`. A locally
built CLI reports no version number and is left alone.

### Create a Connection

```ts
const rfqChannel = "my-rfq-channel-id";
const rfqURI = "rfqs/<assetAddress>"; // Example websocket endpoint

const rfqProc = ryskSDK.execute(ryskSDK.connectArgs(rfqChannel, rfqURI));

const makerChannel = "maker-channel";
const makerURI = "maker";

const makerProc = ryskSDK.execute(ryskSDK.connectArgs(makerChannel, makerURI));
```

### Disconnect

```ts
const makerChannel = "maker-channel";

ryskSDK.execute(ryskSDK.disconnectArgs(makerChannel));
```

### Approve USDC spending

```ts
const chainId = 84532;
const amount = "1000000";
const rpcURL = "https://rpc...";

const proc = ryskSDK.execute(ryskSDK.approveArgs(chainId, amount, rpcURL));
```

### List USDC Balances

```ts
const makerChannel = "maker-channel";
const account = "0xabc";

const proc = ryskSDK.execute(ryskSDK.balancesArgs(makerChannel, account));
```

### Deposit / Withdraw

```ts
const makerChannel = "maker-channel";
const transferDetails: Transfer = {
  amount: "500000",
  asset: "0x...", // The asset address
  chain_id: 84532,
  is_deposit: true,
  nonce: "some-unique-nonce",
};

const proc = ryskSDK.execute(
  ryskSDK.transferArgs(makerChannel, transferDetails)
);
```

### List Positions

```ts
const makerChannel = "maker-channel";
const account = "0xabc";

const proc = ryskSDK.execute(ryskSDK.positionsArgs(makerChannel, account));
```

### Send a Quote

```ts
const makerChannel = "maker-channel";
const request_id = "some-uuid-from-server";
const quoteDetails: Quote = {
  assetAddress: "0x...",
  chainId: 84532,
  expiry: 1678886400,
  isPut: false,
  isTakerBuy: true,
  maker: "0x...",
  nonce: "another-unique-nonce",
  price: "0.01",
  quantity: "1",
  strike: "1000000",
  validUntil: 1678886460,
  usd: "0x...",
  collateralAsset: "0x....",
  // optional: asset the premium is paid in. Sent with the quote, not signed.
  premiumAsset: request.premiumAsset,
  // optional: sign against the domain the request asks for. Omit to use the
  // default domain for the chain.
  domain: request.typeDataDomain,
};

const proc = ryskSDK.execute(
  ryskSDK.quoteArgs(makerChannel, request_id, quoteDetails)
);
```

`domain` needs `name`, `version` and `verifyingContract` together; its `chainId`
always comes from the quote, so a domain for another chain - or one carrying a
`salt` - is rejected instead of being signed against the wrong domain.

### Premium RFQ (maker)

The premium RFQ api is plain HTTP, so these need no connection and no channel: each one runs the CLI
once and prints the api's response on stdout. `url` is optional — leave it out for production, pass
it for a local or staging api.

```ts
// requests this maker may quote, each with the domain to sign against
const requests = ryskSDK.execute(ryskSDK.premiumRequestsArgs(maker));

// sign and post one quote for a request
const quote: Quote = {
  ...quoteDetails,
  maker,
  nonce: "42",                              // decimal uint64, one counter per key
  price: "1250000000000000000",             // 1e18
  validUntil: Math.floor(Date.now() / 1000) + 300, // SECONDS, now+2min .. now+10min
  domain: request.typeDataDomain,           // the pool's option handler
};
const proc = ryskSDK.execute(ryskSDK.premiumQuoteArgs(request.id, quote));

// a whole strip in one api call
writeFileSync("batch.json", ryskSDK.premiumQuoteBatch([
  { requestId: request.id, quote: quoteA },
  { requestId: request.id, quote: quoteB },
]));
ryskSDK.execute(ryskSDK.premiumQuoteBatchArgs("batch.json"));

// your live quotes (the only source of quote ids), one quote, and a cancel
ryskSDK.execute(ryskSDK.premiumQuotesArgs(maker));
ryskSDK.execute(ryskSDK.premiumQuoteStatusArgs(quoteId));
ryskSDK.execute(ryskSDK.premiumCancelArgs(quoteId, chainId, "43"));
```

Terms have to be the request's — the api rebuilds the signed message from the stored request, so an
altered term just yields a signature that will not verify. `domain` needs `verifyingContract` (the
CLI defaults name and version); a domain with a `salt` or another chain's `chainId` throws here
rather than being signed.

Posting quotes always answers `200`, with rejections in a `failures` array. The CLI prints the
response and exits non zero when that array is not empty, so check the exit code, not the status.

Nonces are spent once and share one keyspace per address across quotes and cancels — draw them from a
single persisted counter.

## Examples

Two runnable maker loops live in `examples/`, one per transport:

| File | What it does |
| --- | --- |
| `examples/run.js` | Websocket flow: holds the maker connection open, listens for RFQs on an asset, prices them and sends signed quotes back through the maker channel. Also reads balances and positions, and disconnects both channels on `SIGINT`. |
| `examples/premium.js` | Premium RFQ flow: polls the requests this maker may quote, prices them, posts the batch in one api call, matches quote ids back from the listing, refreshes each quote before its 10 minute window closes, and pulls everything on `SIGINT`. |

```sh
RYSK_SDK_PK=<hex private key> RYSK_MAKER=0x<maker address> node examples/run.js

RYSK_SDK_PK=<hex private key> RYSK_MAKER=0x<maker address> \
  PREMIUM_URL=https://insti-testnet.rysk.finance node examples/premium.js
```

Both keep their nonce counter in a `.rysk-nonce` file through `NonceCounter`, because a nonce is spent
once per address and a counter that rewinds after a restart starts failing every write. `priceRequest`
is the only part meant to be replaced — everything else is the plumbing the api expects.

```ts
import { NonceCounter } from "ryskv12";

const nonces = new NonceCounter(".rysk-nonce"); // one counter per signing key
const quoteNonce = nonces.next();               // decimal string, never reused, never rewound
```

## Tests

```sh
yarn test                     # unit tests
make dev-bin && yarn test     # + the integration tests, which need a cli
```

`test/integration.test.js` runs the real CLI against a fake api, so the arg builders and the binary
cannot drift apart. Those tests skip themselves when no CLI is present.
