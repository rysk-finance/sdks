# Rysk V12 Node client

Node wrapper for ryskV12 cli

## Setup

The package comes with a postinstall script that automatically pulls the [latest release of `ryskV12 cli`](https://github.com/rysk-finance/ryskV12/releases).
If the script fails to do so please navigate to https://github.com/rysk-finance/ryskV12/releases and download the latest release in your project directory as `ryskV12`.

## Run

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

### Instantiation

```ts
const privateKey = "0xYOUR_PRIVATE_KEY";
const env = Env.TESTNET; // Env.LOCAL | Env.TESTNET | Env.MAINNET
const ryskSDK = new Rysk(env, privateKey, "/path/to/ryskV12"); // Optional CLI path
```

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

## Example

Check out the `examples` folder.
