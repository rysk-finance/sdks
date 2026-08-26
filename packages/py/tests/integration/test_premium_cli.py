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
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Callable, List

import pytest

from ryskV12.client import Env, Rysk
from ryskV12.models import Quote, TypedDataDomain

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


def test_cli_signs_and_posts_what_the_sdk_built(sdk):
    api = FakeApi(json_ok(b'{"failures":[]}'))
    try:
        code, stdout, stderr = run(sdk, sdk.premium_quote_args("req-1", quote(), api.url))
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


def test_a_rejected_quote_fails_the_command(sdk):
    api = FakeApi(json_ok(b'{"failures":[{"error":"request expired","quote":{}}]}'))
    try:
        code, stdout, stderr = run(sdk, sdk.premium_quote_args("req-1", quote(), api.url))
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


def test_cli_refuses_a_quote_it_cannot_get_accepted(sdk):
    api = FakeApi(json_ok(b'{"failures":[]}'))
    try:
        # the window is checked before signing, so nothing reaches the api
        too_far = quote(validUntil=int(time.time()) + 3600)
        code, _, stderr = run(sdk, sdk.premium_quote_args("req-1", too_far, api.url))
        assert code != 0
        assert "outside the api's window" in stderr
        assert api.seen == [], "a doomed quote must not cost a round trip"
    finally:
        api.close()
