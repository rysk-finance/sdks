import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import Rysk, { Env, NonceCounter } from "../dist/index.js";
import {
  isErrorData,
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

test("approve leaves the asset to the cli unless one is given", () => {
  const rysk = sdk();

  const fallback = rysk.approveArgs(84532, "1", "https://rpc");
  assert.ok(!fallback.includes("--asset"), "an absent asset must not reach the cli");

  const explicit = rysk.approveArgs(84532, "1", "https://rpc", ASSET);
  assert.equal(flagValue(explicit, "--asset"), ASSET);
});

test("approve can be told to approve any erc20", () => {
  const other = "0x1111111111111111111111111111111111111111";
  const args = sdk().approveArgs(84532, "1", "https://rpc", other);
  assert.equal(flagValue(args, "--asset"), other);
  assert.equal(flagValue(args, "--amount"), "1");
});

test("no arg builder carries the private key", () => {
  const rysk = sdk();
  const builders = [
    rysk.connectArgs("maker"),
    rysk.approveArgs(84532, "1", "https://rpc"),
    rysk.balancesArgs(MAKER),
    rysk.positionsArgs(MAKER),
    rysk.transferArgs({
      user: MAKER,
      amount: "1",
      asset: ASSET,
      chain_id: 84532,
      is_deposit: true,
      nonce: "1",
    }),
    rysk.quoteArgs("rfq", quote()),
    rysk.premiumRequestsArgs(MAKER),
    rysk.premiumQuotesArgs(MAKER),
    rysk.premiumQuoteStatusArgs("id"),
    rysk.premiumQuoteBatchArgs("batch.json"),
    rysk.premiumCancelArgs("id", 84532, "43"),
    rysk.premiumRequestArgs("requests.json", 84532, "44"),
    rysk.premiumCancelRequestArgs("id", 84532, "45"),
    rysk.premiumExecuteArgs("7", "46"),
    rysk.willRedeemArgs(84532, MAKER, ASSET, true, "1"),
    rysk.selfRedeemArgs(84532, "1"),
  ];

  for (const args of builders) {
    assert.ok(!args.includes("--private_key"), `${args[0]} ${args[1]} leaks the key`);
    assert.ok(!args.includes("test-key"), `${args[0]} ${args[1]} leaks the key`);
  }
});

test("connect addresses the env url", () => {
  assert.deepEqual(sdk().connectArgs("maker"), [
    "connect",
    "--url",
    "wss://rip-testnet.rysk.finance/maker",
  ]);
});

test("send writes one json args line into the process's stdin", async () => {
  const rysk = sdk();
  rysk._cli_path = "cat"; // echoes stdin back, standing in for connect
  const proc = rysk.execute();
  const args = sdk().quoteArgs("rfq-1", quote());

  try {
    const line = new Promise((resolve) => proc.on("message", (data) => resolve(data.toString())));
    proc.send(args);
    // a message is exactly one line, newline stripped
    assert.deepEqual(JSON.parse(await line), args);
  } finally {
    proc.close();
  }
});

test("no arg builder emits an empty arg", () => {
  const rysk = sdk();
  for (const args of [
    rysk.quoteArgs("rfq", quote({ isPut: false, isTakerBuy: false })),
    rysk.transferArgs({ user: MAKER, amount: "1", asset: ASSET, chain_id: 84532, is_deposit: false, nonce: "1" }),
  ]) {
    assert.ok(!args.includes(""), `${args[0]} carries "", which the cli rejects`);
  }
});

test("quote passes the terms and the booleans it was given", () => {
  const args = sdk().quoteArgs("rfq-1", quote({ isTakerBuy: true }));

  assert.equal(flagValue(args, "--rfq_id"), "rfq-1");
  assert.equal(flagValue(args, "--asset"), ASSET);
  assert.equal(flagValue(args, "--collateral"), ASSET);
  assert.equal(flagValue(args, "--valid_until"), "1767139500");
  assert.ok(args.includes("--is_put"));
  assert.ok(args.includes("--is_taker_buy"));

  const call = sdk().quoteArgs("rfq-1", quote({ isPut: false, isTakerBuy: false }));
  assert.ok(!call.includes("--is_put"));
  assert.ok(!call.includes("--is_taker_buy"));
});

test("quote sends the premium asset only when it is set", () => {
  const withAsset = sdk().quoteArgs("rfq", quote({ premiumAsset: HANDLER }));
  assert.equal(flagValue(withAsset, "--premium_asset"), HANDLER);
  assert.ok(!sdk().quoteArgs("rfq", quote()).includes("--premium_asset"));
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
  const batch = (domain) => rysk.premiumQuoteBatch([{ requestId: "req", quote: quote({ domain }) }]);

  assert.throws(() => batch(undefined), /missing verifyingContract/);
  assert.throws(() => batch({ verifyingContract: HANDLER, salt: "0x01" }), /salt is not supported/);
  assert.throws(
    () => batch({ verifyingContract: HANDLER, chainId: 8453 }),
    /does not match the quote's chain 84532/,
  );
  // a hex chain id that does match is fine
  assert.ok(batch({ verifyingContract: HANDLER, chainId: "0x14a34" }));
});

test("websocket quote takes the domain all or nothing", () => {
  const rysk = sdk();
  assert.throws(
    () => rysk.quoteArgs("rfq", quote({ domain: { name: "rysk" } })),
    /missing version, verifyingContract/,
  );
  const args = rysk.quoteArgs("rfq", quote({ domain: { name: "rysk", version: "0.0.0", verifyingContract: HANDLER } }));
  assert.equal(flagValue(args, "--domain_verifying_contract"), HANDLER);
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
    isTransfer({ user: MAKER, amount: "1", asset: ASSET, chainId: 1, isDeposit: true, nonce: "1" }),
  );
  assert.ok(isJSONRPCResponse({ jsonrpc: "2.0", id: "1", result: {} }));
  assert.ok(!isJSONRPCResponse({ jsonrpc: "2.0", id: 1, result: {} }));
  assert.ok(isErrorData({ code: -32000, message: "nope" }));
  assert.ok(!isErrorData({ code: "-32000", message: "nope" }));
  assert.ok(!isErrorData({ code: -32000 }));
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

test("a failed call is still a response", () => {
  // The cli sends error and omits result entirely, so a guard that insists on a
  // well formed result drops every error it reports.
  assert.ok(
    isJSONRPCResponse({
      jsonrpc: "2.0",
      id: "1",
      error: { code: -32000, message: "nope" },
    }),
  );

  // neither half is not a response either way
  assert.ok(!isJSONRPCResponse({ jsonrpc: "2.0", id: "1" }));
  // a malformed error cannot stand in for a result
  assert.ok(
    !isJSONRPCResponse({ jsonrpc: "2.0", id: "1", error: { message: "no code" } }),
  );
});

test("an error carries its free form data through", () => {
  const err = { code: -32000, message: "nope", data: { why: "late" } };
  assert.ok(isErrorData(err));
  assert.deepEqual(err.data, { why: "late" });
});

test("will_redeem and self-redeem spell the cli's flags", () => {
  const rysk = sdk();
  assert.deepEqual(rysk.willRedeemArgs(84532, MAKER, ASSET, true, "7"), [
    "will_redeem", "--chain_id", "84532", "--account", MAKER, "--otoken", ASSET, "--nonce", "7", "--will_redeem",
  ]);
  assert.ok(!rysk.willRedeemArgs(84532, MAKER, ASSET, false, "7").includes("--will_redeem"));
  assert.deepEqual(rysk.selfRedeemArgs(84532, "8"), [
    "self-redeem", "--url", "https://rip-testnet.rysk.finance", "--chain_id", "84532", "--nonce", "8",
  ]);
  assert.deepEqual(rysk.selfRedeemArgs(84532, "8", 1767225600, [ASSET, MAKER], "http://x").slice(2), [
    "http://x", "--chain_id", "84532", "--nonce", "8", "--expiry", "1767225600", "--exclude_assets", `${ASSET},${MAKER}`,
  ]);
});

test("premium trades filters only when given", () => {
  assert.deepEqual(sdk().premiumTradesArgs(MAKER), ["premium", "trades", "--account", MAKER]);
  assert.deepEqual(sdk().premiumTradesArgs(MAKER, 84532, "0x6e37fb1f933f53f389a072277d7f942593a760dd", "http://x"), [
    "premium", "trades", "--account", MAKER, "--chain_id", "84532", "--pool", "0x6e37fb1f933f53f389a072277d7f942593a760dd", "--url", "http://x",
  ]);
});
