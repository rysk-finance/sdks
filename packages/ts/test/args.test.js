import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import Rysk, { Env, NonceCounter } from "../dist/index.js";
import {
  isJSONRPCResponse,
  isQuote,
  isQuoteNotification,
  isRequest,
  isTransfer,
} from "../dist/models.js";

const MAKER = "0x1000000000000000000000000000000000000001";
const HANDLER = "0x2000000000000000000000000000000000000002";
const ASSET = "0x98d56648c9b7f3cb49531f4135115b5000ab1733";

/** A SDK whose constructor does not shell out to a CLI that may not exist. */
const sdk = () => {
  const instance = Object.create(Rysk.prototype);
  instance._env = Env.TESTNET;
  instance._cli_path = "./ryskV12";
  instance._private_key = "test-key";
  return instance;
};

const quote = (extra = {}) => ({
  assetAddress: ASSET,
  chainId: 84532,
  expiry: 1767225600,
  isPut: true,
  isTakerBuy: false,
  maker: MAKER,
  nonce: "42",
  price: "1250000000000000000",
  quantity: "1000000000000000000",
  strike: "300000000000",
  validUntil: 1767139500,
  usd: ASSET,
  collateralAsset: ASSET,
  ...extra,
});

const flagValue = (args, flag) => args[args.indexOf(flag) + 1];

test("no arg builder carries the private key", () => {
  const rysk = sdk();
  const builders = [
    rysk.connectArgs("chan", "maker"),
    rysk.disconnectArgs("chan"),
    rysk.approveArgs(84532, "1", "https://rpc"),
    rysk.balancesArgs("chan", MAKER),
    rysk.positionsArgs("chan", MAKER),
    rysk.transferArgs("chan", {
      user: MAKER,
      amount: "1",
      asset: ASSET,
      chain_id: 84532,
      is_deposit: true,
      nonce: "1",
    }),
    rysk.quoteArgs("chan", "rfq", quote()),
    rysk.premiumRequestsArgs(MAKER),
    rysk.premiumQuotesArgs(MAKER),
    rysk.premiumQuoteStatusArgs("id"),
    rysk.premiumQuoteArgs("rfq", quote({ domain: { verifyingContract: HANDLER } })),
    rysk.premiumQuoteBatchArgs("batch.json"),
    rysk.premiumCancelArgs("id", 84532, "43"),
  ];

  for (const args of builders) {
    assert.ok(!args.includes("--private_key"), `${args[0]} ${args[1]} leaks the key`);
    assert.ok(!args.includes("test-key"), `${args[0]} ${args[1]} leaks the key`);
  }
});

test("connect and disconnect address the channel and env url", () => {
  const rysk = sdk();
  assert.deepEqual(rysk.connectArgs("chan", "maker"), [
    "connect",
    "--channel_id",
    "chan",
    "--url",
    "wss://rip-testnet.rysk.finance/maker",
  ]);
  assert.deepEqual(rysk.disconnectArgs("chan"), ["disconnect", "--channel_id", "chan"]);
});

test("quote passes the terms and the booleans it was given", () => {
  const args = sdk().quoteArgs("chan", "rfq-1", quote({ isTakerBuy: true }));

  assert.equal(flagValue(args, "--rfq_id"), "rfq-1");
  assert.equal(flagValue(args, "--asset"), ASSET);
  assert.equal(flagValue(args, "--collateral"), ASSET);
  assert.equal(flagValue(args, "--valid_until"), "1767139500");
  assert.ok(args.includes("--is_put"));
  assert.ok(args.includes("--is_taker_buy"));

  const call = sdk().quoteArgs("chan", "rfq-1", quote({ isPut: false, isTakerBuy: false }));
  assert.ok(!call.includes("--is_put"));
  assert.ok(!call.includes("--is_taker_buy"));
});

test("quote sends the premium asset only when it is set", () => {
  const withAsset = sdk().quoteArgs("chan", "rfq", quote({ premiumAsset: HANDLER }));
  assert.equal(flagValue(withAsset, "--premium_asset"), HANDLER);
  assert.ok(!sdk().quoteArgs("chan", "rfq", quote()).includes("--premium_asset"));
});

test("premium quote carries the domain and the request id", () => {
  const args = sdk().premiumQuoteArgs("req-1", quote({
    domain: { name: "PremiumOptionHandler", version: "1", verifyingContract: HANDLER },
  }));

  assert.deepEqual(args.slice(0, 2), ["premium", "quote"]);
  assert.equal(flagValue(args, "--request_id"), "req-1");
  assert.equal(flagValue(args, "--domain_name"), "PremiumOptionHandler");
  assert.equal(flagValue(args, "--domain_version"), "1");
  assert.equal(flagValue(args, "--domain_verifying_contract"), HANDLER);
});

test("premium url is only passed when given", () => {
  const rysk = sdk();
  assert.ok(!rysk.premiumRequestsArgs(MAKER).includes("--url"));
  assert.equal(
    flagValue(rysk.premiumRequestsArgs(MAKER, "http://localhost:8080"), "--url"),
    "http://localhost:8080",
  );
});

test("a domain the cli cannot sign is rejected before spawning", () => {
  const rysk = sdk();

  assert.throws(
    () => rysk.premiumQuoteArgs("req", quote()),
    /missing verifyingContract/,
  );
  assert.throws(
    () => rysk.premiumQuoteArgs("req", quote({ domain: { verifyingContract: HANDLER, salt: "0x01" } })),
    /salt is not supported/,
  );
  assert.throws(
    () =>
      rysk.premiumQuoteArgs(
        "req",
        quote({ domain: { verifyingContract: HANDLER, chainId: 8453 } }),
      ),
    /does not match the quote's chain 84532/,
  );
  // a hex chain id that does match is fine
  assert.ok(
    rysk.premiumQuoteArgs(
      "req",
      quote({ domain: { verifyingContract: HANDLER, chainId: "0x14a34" } }),
    ),
  );
});

test("websocket quote rejects a partial domain, premium defaults its name", () => {
  const rysk = sdk();
  assert.throws(
    () => rysk.quoteArgs("chan", "rfq", quote({ domain: { name: "rysk" } })),
    /missing version, verifyingContract/,
  );

  const premium = rysk.premiumQuoteArgs("req", quote({ domain: { verifyingContract: HANDLER } }));
  assert.ok(!premium.includes("--domain_name"));
  assert.equal(flagValue(premium, "--domain_verifying_contract"), HANDLER);
});

test("premiumQuoteBatch serialises entries the cli can read", () => {
  const batch = JSON.parse(
    sdk().premiumQuoteBatch([
      { requestId: "req-0", quote: quote({ domain: { verifyingContract: HANDLER } }) },
      { requestId: "req-1", quote: quote({ nonce: "43", domain: { verifyingContract: HANDLER } }) },
    ]),
  );

  assert.equal(batch.length, 2);
  assert.equal(batch[0].requestId, "req-0");
  assert.equal(batch[1].nonce, "43");
  assert.equal(batch[0].domain.verifyingContract, HANDLER);
  assert.equal(batch[0].assetAddress, ASSET);
});

test("premiumQuoteBatch refuses an entry it could not sign", () => {
  assert.throws(
    () => sdk().premiumQuoteBatch([{ requestId: "req", quote: quote() }]),
    /missing verifyingContract/,
  );
});

test("NonceCounter never repeats or rewinds", () => {
  const file = join(mkdtempSync(join(tmpdir(), "rysk-nonce-")), "counter");

  const first = new NonceCounter(file);
  const issued = [first.next(), first.next(), first.next()];
  assert.deepEqual(issued, [...new Set(issued)], "handed out a nonce twice");
  assert.ok(issued.every((nonce) => /^\d+$/.test(nonce)), "nonces have to be decimal strings");
  for (let i = 1; i < issued.length; i++) {
    assert.ok(Number(issued[i]) > Number(issued[i - 1]), "nonces have to climb");
  }

  // a restart continues from the file, not from zero
  const second = new NonceCounter(file);
  assert.ok(Number(second.next()) > Number(issued.at(-1)));

  // a stored counter ahead of the clock still wins
  writeFileSync(file, "99999999999999");
  assert.equal(new NonceCounter(file).next(), "100000000000000");

  // and a corrupt file falls back to the clock rather than throwing
  writeFileSync(file, "not a number");
  assert.ok(Number(new NonceCounter(file).next()) > 0);
  assert.equal(readFileSync(file, "utf8").includes("not"), false);
});

test("predicates accept both websocket and premium requests", () => {
  const websocket = {
    asset: ASSET,
    assetName: "ETH",
    chainId: 84532,
    expiry: 1767225600,
    isPut: false,
    quantity: "1",
    strike: "1",
    taker: MAKER,
    usd: ASSET,
    collateralAsset: ASSET,
  };
  const premium = {
    id: "b7c2",
    asset: ASSET,
    chainId: 84532,
    expiry: 1767225600,
    isPut: false,
    isTakerBuy: false,
    quantity: "1",
    strike: "1",
    taker: MAKER,
    usd: ASSET,
    collateralAsset: ASSET,
    validUntil: 1787740878814,
    makers: [],
    premiumAsset: ASSET,
    isPremium: true,
    createdAt: 1787740279,
    typeDataDomain: { name: "PremiumOptionHandler", version: "1", verifyingContract: HANDLER },
  };

  assert.ok(isRequest(websocket), "websocket request rejected");
  assert.ok(isRequest(premium), "premium request rejected");
  assert.ok(!isRequest({ ...premium, chainId: "84532" }), "a string chainId is not a request");
  assert.ok(!isRequest({ ...premium, isPremium: "yes" }), "a string flag is not a bool");
  assert.ok(!isRequest(null));
});

test("the remaining predicates hold", () => {
  assert.ok(isQuote({ ...quote(), signature: "0x00" }));
  assert.ok(!isQuote(quote()), "a quote without a signature is not on the wire yet");
  assert.ok(
    isTransfer({ user: MAKER, amount: "1", asset: ASSET, chain_id: 1, isDeposit: true, nonce: "1" }),
  );
  assert.ok(isJSONRPCResponse({ jsonrpc: "2.0", id: "1", result: {} }));
  assert.ok(!isJSONRPCResponse({ jsonrpc: "2.0", id: 1, result: {} }));
  assert.ok(
    isQuoteNotification({
      rfqId: "1",
      assetAddress: ASSET,
      chainId: 84532,
      newBest: "1",
      yours: "1",
    }),
  );
});
