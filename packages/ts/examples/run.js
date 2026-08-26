// Market maker loop for the websocket flow: hold a maker connection open, listen
// for RFQs on an asset, price them, and send signed quotes back through the
// maker channel.
//
//   RYSK_SDK_PK=<hex private key> \
//   RYSK_MAKER=0x<maker address> \
//   RYSK_ASSET=0x<asset address> \
//   node examples/run.js
import { readFileSync, writeFileSync } from "node:fs";

import Rysk, { Env } from "ryskv12";
import {
  isJSONRPCResponse,
  isQuoteNotification,
  isRequest,
} from "ryskv12/models";

const PK = process.env.RYSK_SDK_PK;
const MAKER = process.env.RYSK_MAKER;
const ASSET = process.env.RYSK_ASSET || "0x5555555555555555555555555555555555555555";

const MAKER_CHANNEL = "MAKER_CHAN";
const RFQ_CHANNEL = "RFQ_CHAN";
const QUOTE_VALIDITY_SECONDS = 30;
const NONCE_FILE = ".rysk-nonce";

const sdk = new Rysk(Env.TESTNET, PK, "./ryskV12");

/**
 * Nonces are spent once per address, so they come from a single counter that
 * survives a restart - one that rewinds starts failing every write.
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

/** Replace with your own pricing. Quantity and strike come from the request. */
const priceRequest = (request) => {
  const mid = 4n * 10n ** 18n;
  const edge = request.isTakerBuy ? 110n : 90n; // sell above, buy below
  return ((mid * edge) / 100n).toString();
};

/**
 * The terms have to be the request's - they are what the taker asked for and
 * what the signature commits to. Ours are the maker's own fields.
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
  validUntil: Math.ceil(Date.now() / 1000) + QUOTE_VALIDITY_SECONDS,
  // sent with the quote but not signed; present when the request names one
  premiumAsset: request.premiumAsset,
  // sign against the domain the request asks for, if it asks for one
  domain: request.typeDataDomain,
});

const main = () => {
  if (!PK || !MAKER) {
    console.error("set RYSK_SDK_PK and RYSK_MAKER");
    process.exit(1);
  }

  // The maker connection is what quotes and transfers are written into; it has
  // to stay open for the whole session.
  const makerProc = sdk.execute(sdk.connectArgs(MAKER_CHANNEL, "maker"));
  makerProc.on("open", () => console.log("maker channel connected"));
  makerProc.onmessage = (data) => {
    const payload = data.toString().trim();
    if (!payload) return;

    try {
      const message = JSON.parse(payload);
      // A quote notification tells you where your price stands, so it is the cue
      // to re-quote rather than wait.
      if (isJSONRPCResponse(message) && isQuoteNotification(message.result)) {
        const { rfqId, newBest, yours } = message.result;
        console.log(`rfq ${rfqId}: best ${newBest}, yours ${yours}`);
        return;
      }
      console.log("maker:", payload);
    } catch {
      console.log("maker:", payload);
    }
  };
  makerProc.on("error", (err) => console.error("maker error:", err.toString()));

  const quote = (id, request) => {
    const proc = sdk.execute(sdk.quoteArgs(MAKER_CHANNEL, id, buildQuote(request)));
    proc.on("message", (data) => console.log("quote:", data.toString().trim()));
    proc.on("error", (err) => console.error("quote error:", err.toString()));
  };

  const rfqProc = sdk.execute(sdk.connectArgs(RFQ_CHANNEL, `rfqs/${ASSET}`));
  rfqProc.on("open", () => console.log(`listening for rfqs on ${ASSET}`));
  rfqProc.onmessage = (data) => {
    const payload = data.toString().trim();
    if (!payload) return;

    try {
      const message = JSON.parse(payload);
      if (!isJSONRPCResponse(message) || !isRequest(message.result)) return;
      console.log(`rfq ${message.id}: ${message.result.quantity} @ ${message.result.strike}`);
      quote(message.id, message.result);
    } catch (error) {
      console.error("failed to handle rfq:", error);
    }
  };
  rfqProc.on("error", (err) => console.error("rfq error:", err.toString()));

  // Account reads go through the maker channel like everything else.
  sdk.execute(sdk.balancesArgs(MAKER_CHANNEL, MAKER));
  sdk.execute(sdk.positionsArgs(MAKER_CHANNEL, MAKER));

  process.on("SIGINT", () => {
    console.log("\ndisconnecting");
    sdk.execute(sdk.disconnectArgs(RFQ_CHANNEL));
    sdk.execute(sdk.disconnectArgs(MAKER_CHANNEL));
    setTimeout(() => process.exit(0), 500);
  });
};

main();
