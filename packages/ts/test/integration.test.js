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
import { isRequest } from "../dist/models.js";

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
    const proc = sdk.execute(args);
    let stdout = "";
    let stderr = "";
    proc.on("message", (line) => (stdout += line.toString() + "\n"));
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

test("open reaches a listener attached after execute", { skip: !available }, async () => {
  const proc = sdk().execute(["version"]);
  let opened = false;
  proc.on("open", () => (opened = true));
  await new Promise((resolve) => proc.on("close", resolve));
  assert.ok(opened);
});

/** Writes quotes as a batch file and returns the args that post it. */
const quoteBatchArgs = (rysk, url, ...entries) => {
  const file = join(mkdtempSync(join(tmpdir(), "rysk-batch-")), "batch.json");
  writeFileSync(file, rysk.premiumQuoteBatch(entries));
  return rysk.premiumQuoteBatchArgs(file, url);
};

test("the cli signs and posts what the sdk built", { skip: !available }, async () => {
  const api = await fakeApi((_req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end('{"failures":[]}');
  });

  try {
    const result = await run(sdk(), quoteBatchArgs(sdk(), api.url, { requestId: "req-1", quote: quote() }));
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
    const result = await run(sdk(), quoteBatchArgs(sdk(), api.url, { requestId: "req-1", quote: quote() }));
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

test("taker writes reach the api: request, cancel-request, execute", { skip: !available }, async () => {
  const api = await fakeApi((req, res) => {
    const bodies = {
      "/api/quotes/7": `{"requestId":"r1","maker":"${MAKER}","price":"1","nonce":"11","signature":"0xab","validUntil":1767225000}`,
      "/api/requests/r1": `{"asset":"${ASSET}","chainId":84532,"expiry":1767225600,"quantity":"1","strike":"1","taker":"${MAKER}","usd":"${ASSET}","collateralAsset":"${ASSET}","premiumAsset":"${ASSET}"}`,
      [`/api/pools/${MAKER}`]: `{"optionHandler":"${HANDLER}"}`,
    };
    if (req.method === "GET") return res.writeHead(200).end(bodies[req.url]);
    res.writeHead(req.url === "/api/execute" ? 201 : 204).end(req.url === "/api/execute" ? "0xfeed" : "");
  });

  try {
    const rysk = sdk();
    const file = join(mkdtempSync(join(tmpdir(), "rysk-")), "requests.json");
    writeFileSync(file, '[{"taker":"0x1"}]');

    assert.equal((await run(rysk, rysk.premiumRequestArgs(file, 84532, "5", api.url))).code, 0);
    assert.equal(api.seen.at(-1).body, '[{"taker":"0x1"}]');
    assert.equal(api.seen.at(-1).headers["x-nonce"], "5");

    assert.equal((await run(rysk, rysk.premiumCancelRequestArgs("r1", 84532, "6", api.url))).code, 0);
    assert.equal(`${api.seen.at(-1).method} ${api.seen.at(-1).path}`, "DELETE /api/requests/r1");

    const executed = await run(rysk, rysk.premiumExecuteArgs("7", "8", api.url));
    assert.equal(executed.code, 0, executed.stderr);
    assert.equal(executed.stdout, "0xfeed");
    assert.equal(JSON.parse(api.seen.at(-1).body).nonce, "8");
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
      [rysk.premiumPoolsArgs(api.url), "/api/pools"],
      [rysk.premiumTakerRequestsArgs(MAKER, api.url), "/api/requests/taker"],
      [rysk.premiumRequestQuotesArgs("b7c2", api.url), "/api/requests/b7c2/quotes"],
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
    const result = await run(sdk(), quoteBatchArgs(sdk(), api.url, { requestId: "req-1", quote: tooFar }));
    assert.notEqual(result.code, 0);
    assert.match(result.stderr, /outside the api's window/);
    assert.equal(api.seen.length, 0, "a doomed quote must not cost a round trip");
  } finally {
    api.close();
  }
});

// Opt in against the real testnet api (`make e2e` reads the keys from the repo's
// .env): a taker opens an rfq only the maker may quote, the maker polls for it,
// quotes, cancels and quotes again, and the taker takes the trade on chain. The
// trade is real but costs the maker 1 wei; it relies on the maker's premium
// allowance for the pool's option handler, which the go e2e sets up.
const E2E_KEY = process.env.RYSK_E2E_PRIVATE_KEY;
const E2E_MAKER = process.env.RYSK_E2E_MAKER?.toLowerCase();
const E2E_TAKER_KEY = process.env.RYSK_E2E_TAKER_PRIVATE_KEY;
const E2E_POOL = process.env.RYSK_E2E_POOL?.toLowerCase();
const E2E_URL = process.env.RYSK_E2E_PREMIUM_URL || "https://insti-testnet.rysk.finance";
// base sepolia's strike asset, which a request's usd has to be
const E2E_USD = "0x98d56648c9b7f3cb49531f4135115b5000ab1733";

test(
  "testnet: open an rfq, quote, cancel, requote and take the trade",
  { skip: !available || !E2E_KEY || !E2E_MAKER || !E2E_TAKER_KEY || !E2E_POOL },
  async () => {
    const maker = new Rysk(Env.TESTNET, E2E_KEY, CLI);
    const taker = new Rysk(Env.TESTNET, E2E_TAKER_KEY, CLI);
    const ok = async (sdk, args) => {
      const result = await run(sdk, args);
      assert.equal(result.code, 0, `${args[1]}: ${result.stderr}\n${result.stdout}`);
      return result.stdout;
    };
    const json = async (sdk, args) => JSON.parse((await ok(sdk, args)) || "[]");
    // unix nanoseconds: spent once per address, fresh across runs
    let last = 0n;
    const nonce = () => {
      last = BigInt(Date.now()) * 1_000_000n > last ? BigInt(Date.now()) * 1_000_000n : last + 1n;
      return last.toString();
    };

    // 1. the taker opens a request on the pool, only this maker may quote
    const pool = (await json(taker, taker.premiumPoolsArgs(E2E_URL))).find((p) => p.address.toLowerCase() === E2E_POOL);
    assert.ok(pool, `pool ${E2E_POOL} is not listed`);
    const expiry = new Date();
    expiry.setUTCHours(8, 0, 0, 0);
    expiry.setUTCDate(expiry.getUTCDate() + 7);
    while (expiry.getUTCDay() !== 5) expiry.setUTCDate(expiry.getUTCDate() + 1);
    const file = join(mkdtempSync(join(tmpdir(), "rysk-e2e-")), "requests.json");
    writeFileSync(
      file,
      JSON.stringify([
        {
          asset: pool.underlying,
          chainId: pool.chainId,
          expiry: Math.floor(expiry.getTime() / 1000),
          isPut: pool.type === "Put",
          isTakerBuy: false,
          quantity: "1000000000000000000",
          strike: "100000000000000",
          taker: pool.address,
          usd: E2E_USD,
          collateralAsset: pool.asset,
          premiumAsset: pool.asset,
          isPremium: true,
          makers: [E2E_MAKER],
          validUntil: Date.now() + 5 * 60_000,
        },
      ]),
    );
    await ok(taker, taker.premiumRequestArgs(file, pool.chainId, nonce(), E2E_URL));

    // 2. the maker polls its requests until that one shows up
    let request;
    for (let i = 0; i < 30 && !request; i++) {
      request = (await json(maker, maker.premiumRequestsArgs(E2E_MAKER, E2E_URL)))
        .filter((r) => isRequest(r) && r.taker.toLowerCase() === E2E_POOL)
        .at(-1);
      if (!request) await new Promise((resolve) => setTimeout(resolve, 1000));
    }
    assert.ok(request, `no request from ${E2E_POOL} listed for ${E2E_MAKER}`);

    const quote = async () => {
      const n = nonce();
      await ok(
        maker,
        quoteBatchArgs(maker, E2E_URL, {
          requestId: request.id,
          quote: {
            assetAddress: request.asset,
            chainId: request.chainId,
            expiry: request.expiry,
            isPut: request.isPut,
            isTakerBuy: Boolean(request.isTakerBuy),
            quantity: request.quantity,
            strike: request.strike,
            usd: request.usd,
            collateralAsset: request.collateralAsset,
            maker: E2E_MAKER,
            nonce: n,
            price: "1",
            validUntil: Math.floor(Date.now() / 1000) + 300,
            domain: request.typeDataDomain,
          },
        }),
      );
      return n;
    };
    const onBook = async (n) =>
      (await json(maker, maker.premiumQuotesArgs(E2E_MAKER, E2E_URL))).find((q) => String(q.nonce) === n)?.id;

    // 3-4. quote, then cancel it off the book
    const first = await quote();
    const id = await onBook(first);
    assert.ok(id, `quote with nonce ${first} is not in the maker's live quotes`);
    await ok(maker, maker.premiumCancelArgs(String(id), request.chainId, nonce(), E2E_URL));
    assert.equal(await onBook(first), undefined, `quote ${id} is still live after cancel`);

    // 5-6. quote again, and the taker takes it from the request's book
    const second = await quote();
    const picked = (await json(taker, taker.premiumRequestQuotesArgs(request.id, E2E_URL))).find(
      (q) => String(q.nonce) === second,
    );
    assert.ok(picked, `quote with nonce ${second} is not on request ${request.id}'s book`);
    const tx = await ok(taker, taker.premiumExecuteArgs(String(picked.id), nonce(), E2E_URL));
    assert.match(tx, /^0x[0-9a-f]{64}$/i);
    assert.equal(await onBook(second), undefined, `quote ${picked.id} is still live after the trade`);
  },
);
