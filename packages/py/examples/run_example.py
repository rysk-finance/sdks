"""Market maker loop for the websocket flow: hold a maker connection open, listen
for RFQs on an asset, price them, and send signed quotes back through the maker
channel.

    RYSK_SDK_PK=<hex private key> \
    RYSK_MAKER=0x<maker address> \
    RYSK_ASSET=0x<asset address> \
    python tests/run_example.py
"""

import asyncio
import json
import os
import signal
import sys
import time

from ryskV12.client import Env, Rysk
from ryskV12.nonce import NonceCounter
from ryskV12.models import (
    Quote,
    Request,
    is_error_data,
    is_json_rpc_response,
    is_quote_notification,
    is_request,
)

PK = os.environ.get("RYSK_SDK_PK", "")
MAKER = os.environ.get("RYSK_MAKER", "")
ASSET = os.environ.get("RYSK_ASSET", "0xb67bfa7b488df4f2efa874f4e59242e9130ae61f")

MAKER_CHANNEL = "maker__py"
RFQ_CHANNEL = f"{ASSET}__py"
QUOTE_VALIDITY_SECONDS = 30
# one counter per signing key, shared by quotes and cancels
nonces = NonceCounter(".rysk-nonce")

rysk_sdk = Rysk(env=Env.TESTNET, private_key=PK, v12_cli_path="./ryskV12cli")


def price_request(request: Request) -> str:
    """Replace with your own pricing. Quantity and strike come from the request."""
    mid = 4 * 10**18
    edge = 110 if request.isTakerBuy else 90  # sell above, buy below
    return str(mid * edge // 100)


def build_quote(request: Request) -> Quote:
    """The terms have to be the request's - they are what the taker asked for and
    what the signature commits to. Ours are the maker's own fields."""
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
        nonce=nonces.next(),
        price=price_request(request),
        validUntil=int(time.time()) + QUOTE_VALIDITY_SECONDS,
        # sent with the quote but not signed; present when the request names one
        premiumAsset=request.premiumAsset,
        # sign against the domain the request asks for, if it asks for one
        domain=request.typeDataDomain,
    )


def quote(request_id: str, request: Request) -> None:
    proc = rysk_sdk.execute(rysk_sdk.quote_args(MAKER_CHANNEL, request_id, build_quote(request)))
    stdout, stderr = proc.communicate()
    if stdout.strip():
        print("quote:", stdout.strip())
    if stderr.strip():
        print("quote error:", stderr.strip())


def handle_maker_message(payload: bytes) -> None:
    text = payload.decode().strip()
    if not text:
        return
    try:
        message = json.loads(text)
    except json.JSONDecodeError:
        print("maker:", text)
        return

    # A quote notification tells you where your price stands, so it is the cue to
    # re-quote rather than wait.
    # An error arrives instead of a result, so it has to be read before anything
    # reaches for one.
    if is_json_rpc_response(message) and is_error_data(message.get("error")):
        print(f"maker rpc {message['id']} failed: {message['error']['message']}")
        return
    if is_json_rpc_response(message) and is_quote_notification(message.get("result")):
        result = message["result"]
        print(f"rfq {result['rfqId']}: best {result['newBest']}, yours {result['yours']}")
        return
    print("maker:", text)


def handle_rfq_message(payload: bytes) -> None:
    text = payload.decode().strip()
    if not text:
        return
    try:
        message = json.loads(text)
        if is_json_rpc_response(message) and is_error_data(message.get("error")):
            print(f"rfq rpc {message['id']} failed: {message['error']['message']}")
            return
        if not is_json_rpc_response(message) or not is_request(message.get("result")):
            return
        request = Request.from_json(json.dumps(message["result"]))
        print(f"rfq {message['id']}: {request.quantity} @ {request.strike}")
        quote(message["id"], request)
    except Exception as e:
        print("failed to handle rfq:", e)


async def process_rfqs() -> None:
    try:
        # The maker connection is what quotes and transfers are written into; it
        # has to stay open for the whole session.
        asyncio.create_task(
            rysk_sdk.execute_async(
                rysk_sdk.connect_args(MAKER_CHANNEL, "maker"), handle_maker_message
            )
        )
        await asyncio.sleep(1)  # let the maker socket come up before quoting

        # Account reads go through the maker channel like everything else.
        rysk_sdk.execute(rysk_sdk.balances_args(MAKER_CHANNEL, MAKER))
        rysk_sdk.execute(rysk_sdk.positions_args(MAKER_CHANNEL, MAKER))

        print(f"listening for rfqs on {ASSET}")
        await rysk_sdk.execute_async(
            rysk_sdk.connect_args(RFQ_CHANNEL, f"rfqs/{ASSET}"), handle_rfq_message
        )
    except Exception as e:
        print(e)
    finally:
        rysk_sdk.execute(rysk_sdk.disconnect_args(RFQ_CHANNEL))
        rysk_sdk.execute(rysk_sdk.disconnect_args(MAKER_CHANNEL))


def handle_sig(sig, frame):
    print("\ndisconnecting")
    rysk_sdk.execute(rysk_sdk.disconnect_args(RFQ_CHANNEL))
    rysk_sdk.execute(rysk_sdk.disconnect_args(MAKER_CHANNEL))
    for channel in (RFQ_CHANNEL, MAKER_CHANNEL):
        try:
            os.remove(f"/tmp/{channel}.sock")
        except FileNotFoundError:
            pass
    sys.exit(0)


if __name__ == "__main__":
    if not PK or not MAKER:
        print("set RYSK_SDK_PK and RYSK_MAKER", file=sys.stderr)
        sys.exit(1)
    signal.signal(signal.SIGINT, handle_sig)
    asyncio.run(process_rfqs())
