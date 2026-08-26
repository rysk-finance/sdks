"""Market maker loop for the premium RFQ api: poll the requests you may quote,
price them, post the quotes in one call, refresh them before their window closes,
and pull everything on the way out.

    RYSK_SDK_PK=<hex private key> \
    RYSK_MAKER=0x<maker address> \
    PREMIUM_URL=https://insti-testnet.rysk.finance \
    python tests/run_premium_example.py

PREMIUM_URL is optional: left out, the CLI talks to production.
"""

import json
import os
import signal
import sys
import time
from typing import Dict, List, Optional, Tuple

from ryskV12.client import Env, Rysk
from ryskV12.models import Quote, Request, is_request

PK = os.environ.get("RYSK_SDK_PK", "")
MAKER = os.environ.get("RYSK_MAKER", "")
PREMIUM_URL = os.environ.get("PREMIUM_URL")

POLL_INTERVAL = 2
# The api takes a validUntil strictly inside now+2min .. now+10min, so a quote has
# to be replaced before it leaves that window.
QUOTE_VALIDITY_SECONDS = 8 * 60
REFRESH_BEFORE_SECONDS = 3 * 60

NONCE_FILE = ".rysk-nonce"
BATCH_FILE = ".rysk-batch.json"

rysk_sdk = Rysk(env=Env.TESTNET, private_key=PK, v12_cli_path="./ryskV12cli")

# request id -> the quote we currently have on the book for it
live: Dict[str, dict] = {}
running = True


def next_nonce() -> str:
    """Nonces are spent once and share one keyspace per address across quotes and
    cancels, so they come from a single counter that survives a restart. A counter
    that rewinds starts failing every write."""
    counter = int(time.time() * 1000)
    try:
        with open(NONCE_FILE) as f:
            counter = max(counter, int(f.read()) + 1)
    except (FileNotFoundError, ValueError):
        pass  # first run, start from the clock
    with open(NONCE_FILE, "w") as f:
        f.write(str(counter))
    return str(counter)


def run_once(args: List[str]) -> str:
    """Runs one CLI command to completion and returns its stdout."""
    proc = rysk_sdk.execute(args)
    stdout, stderr = proc.communicate()
    if proc.returncode != 0:
        # Posting quotes answers 200 with rejections in a failures array, so the
        # CLI exits non zero and stdout still holds the api's response.
        raise RuntimeError(f"{args[1]} exited {proc.returncode}: {(stderr or stdout).strip()}")
    return stdout.strip()


def price_request(request: Request) -> str:
    """Replace with your own pricing. Quantity and strike come from the request."""
    mid = 4 * 10**18
    edge = 110 if request.isTakerBuy else 90  # sell above, buy below
    return str(mid * edge // 100)


def build_quote(request: Request) -> Quote:
    """The terms have to be the request's: the api rebuilds the signed message
    from the stored request, so an altered term only yields a signature that will
    not verify. Ours are the maker's own fields plus the domain the request
    carries."""
    return Quote(
        assetAddress=request.asset,
        chainId=request.chainId,
        expiry=request.expiry,
        isPut=request.isPut,
        isTakerBuy=bool(request.isTakerBuy),
        quantity=request.quantity,
        strike=request.strike,
        usd=request.usd,
        collateralAsset=request.collateralAsset,
        maker=MAKER,
        nonce=next_nonce(),
        price=price_request(request),
        validUntil=int(time.time()) + QUOTE_VALIDITY_SECONDS,
        domain=request.typeDataDomain,
    )


def post_quotes(pairs: List[Tuple[str, Quote]]) -> None:
    if not pairs:
        return

    # One api call for the whole strip: the rate limit is shared across routes.
    with open(BATCH_FILE, "w") as f:
        f.write(rysk_sdk.premium_quote_batch(pairs))
    try:
        response = run_once(rysk_sdk.premium_quote_batch_args(BATCH_FILE, PREMIUM_URL))
        print(f"posted {len(pairs)} quotes: {response}")
    except RuntimeError as e:
        # Per quote feedback, not a batch rejection - the quotes not listed in
        # failures are on the book.
        print(f"some quotes were rejected: {e}")
    finally:
        os.unlink(BATCH_FILE)

    for request_id, quote in pairs:
        live[request_id] = {
            "nonce": quote.nonce,
            "validUntil": quote.validUntil,
            "chainId": quote.chainId,
            "id": None,
        }


def reconcile() -> None:
    """Ids only come back from the quotes listing, so posted quotes are matched to
    their ids by nonce - which is unique per quote here."""
    listed = json.loads(run_once(rysk_sdk.premium_quotes_args(MAKER, PREMIUM_URL)) or "[]")
    by_nonce = {q["nonce"]: q for q in listed}

    for request_id, tracked in list(live.items()):
        on_book = by_nonce.get(tracked["nonce"])
        if on_book is None:
            # Gone: filled, cancelled, or expired. The api cannot tell you which -
            # confirm fills on chain.
            print(f"quote on {request_id} left the book")
            del live[request_id]
            continue
        tracked["id"] = on_book["id"]


def cancel(request_id: str, tracked: dict) -> None:
    if not tracked["id"]:
        return  # never seen on the book, nothing to pull
    run_once(
        rysk_sdk.premium_cancel_args(tracked["id"], tracked["chainId"], next_nonce(), PREMIUM_URL)
    )
    live.pop(request_id, None)
    print(f"cancelled {tracked['id']}")


def tick() -> None:
    payload = json.loads(run_once(rysk_sdk.premium_requests_args(MAKER, PREMIUM_URL)) or "[]")
    quotable = [Request.from_json(json.dumps(r)) for r in payload if is_request(r)]
    now = int(time.time())

    pairs: List[Tuple[str, Quote]] = []
    for request in quotable:
        tracked = live.get(request.id)

        if tracked is None:
            pairs.append((request.id, build_quote(request)))
            continue
        # A price posted 9 minutes ago is about to become unexecutable: replace it
        # rather than letting it go stale.
        if tracked["validUntil"] - now < REFRESH_BEFORE_SECONDS:
            cancel(request.id, tracked)
            pairs.append((request.id, build_quote(request)))

    # A request that disappeared from the listing expired or was cancelled by its
    # taker; one fill also retires every other quote under it.
    quotable_ids = {request.id for request in quotable}
    for request_id, tracked in list(live.items()):
        if request_id not in quotable_ids:
            del live[request_id]
            print(f"request {request_id} is gone, dropping quote {tracked['id'] or '(unlisted)'}")

    post_quotes(pairs)
    reconcile()


def handle_sig(sig, frame):
    global running
    print("\npulling quotes before exit")
    running = False


def main() -> None:
    if not PK or not MAKER:
        print("set RYSK_SDK_PK and RYSK_MAKER", file=sys.stderr)
        sys.exit(1)

    signal.signal(signal.SIGINT, handle_sig)

    while running:
        try:
            tick()
        except Exception as e:
            print(f"tick failed: {e}")
        time.sleep(POLL_INTERVAL)

    for request_id, tracked in list(live.items()):
        try:
            cancel(request_id, tracked)
        except Exception as e:
            print(f"failed to cancel {tracked['id']}: {e}")


if __name__ == "__main__":
    main()
