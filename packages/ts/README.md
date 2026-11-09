# Rysk V12 Node client

Node wrapper for ryskV12 cli

## Setup

The package comes with a postinstall script that automatically pulls the [latest release of `ryskV12 cli`](https://github.com/rysk-finance/ryskV12/releases).
If the script fails to do so please navigate to https://github.com/rysk-finance/ryskV12/releases and download the latest release in your project directory as `ryskV12`.

## Run

### Process events

`execute` returns a `CliWebSocket`, an `EventEmitter`. Listen with `.on(event, handler)`:

| Event | Carries |
| --- | --- |
| `open` | the process was spawned; fires on the next tick, so a listener attached right after `execute` hears it |
| `message` | one line of the CLI's stdout: one message |
| `stderr` | a chunk of the CLI's stderr — the CLI logs there, so this is not a failure |
| `close` | the exit code; a non-zero one is how a command run on its own reports failure |
| `error` | the process itself failed to spawn or run |

`send(args)` writes a command into a running `connect` (see below), and `close()` ends the process.

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
const rfqProc = ryskSDK.execute(ryskSDK.connectArgs("rfqs/<assetAddress>"));
const makerProc = ryskSDK.execute(ryskSDK.connectArgs("maker"));
```

Balances, positions, transfers and quotes are sent into a connection with `send`: the running
`connect` signs and sends each one itself, so no process is spawned per command. A command it cannot
run comes back as a `message`: a json-rpc error under the id the server would have used (the
rfq id for a quote, the command name otherwise), so `isErrorData` catches it with the server's.

### Disconnect

```ts
makerProc.close();
```

### Approve spending

Omit the asset and the chain's strike asset is approved, as before:

```ts
const chainId = 84532;
const amount = "1000000";
const rpcURL = "https://rpc...";

const proc = ryskSDK.execute(ryskSDK.approveArgs(chainId, amount, rpcURL));
```

Pass one to approve any other erc20:

```ts
const proc = ryskSDK.execute(
  ryskSDK.approveArgs(chainId, amount, rpcURL, "0xabc..."),
);
```

### List USDC Balances

```ts
makerProc.send(ryskSDK.balancesArgs("0xabc"));
```

### Deposit / Withdraw

```ts
const transferDetails: Transfer = {
  amount: "500000",
  asset: "0x...", // The asset address
  chain_id: 84532,
  is_deposit: true,
  nonce: "some-unique-nonce",
};

makerProc.send(ryskSDK.transferArgs(transferDetails));
```

### List Positions

```ts
makerProc.send(ryskSDK.positionsArgs("0xabc"));
```

### Redemption

```ts
// opt an otoken in (true) or out (false) of redemption at expiry, over the maker connection
makerProc.send(ryskSDK.willRedeemArgs(84532, "0xabc", "0xotoken", true, "44"));

// redeem your own expired otokens now - a plain http call to this env's api
ryskSDK.execute(ryskSDK.selfRedeemArgs(84532, "45"));
// or up to an expiry, skipping some assets
ryskSDK.execute(ryskSDK.selfRedeemArgs(84532, "46", 1767225600, ["0xasset"]));
```

### Send a Quote

```ts
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

makerProc.send(ryskSDK.quoteArgs(request_id, quoteDetails));
```

`domain` needs `name`, `version` and `verifyingContract` together; its `chainId`
always comes from the quote, so a domain for another chain - or one carrying a
`salt` - is rejected instead of being signed against the wrong domain.

### Premium RFQ (maker)

The premium RFQ api is plain HTTP, so these need no connection: each one runs the CLI
once and prints the api's response on stdout. `url` is optional — leave it out for production, pass
it for a local or staging api.

```ts
// requests this maker may quote, each with the domain to sign against
const requests = ryskSDK.execute(ryskSDK.premiumRequestsArgs(maker));

// sign and post quotes: one or a whole strip, in one api call
const quote: Quote = {
  ...quoteDetails,
  maker,
  nonce: "42",                              // decimal uint64, one counter per key
  price: "1250000000000000000",             // 1e18
  validUntil: Math.floor(Date.now() / 1000) + 300, // SECONDS, now+2min .. now+10min
  domain: request.typeDataDomain,           // the pool's option handler
};
writeFileSync("batch.json", ryskSDK.premiumQuoteBatch([{ requestId: request.id, quote }]));
const proc = ryskSDK.execute(ryskSDK.premiumQuoteBatchArgs("batch.json"));

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

### Premium RFQ (taker)

A request's taker is a pool; the SDK's key has to be one of the pool's on chain RFQ signers.

```ts
ryskSDK.execute(ryskSDK.premiumPoolsArgs());

// a json array of requests as the api takes them; answers with no body
writeFileSync("requests.json", JSON.stringify([request]));
ryskSDK.execute(ryskSDK.premiumRequestArgs("requests.json", chainId, "44"));

ryskSDK.execute(ryskSDK.premiumTakerRequestsArgs(pool));       // the pool's live requests
ryskSDK.execute(ryskSDK.premiumRequestQuotesArgs(requestId));  // the quotes on one
ryskSDK.execute(ryskSDK.premiumCancelRequestArgs(requestId, chainId, "45"));

// take a quote: prints the settling transaction's hash
ryskSDK.execute(ryskSDK.premiumExecuteArgs(quoteId, "46"));
```

See the CLI README for the request fields; note a request's `validUntil` is in **milliseconds**.

## Tests

```sh
yarn test                     # unit tests
make dev-bin && yarn test     # + the integration tests, which need a cli
```

`test/integration.test.js` runs the real CLI against a fake api, so the arg builders and the binary
cannot drift apart. Those tests skip themselves when no CLI is present.

The `testnet` test at the bottom of `test/integration.test.js` is the full premium flow end to end
against the real testnet api — a taker opens an rfq, the maker quotes, cancels and requotes, and
the taker takes the trade on chain. Read it as the worked example. It skips unless the e2e keys are
set; `make e2e` from the repo root loads them from `.env`.
