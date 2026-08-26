// Market maker loop for the premium RFQ api: poll the requests you may quote,
// price them, post the quotes in one call, refresh them before their window
// closes, and pull everything on the way out.
//
//   RYSK_SDK_PK=<hex private key> \
//   RYSK_MAKER=0x<maker address> \
//   PREMIUM_URL=https://insti-testnet.rysk.finance \
//   node examples/premium.js
//
// PREMIUM_URL is optional: left out, the CLI talks to production.
import { readFileSync, unlinkSync, writeFileSync } from "node:fs";

import Rysk, { Env } from "ryskv12";
import { isRequest } from "ryskv12/models";

const PK = process.env.RYSK_SDK_PK;
const MAKER = process.env.RYSK_MAKER;
const PREMIUM_URL = process.env.PREMIUM_URL;

const POLL_INTERVAL_MS = 2_000;
// The api takes a validUntil strictly inside now+2min .. now+10min, so a quote
// has to be replaced before it leaves that window.
const QUOTE_VALIDITY_SECONDS = 8 * 60;
const REFRESH_BEFORE_SECONDS = 3 * 60;

const NONCE_FILE = ".rysk-nonce";
const BATCH_FILE = ".rysk-batch.json";

const sdk = new Rysk(Env.TESTNET, PK, "./ryskV12");

/**
 * Nonces are spent once and share one keyspace per address across quotes and
 * cancels, so they come from a single counter that survives a restart. A counter
 * that rewinds starts failing every write.
 */
const nextNonce = () => {
  let counter = Date.now();
  try {
    counter = Math.max(counter, Number(readFileSync(NONCE_FILE, "utf8")) + 1);
  } catch {
    // first run, start from the clock
  }
  writeFileSync(NONCE_FILE, String(counter));
  return String(counter);
};

/** Runs one CLI command to completion and returns its stdout. */
const runOnce = (args) =>
  new Promise((resolve, reject) => {
    const proc = sdk.execute(args.filter((arg) => arg !== ""));
    let stdout = "";
    let stderr = "";
    proc.on("message", (data) => (stdout += data.toString()));
    proc.on("error", (err) => (stderr += err.toString()));
    proc.on("close", (code) => {
      if (code === 0) {
        resolve(stdout.trim());
        return;
      }
      // Posting quotes answers 200 with rejections in a failures array, so the
      // CLI exits non zero and stdout still holds the api's response.
      reject(new Error(`${args[1]} exited ${code}: ${stderr.trim() || stdout.trim()}`));
    });
  });

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** Replace with your own pricing. Quantity and strike come from the request. */
const priceRequest = (request) => {
  const mid = 4n * 10n ** 18n;
  const edge = request.isTakerBuy ? 110n : 90n; // sell above, buy below
  return ((mid * edge) / 100n).toString();
};

/**
 * The terms have to be the request's: the api rebuilds the signed message from
 * the stored request, so an altered term only yields a signature that will not
 * verify. Ours are the maker's own fields plus the domain the request carries.
 */
const buildQuote = (request) => ({
  assetAddress: request.asset,
  chainId: request.chainId,
  expiry: request.expiry,
  isPut: request.isPut,
  isTakerBuy: request.isTakerBuy,
  quantity: request.quantity,
  strike: request.strike,
  usd: request.usd,
  collateralAsset: request.collateralAsset,
  maker: MAKER,
  nonce: nextNonce(),
  price: priceRequest(request),
  validUntil: Math.floor(Date.now() / 1000) + QUOTE_VALIDITY_SECONDS,
  domain: request.typeDataDomain,
});

/** requestId -> the quote we currently have on the book for it. */
const live = new Map();

const postQuotes = async (pairs) => {
  if (pairs.length === 0) return;

  // One api call for the whole strip: the rate limit is shared across routes.
  writeFileSync(BATCH_FILE, sdk.premiumQuoteBatch(pairs));
  try {
    const response = await runOnce(sdk.premiumQuoteBatchArgs(BATCH_FILE, PREMIUM_URL));
    console.log(`posted ${pairs.length} quotes: ${response}`);
  } catch (error) {
    // Per quote feedback, not a batch rejection - the quotes not listed in
    // failures are on the book.
    console.error(`some quotes were rejected: ${error.message}`);
  } finally {
    unlinkSync(BATCH_FILE);
  }

  for (const { requestId, quote } of pairs) {
    live.set(requestId, { nonce: quote.nonce, validUntil: quote.validUntil, chainId: quote.chainId });
  }
};

/**
 * Ids only come back from the quotes listing, so posted quotes are matched to
 * their ids by nonce - which is unique per quote here.
 */
const reconcile = async () => {
  const listed = JSON.parse((await runOnce(sdk.premiumQuotesArgs(MAKER, PREMIUM_URL))) || "[]");
  const byNonce = new Map(listed.map((quote) => [quote.nonce, quote]));

  for (const [requestId, tracked] of live) {
    const onBook = byNonce.get(tracked.nonce);
    if (!onBook) {
      // Gone: filled, cancelled, or expired. The api cannot tell you which -
      // confirm fills on chain.
      console.log(`quote on ${requestId} left the book`);
      live.delete(requestId);
      continue;
    }
    tracked.id = onBook.id;
  }
};

const cancel = async (requestId, tracked) => {
  if (!tracked.id) return; // never seen on the book, nothing to pull
  await runOnce(sdk.premiumCancelArgs(tracked.id, tracked.chainId, nextNonce(), PREMIUM_URL));
  live.delete(requestId);
  console.log(`cancelled ${tracked.id}`);
};

const tick = async () => {
  const requests = JSON.parse((await runOnce(sdk.premiumRequestsArgs(MAKER, PREMIUM_URL))) || "[]");
  const quotable = requests.filter(isRequest);
  const nowSeconds = Math.floor(Date.now() / 1000);

  const pairs = [];
  for (const request of quotable) {
    const tracked = live.get(request.id);

    if (!tracked) {
      pairs.push({ requestId: request.id, quote: buildQuote(request) });
      continue;
    }
    // A price posted 9 minutes ago is about to become unexecutable: replace it
    // rather than letting it go stale.
    if (tracked.validUntil - nowSeconds < REFRESH_BEFORE_SECONDS) {
      await cancel(request.id, tracked);
      pairs.push({ requestId: request.id, quote: buildQuote(request) });
    }
  }

  // A request that disappeared from the listing expired or was cancelled by its
  // taker; one fill also retires every other quote under it.
  for (const [requestId, tracked] of live) {
    if (!quotable.some((request) => request.id === requestId)) {
      live.delete(requestId);
      console.log(`request ${requestId} is gone, dropping quote ${tracked.id ?? "(unlisted)"}`);
    }
  }

  await postQuotes(pairs);
  await reconcile();
};

const main = async () => {
  if (!PK || !MAKER) {
    console.error("set RYSK_SDK_PK and RYSK_MAKER");
    process.exit(1);
  }

  let running = true;
  process.on("SIGINT", () => {
    console.log("\npulling quotes before exit");
    running = false;
  });

  while (running) {
    try {
      await tick();
    } catch (error) {
      console.error(`tick failed: ${error.message}`);
    }
    await sleep(POLL_INTERVAL_MS);
  }

  for (const [requestId, tracked] of [...live]) {
    try {
      await cancel(requestId, tracked);
    } catch (error) {
      console.error(`failed to cancel ${tracked.id}: ${error.message}`);
    }
  }
};

main();
