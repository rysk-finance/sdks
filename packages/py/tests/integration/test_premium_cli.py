"""Drives the real CLI binary against a fake premium api: the arg builders and the
binary have to agree on flags, the key has to travel in the environment, and a
rejected quote has to fail the command.

Skipped when no CLI is present. `make dev-bin` from the repo root builds one.
"""

import json
import os
import re
import threading
import time
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Callable, List

import pytest

from ryskV12.client import Env, Rysk
from ryskV12.models import Quote, Request, TypedDataDomain, is_request

CLI = os.environ.get("RYSK_CLI_PATH", "./ryskV12cli")
# well known test key; its address is the maker below
PK = "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
MAKER = "0x2c7536E3605D9C16a7a3D7b1898e529396a65c23"
HANDLER = "0x54f1cc396e08f0defbe956bcddf6abe46d61cb48"
ASSET = "0x98d56648c9b7f3cb49531f4135115b5000ab1733"

pytestmark = pytest.mark.skipif(
    not os.path.exists(CLI), reason=f"no cli at {CLI}; run `make dev-bin`"
)


class FakeApi:
    """Records what reached it and answers with whatever `respond` writes."""

    def __init__(self, respond: Callable[[BaseHTTPRequestHandler, int], None]):
        self.seen: List[dict] = []
        api = self

        class Handler(BaseHTTPRequestHandler):
            def _record(self):
                length = int(self.headers.get("Content-Length", 0))
                api.seen.append(
                    {
                        "method": self.command,
                        "path": self.path,
                        "headers": {k.lower(): v for k, v in self.headers.items()},
                        "body": self.rfile.read(length).decode() if length else "",
                    }
                )
                respond(self, len(api.seen))

            do_GET = _record
            do_POST = _record
            do_DELETE = _record

            def log_message(self, *args):
                pass

        self._server = HTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self._server.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self._server.server_port}"

    def close(self):
        self._server.shutdown()
        self._server.server_close()


def json_ok(body: bytes):
    def respond(handler: BaseHTTPRequestHandler, _nth: int):
        handler.send_response(200)
        handler.send_header("Content-Type", "application/json")
        handler.end_headers()
        handler.wfile.write(body)

    return respond


@pytest.fixture
def sdk():
    return Rysk(env=Env.TESTNET, private_key=PK, v12_cli_path=CLI)


def quote(**overrides) -> Quote:
    fields = dict(
        assetAddress=ASSET,
        chainId=84532,
        expiry=1767225600,
        isPut=True,
        isTakerBuy=False,
        maker=MAKER,
        nonce=str(int(time.time() * 1000)),
        price="1250000000000000000",
        quantity="1000000000000000000",
        strike="300000000000",
        # the api takes a window of now+2min .. now+10min
        validUntil=int(time.time()) + 300,
        usd=ASSET,
        collateralAsset=ASSET,
        domain=TypedDataDomain(verifyingContract=HANDLER),
    )
    fields.update(overrides)
    return Quote(**fields)


def run(sdk: Rysk, args: List[str]):
    proc = sdk.execute(args)
    stdout, stderr = proc.communicate()
    return proc.returncode, stdout.strip(), stderr.strip()


def one_quote_args(sdk: Rysk, tmp_path, request_id: str, q: Quote, url: str) -> List[str]:
    """Writes a batch of one quote and returns the args that post it."""
    batch = tmp_path / f"quote-{q.nonce}.json"
    batch.write_text(sdk.premium_quote_batch([(request_id, q)]))
    return sdk.premium_quote_batch_args(str(batch), url)


def test_cli_signs_and_posts_what_the_sdk_built(sdk, tmp_path):
    api = FakeApi(json_ok(b'{"failures":[]}'))
    try:
        code, stdout, stderr = run(sdk, one_quote_args(sdk, tmp_path, "req-1", quote(), api.url))
        assert code == 0, stderr
        assert stdout == '{"failures":[]}'

        call = api.seen[0]
        assert call["method"] == "POST"
        assert call["path"] == "/api/quotes"

        posted = json.loads(call["body"])
        assert len(posted) == 1
        assert sorted(posted[0]) == [
            "maker",
            "nonce",
            "price",
            "requestId",
            "signature",
            "validUntil",
        ]
        assert re.fullmatch(r"0x[0-9a-f]{130}", posted[0]["signature"])
    finally:
        api.close()


def test_a_batch_is_one_call(sdk, tmp_path):
    api = FakeApi(json_ok(b'{"failures":[]}'))
    try:
        now = int(time.time() * 1000)
        entries = [(f"req-{i}", quote(nonce=str(now + i))) for i in range(5)]
        batch = tmp_path / "batch.json"
        batch.write_text(sdk.premium_quote_batch(entries))

        code, _, stderr = run(sdk, sdk.premium_quote_batch_args(str(batch), api.url))
        assert code == 0, stderr
        assert len(api.seen) == 1, "a batch has to be one request"
        assert len(json.loads(api.seen[0]["body"])) == 5
    finally:
        api.close()


def test_a_rejected_quote_fails_the_command(sdk, tmp_path):
    api = FakeApi(json_ok(b'{"failures":[{"error":"request expired","quote":{}}]}'))
    try:
        code, stdout, stderr = run(sdk, one_quote_args(sdk, tmp_path, "req-1", quote(), api.url))
        assert code != 0, "the api answers 200, the exit code is the result"
        assert "request expired" in stdout, "the response still reaches stdout"
        assert "1 of 1 quotes rejected" in stderr
    finally:
        api.close()


def test_rate_limit_and_missing_routes_are_told_apart(sdk):
    def respond(handler: BaseHTTPRequestHandler, nth: int):
        if nth == 1:
            handler.send_response(429)
            handler.end_headers()
            handler.wfile.write(b"cc")
            return
        handler.send_response(404)
        handler.end_headers()
        handler.wfile.write(b"404 page not found")

    api = FakeApi(respond)
    try:
        code, _, stderr = run(sdk, sdk.premium_requests_args(MAKER, api.url))
        assert code != 0
        assert "rate limited" in stderr

        code, _, stderr = run(sdk, sdk.premium_requests_args(MAKER, api.url))
        assert code != 0
        assert "no rfq routes" in stderr
    finally:
        api.close()


def test_cancel_authenticates_with_headers_not_argv(sdk):
    def respond(handler: BaseHTTPRequestHandler, _nth: int):
        handler.send_response(204)
        handler.end_headers()

    api = FakeApi(respond)
    try:
        args = sdk.premium_cancel_args("24361", 84532, "43", api.url)
        assert "--private_key" not in args

        code, _, stderr = run(sdk, args)
        assert code == 0, stderr

        call = api.seen[0]
        assert call["method"] == "DELETE"
        assert call["path"] == "/api/quotes/24361"
        assert call["headers"]["x-chain-id"] == "84532"
        assert call["headers"]["x-nonce"] == "43"
        assert re.fullmatch(r"0x[0-9a-f]{130}", call["headers"]["x-signature"])
    finally:
        api.close()


def test_reads_proxy_the_api_verbatim(sdk):
    api = FakeApi(json_ok(b'[{"id":"b7c2"}]'))
    try:
        cases = [
            (sdk.premium_requests_args(MAKER, api.url), "/api/requests/maker"),
            (sdk.premium_quotes_args(MAKER, api.url), "/api/quotes"),
            (sdk.premium_quote_status_args("9f31", api.url), "/api/quotes/9f31"),
        ]
        for args, path in cases:
            code, stdout, stderr = run(sdk, args)
            assert code == 0, stderr
            assert stdout == '[{"id":"b7c2"}]'
            assert api.seen[-1]["path"].startswith(path)
    finally:
        api.close()


def test_cli_refuses_a_quote_it_cannot_get_accepted(sdk, tmp_path):
    api = FakeApi(json_ok(b'{"failures":[]}'))
    try:
        # the window is checked before signing, so nothing reaches the api
        too_far = quote(validUntil=int(time.time()) + 3600)
        code, _, stderr = run(sdk, one_quote_args(sdk, tmp_path, "req-1", too_far, api.url))
        assert code != 0
        assert "outside the api's window" in stderr
        assert api.seen == [], "a doomed quote must not cost a round trip"
    finally:
        api.close()


# Opt in against the real testnet api (`make e2e` reads the keys from the repo's
# .env): a taker opens an rfq only the maker may quote, the maker polls for it,
# quotes, cancels and quotes again, and the taker takes the trade on chain. The
# trade is real but costs the maker 1 wei; it relies on the maker's premium
# allowance for the pool's option handler, which the go e2e sets up.
E2E_KEY = os.environ.get("RYSK_E2E_PRIVATE_KEY")
E2E_MAKER = (os.environ.get("RYSK_E2E_MAKER") or "").lower()
E2E_TAKER_KEY = os.environ.get("RYSK_E2E_TAKER_PRIVATE_KEY")
E2E_POOL = (os.environ.get("RYSK_E2E_POOL") or "").lower()
E2E_URL = os.environ.get("RYSK_E2E_PREMIUM_URL") or "https://insti-testnet.rysk.finance"
# base sepolia's strike asset, which a request's usd has to be
E2E_USD = "0x98d56648c9b7f3cb49531f4135115b5000ab1733"


@pytest.mark.skipif(
    not (E2E_KEY and E2E_MAKER and E2E_TAKER_KEY and E2E_POOL),
    reason="set RYSK_E2E_PRIVATE_KEY, RYSK_E2E_MAKER, RYSK_E2E_TAKER_PRIVATE_KEY and RYSK_E2E_POOL",
)
def test_testnet_open_an_rfq_quote_cancel_requote_and_take_the_trade(tmp_path):
    maker = Rysk(env=Env.TESTNET, private_key=E2E_KEY, v12_cli_path=CLI)
    taker = Rysk(env=Env.TESTNET, private_key=E2E_TAKER_KEY, v12_cli_path=CLI)

    def ok(sdk: Rysk, args: List[str]) -> str:
        code, out, err = run(sdk, args)
        assert code == 0, f"{args[1]}: {err}\n{out}"
        return out

    def as_json(sdk: Rysk, args: List[str]):
        return json.loads(ok(sdk, args) or "[]")

    # unix nanoseconds: spent once per address, fresh across runs
    last = 0

    def nonce() -> str:
        nonlocal last
        last = max(time.time_ns(), last + 1)
        return str(last)

    # 1. the taker opens a request on the pool, only this maker may quote
    pool = next(
        (p for p in as_json(taker, taker.premium_pools_args(E2E_URL)) if p["address"].lower() == E2E_POOL),
        None,
    )
    assert pool, f"pool {E2E_POOL} is not listed"
    expiry = datetime.now(timezone.utc).replace(hour=8, minute=0, second=0, microsecond=0) + timedelta(days=7)
    expiry += timedelta(days=(4 - expiry.weekday()) % 7)  # a friday
    file = tmp_path / "requests.json"
    file.write_text(
        json.dumps(
            [
                {
                    "asset": pool["underlying"],
                    "chainId": pool["chainId"],
                    "expiry": int(expiry.timestamp()),
                    "isPut": pool["type"] == "Put",
                    "isTakerBuy": False,
                    "quantity": "1000000000000000000",
                    "strike": "100000000000000",
                    "taker": pool["address"],
                    "usd": E2E_USD,
                    "collateralAsset": pool["asset"],
                    "premiumAsset": pool["asset"],
                    "isPremium": True,
                    "makers": [E2E_MAKER],
                    "validUntil": int(time.time() * 1000) + 5 * 60_000,
                }
            ]
        )
    )
    ok(taker, taker.premium_request_args(str(file), pool["chainId"], nonce(), E2E_URL))

    # 2. the maker polls its requests until that one shows up
    request = None
    for _ in range(30):
        mine = [
            r
            for r in as_json(maker, maker.premium_requests_args(E2E_MAKER, E2E_URL))
            if is_request(r) and r["taker"].lower() == E2E_POOL
        ]
        if mine:
            request = Request.from_json(json.dumps(mine[-1]))
            break
        time.sleep(1)
    assert request, f"no request from {E2E_POOL} listed for {E2E_MAKER}"

    def quote() -> str:
        n = nonce()
        ok(
            maker,
            one_quote_args(
                maker,
                tmp_path,
                request.id,
                Quote(
                    assetAddress=request.asset,
                    chainId=request.chainId,
                    expiry=request.expiry,
                    isPut=request.isPut,
                    isTakerBuy=bool(request.isTakerBuy),
                    quantity=request.quantity,
                    strike=request.strike,
                    usd=request.usd,
                    collateralAsset=request.collateralAsset,
                    maker=E2E_MAKER,
                    nonce=n,
                    price="1",
                    validUntil=int(time.time()) + 300,
                    domain=request.typeDataDomain,
                ),
                E2E_URL,
            ),
        )
        return n

    def on_book(n: str):
        quotes = as_json(maker, maker.premium_quotes_args(E2E_MAKER, E2E_URL))
        return next((str(q["id"]) for q in quotes if str(q.get("nonce")) == n), None)

    # 3-4. quote, then cancel it off the book
    first = quote()
    quote_id = on_book(first)
    assert quote_id, f"quote with nonce {first} is not in the maker's live quotes"
    ok(maker, maker.premium_cancel_args(quote_id, request.chainId, nonce(), E2E_URL))
    assert on_book(first) is None, f"quote {quote_id} is still live after cancel"

    # 5-6. quote again, and the taker takes it from the request's book
    second = quote()
    picked = next(
        (
            q
            for q in as_json(taker, taker.premium_request_quotes_args(request.id, E2E_URL))
            if str(q.get("nonce")) == second
        ),
        None,
    )
    assert picked, f"quote with nonce {second} is not on request {request.id}'s book"
    tx = ok(taker, taker.premium_execute_args(str(picked["id"]), nonce(), E2E_URL))
    assert re.fullmatch(r"0x[0-9a-fA-F]{64}", tx)
    assert on_book(second) is None, f"quote {picked['id']} is still live after the trade"
