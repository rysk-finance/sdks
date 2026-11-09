# Premium RFQ integration (TypeScript)

The HTTP premium RFQ in TypeScript, for both sides. This page is the code;
[`docs/premium.md`](../../../docs/premium.md) is the protocol (fields, units,
funds, errors). Every call is one short CLI process: no `connect`, no `send`.

The worked example is the `testnet` test at the bottom of
[`test/integration.test.js`](../test/integration.test.js):
it runs this whole flow against the real testnet api.

## Setup

```ts
import { writeFileSync } from "node:fs";
import Rysk, { Env, NonceCounter } from "ryskv12";
import { isRequest, type Quote, type Request } from "ryskv12/models";

const URL = "https://insti-testnet.rysk.finance";   // undefined for production

const maker = new Rysk(Env.TESTNET, MAKER_KEY);
const taker = new Rysk(Env.TESTNET, TAKER_KEY);      // an rfq signer on the pool
const nonces = new NonceCounter();                   // one per key

const run = (sdk: Rysk, args: string[]) =>
  new Promise<string>((resolve, reject) => {
    const proc = sdk.execute(args);
    let out = "", err = "";
    proc.on("message", (line: Buffer) => (out += line.toString() + "\n"));
    proc.on("stderr", (data: Buffer) => (err += data.toString()));
    proc.on("close", (code?: number) =>
      code === 0 ? resolve(out.trim()) : reject(new Error(`${args[1]}: ${err.trim()}`)));
  });

const asJson = async (sdk: Rysk, args: string[]) => JSON.parse((await run(sdk, args)) || "[]");
```

Check the exit code, not the output: a quote post answers `200` even when it
rejects quotes, and the CLI exits non zero when it does.

## 1. Taker: open a request

The taker is a pool. Build the request from the pool's own fields
([fields](../../../docs/premium.md#1-the-taker-opens-a-request)):

```ts
const pool = (await asJson(taker, taker.premiumPoolsArgs(URL))).find(
  (p: any) => p.address.toLowerCase() === POOL,
);

writeFileSync("requests.json", JSON.stringify([{
  taker: pool.address,
  asset: pool.underlying,
  chainId: pool.chainId,
  isPut: pool.type === "Put",
  collateralAsset: pool.asset,
  premiumAsset: pool.asset,
  usd: USD,                                   // the chain's strike asset
  isTakerBuy: false,                          // the pool writes, the maker pays
  strike: "350000000000",                     // 1e8
  quantity: "1000000000000000000",            // 1e18
  expiry: EXPIRY,                             // unix SECONDS
  validUntil: Date.now() + 300_000,           // unix MILLISECONDS
  isPremium: true,
  makers: [MAKER],                            // optional
}]));

await run(taker, taker.premiumRequestArgs("requests.json", pool.chainId, nonces.next(), URL));
```

It answers with no body. Find the request's id through
`premiumTakerRequestsArgs(pool.address, URL)`.

## 2. Maker: find requests

Poll; the api pushes nothing, and allows 30 requests a second per ip.

```ts
const requests: Request[] = (await asJson(maker, maker.premiumRequestsArgs(MAKER, URL))).filter(isRequest);
```

## 3. Maker: quote

Repeat the request's terms exactly, add your own fields, and sign against the
request's `typeDataDomain`
([rules](../../../docs/premium.md#3-the-maker-quotes)). One file posts one quote
or a whole strip:

```ts
const quoteFor = (r: Request): Quote => ({
  assetAddress: r.asset, chainId: r.chainId, expiry: r.expiry,
  isPut: r.isPut, isTakerBuy: Boolean(r.isTakerBuy),
  strike: r.strike, quantity: r.quantity,
  usd: r.usd, collateralAsset: r.collateralAsset,
  maker: MAKER,
  nonce: nonces.next(),
  price: myPrice(r),                                 // 1e18 per option, non zero
  validUntil: Math.floor(Date.now() / 1000) + 300,   // SECONDS, now+2min .. now+10min
  domain: r.typeDataDomain,                          // the pool's option handler
});

const batch = requests.map((r) => ({ requestId: r.id!, quote: quoteFor(r) }));
writeFileSync("quotes.json", maker.premiumQuoteBatch(batch));
await run(maker, maker.premiumQuoteBatchArgs("quotes.json", URL));
```

`premiumQuoteBatch` throws on a domain the CLI could not sign (a `salt`,
another chain) before anything is written. The post returns no ids: match your
live quotes on nonce.

```ts
const book = await asJson(maker, maker.premiumQuotesArgs(MAKER, URL));
const mine = batch[0].quote;
const quoteId = String(book.find((q: any) => String(q.nonce) === mine.nonce).id);

await run(maker, maker.premiumQuoteStatusArgs(quoteId, URL));
await run(maker, maker.premiumTradesArgs(MAKER, CHAIN_ID, undefined, URL)); // settled fills
await run(maker, maker.premiumCancelArgs(quoteId, CHAIN_ID, nonces.next(), URL));
```

A quote lives until taken, cancelled or past `validUntil`; requote inside the
10 minute window to keep a price standing.

To learn which quotes were filled, poll the settled trades, newest first. A
row carries the option, quantity and `txHash`, but not the quote id.

## 4. Taker: take a quote

```ts
const quotes = await asJson(taker, taker.premiumRequestQuotesArgs(requestId, URL));
const best = quotes.reduce((a: any, b: any) => (BigInt(b.price) > BigInt(a.price) ? b : a));
const tx = await run(taker, taker.premiumExecuteArgs(String(best.id), nonces.next(), URL));  // tx hash
```

Or withdraw the request and every quote on it:

```ts
await run(taker, taker.premiumCancelRequestArgs(requestId, CHAIN_ID, nonces.next(), URL));
```

## Funds

When the pool writes (`isTakerBuy: false`) the maker pays the premium from its
wallet, pulled by the option handler. Approve it once, or execute reverts
([amounts](../../../docs/premium.md#funds)):

```ts
await run(maker, [...maker.approveArgs(CHAIN_ID, amount, RPC_URL, premiumAsset), "--spender", optionHandler]);
```

`optionHandler` is the request's `typeDataDomain.verifyingContract`.

## Nonces

Quote, cancel, request and confirmation nonces share one keyspace per address.
`NonceCounter` persists each one before handing it out; use one per key.
Errors and what they mean: [protocol](../../../docs/premium.md#errors-worth-knowing).
