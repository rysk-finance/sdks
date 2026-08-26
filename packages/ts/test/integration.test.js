// Drives the real CLI binary against a fake premium api: the arg builders and the
// binary have to agree on flags, the key has to travel in the environment, and a
// rejected quote has to fail the command.
//
// Skipped when no CLI is present. `make dev-bin` from the repo root builds one.
import assert from "node:assert/strict";
import { existsSync, mkdtempSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import Rysk, { Env } from "../dist/index.js";

const CLI = process.env.RYSK_CLI_PATH ?? "./ryskV12";
// well known test key; its address is the maker below
const PK = "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318";
const MAKER = "0x2c7536E3605D9C16a7a3D7b1898e529396a65c23";
const HANDLER = "0x54f1cc396e08f0defbe956bcddf6abe46d61cb48";
const ASSET = "0x98d56648c9b7f3cb49531f4135115b5000ab1733";

const available = existsSync(CLI);

/** A fake api that records what reached it and answers with `respond`. */
const fakeApi = async (respond) => {
  const seen = [];
  const server = createServer((req, res) => {
    let body = "";
    req.on("data", (chunk) => (body += chunk));
    req.on("end", () => {
      seen.push({ method: req.method, path: req.url, headers: req.headers, body });
      respond(req, res, seen.length);
    });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  return {
    url: `http://127.0.0.1:${server.address().port}`,
    seen,
    close: () => server.close(),
  };
};

const run = (sdk, args) =>
  new Promise((resolve) => {
    const proc = sdk.execute(args.filter((arg) => arg !== ""));
    let stdout = "";
    let stderr = "";
    proc.on("message", (data) => (stdout += data.toString()));
    proc.on("stderr", (data) => (stderr += data.toString()));
    proc.on("close", (code) => resolve({ code, stdout: stdout.trim(), stderr: stderr.trim() }));
  });

const sdk = () => new Rysk(Env.TESTNET, PK, CLI);

const quote = (extra = {}) => ({
  assetAddress: ASSET,
  chainId: 84532,
  expiry: 1767225600,
  isPut: true,
  isTakerBuy: false,
  maker: MAKER,
  nonce: String(Date.now()),
  price: "1250000000000000000",
  quantity: "1000000000000000000",
  strike: "300000000000",
  // the api takes a window of now+2min .. now+10min
  validUntil: Math.floor(Date.now() / 1000) + 300,
  usd: ASSET,
  collateralAsset: ASSET,
  domain: { verifyingContract: HANDLER },
  ...extra,
});

test("the cli signs and posts what the sdk built", { skip: !available }, async () => {
  const api = await fakeApi((_req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end('{"failures":[]}');
  });

  try {
    const result = await run(sdk(), sdk().premiumQuoteArgs("req-1", quote(), api.url));
    assert.equal(result.code, 0, result.stderr);
    assert.equal(result.stdout, '{"failures":[]}');

    const [call] = api.seen;
    assert.equal(call.method, "POST");
    assert.equal(call.path, "/api/quotes");

    const posted = JSON.parse(call.body);
    assert.equal(posted.length, 1);
    assert.deepEqual(Object.keys(posted[0]).sort(), [
      "maker",
      "nonce",
      "price",
      "requestId",
      "signature",
      "validUntil",
    ]);
    assert.match(posted[0].signature, /^0x[0-9a-f]{130}$/);
  } finally {
    api.close();
  }
});

test("a batch is one call", { skip: !available }, async () => {
  const api = await fakeApi((_req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end('{"failures":[]}');
  });

  try {
    const entries = Array.from({ length: 5 }, (_, i) => ({
      requestId: `req-${i}`,
      quote: quote({ nonce: String(Date.now() + i) }),
    }));
    const file = join(mkdtempSync(join(tmpdir(), "rysk-batch-")), "batch.json");
    writeFileSync(file, sdk().premiumQuoteBatch(entries));

    const result = await run(sdk(), sdk().premiumQuoteBatchArgs(file, api.url));
    assert.equal(result.code, 0, result.stderr);
    assert.equal(api.seen.length, 1, "a batch has to be one request");
    assert.equal(JSON.parse(api.seen[0].body).length, 5);
  } finally {
    api.close();
  }
});

test("a rejected quote fails the command", { skip: !available }, async () => {
  const api = await fakeApi((_req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end('{"failures":[{"error":"request expired","quote":{}}]}');
  });

  try {
    const result = await run(sdk(), sdk().premiumQuoteArgs("req-1", quote(), api.url));
    assert.notEqual(result.code, 0, "the api answers 200, the exit code is the result");
    assert.match(result.stdout, /request expired/, "the response still reaches stdout");
    assert.match(result.stderr, /1 of 1 quotes rejected/);
  } finally {
    api.close();
  }
});

test("rate limiting and a host without the routes are told apart", { skip: !available }, async () => {
  const api = await fakeApi((_req, res, nth) => {
    if (nth === 1) {
      res.writeHead(429).end("cc");
      return;
    }
    res.writeHead(404).end("404 page not found");
  });

  try {
    const limited = await run(sdk(), sdk().premiumRequestsArgs(MAKER, api.url));
    assert.notEqual(limited.code, 0);
    assert.match(limited.stderr, /rate limited/);

    const missing = await run(sdk(), sdk().premiumRequestsArgs(MAKER, api.url));
    assert.notEqual(missing.code, 0);
    assert.match(missing.stderr, /no rfq routes/);
  } finally {
    api.close();
  }
});

test("cancel authenticates with headers, not a key in argv", { skip: !available }, async () => {
  const api = await fakeApi((_req, res) => res.writeHead(204).end());

  try {
    const args = sdk().premiumCancelArgs("24361", 84532, "43", api.url);
    assert.ok(!args.includes("--private_key"));

    const result = await run(sdk(), args);
    assert.equal(result.code, 0, result.stderr);

    const [call] = api.seen;
    assert.equal(call.method, "DELETE");
    assert.equal(call.path, "/api/quotes/24361");
    assert.equal(call.headers["x-chain-id"], "84532");
    assert.equal(call.headers["x-nonce"], "43");
    assert.match(call.headers["x-signature"], /^0x[0-9a-f]{130}$/);
  } finally {
    api.close();
  }
});

test("reads proxy the api verbatim", { skip: !available }, async () => {
  const api = await fakeApi((_req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end('[{"id":"b7c2"}]');
  });

  try {
    const rysk = sdk();
    for (const [args, path] of [
      [rysk.premiumRequestsArgs(MAKER, api.url), "/api/requests/maker"],
      [rysk.premiumQuotesArgs(MAKER, api.url), "/api/quotes"],
      [rysk.premiumQuoteStatusArgs("9f31", api.url), "/api/quotes/9f31"],
    ]) {
      const result = await run(rysk, args);
      assert.equal(result.code, 0, result.stderr);
      assert.equal(result.stdout, '[{"id":"b7c2"}]');
      assert.ok(api.seen.at(-1).path.startsWith(path), `${args[1]} hit ${api.seen.at(-1).path}`);
    }
  } finally {
    api.close();
  }
});

test("the cli refuses a quote it cannot get accepted", { skip: !available }, async () => {
  const api = await fakeApi((_req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end('{"failures":[]}');
  });

  try {
    // the window is checked before signing, so nothing reaches the api
    const tooFar = quote({ validUntil: Math.floor(Date.now() / 1000) + 3600 });
    const result = await run(sdk(), sdk().premiumQuoteArgs("req-1", tooFar, api.url));
    assert.notEqual(result.code, 0);
    assert.match(result.stderr, /outside the api's window/);
    assert.equal(api.seen.length, 0, "a doomed quote must not cost a round trip");
  } finally {
    api.close();
  }
});
