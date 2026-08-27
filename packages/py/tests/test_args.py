import json

import pytest

from ryskV12.client import Env, Rysk, _parse_version
from ryskV12.models import (
    Quote,
    Request,
    Transfer,
    TypedDataDomain,
    is_json_rpc_response,
    is_quote,
    is_quote_notification,
    is_request,
    is_transfer,
)
from ryskV12.nonce import NonceCounter

MAKER = "0x1000000000000000000000000000000000000001"
HANDLER = "0x2000000000000000000000000000000000000002"
ASSET = "0x98d56648c9b7f3cb49531f4135115b5000ab1733"


@pytest.fixture
def sdk():
    """A SDK whose constructor does not shell out to a CLI that may not exist."""
    instance = Rysk.__new__(Rysk)
    instance._env = Env.TESTNET
    instance._cli_path = "./ryskV12"
    instance._private_key = "test-key"
    instance._strict_version = False
    return instance


def quote(**overrides) -> Quote:
    fields = dict(
        assetAddress=ASSET,
        chainId=84532,
        expiry=1767225600,
        isPut=True,
        isTakerBuy=False,
        maker=MAKER,
        nonce="42",
        price="1250000000000000000",
        quantity="1000000000000000000",
        strike="300000000000",
        validUntil=1767139500,
        usd=ASSET,
        collateralAsset=ASSET,
    )
    fields.update(overrides)
    return Quote(**fields)


def flag_value(args, flag):
    return args[args.index(flag) + 1]


def test_approve_leaves_the_asset_to_the_cli_unless_one_is_given(sdk):
    fallback = sdk.approve_args(84532, "1", "https://rpc")
    assert "--asset" not in fallback

    explicit = sdk.approve_args(84532, "1", "https://rpc", ASSET)
    assert flag_value(explicit, "--asset") == ASSET


def test_approve_can_be_told_to_approve_any_erc20(sdk):
    other = "0x1111111111111111111111111111111111111111"
    args = sdk.approve_args(84532, "1", "https://rpc", other)
    assert flag_value(args, "--asset") == other
    assert flag_value(args, "--amount") == "1"


def test_no_arg_builder_carries_the_private_key(sdk):
    transfer = Transfer(
        user=MAKER, amount="1", asset=ASSET, chain_id=84532, is_deposit=True, nonce="1"
    )
    premium_quote = quote(domain=TypedDataDomain(verifyingContract=HANDLER))

    builders = [
        sdk.connect_args("chan", "maker"),
        sdk.disconnect_args("chan"),
        sdk.approve_args(84532, "1", "https://rpc"),
        sdk.balances_args("chan", MAKER),
        sdk.positions_args("chan", MAKER),
        sdk.transfer_args("chan", transfer),
        sdk.quote_args("chan", "rfq", quote()),
        sdk.premium_requests_args(MAKER),
        sdk.premium_quotes_args(MAKER),
        sdk.premium_quote_status_args("id"),
        sdk.premium_quote_args("rfq", premium_quote),
        sdk.premium_quote_batch_args("batch.json"),
        sdk.premium_cancel_args("id", 84532, "43"),
    ]

    for args in builders:
        assert "--private_key" not in args, args[:2]
        assert "test-key" not in args, args[:2]


def test_connect_and_disconnect(sdk):
    assert sdk.connect_args("chan", "maker") == [
        "connect",
        "--channel_id",
        "chan",
        "--url",
        "wss://rip-testnet.rysk.finance/maker",
    ]
    assert sdk.disconnect_args("chan") == ["disconnect", "--channel_id", "chan"]


def test_quote_passes_terms_and_flags(sdk):
    args = sdk.quote_args("chan", "rfq-1", quote(isTakerBuy=True))

    assert flag_value(args, "--rfq_id") == "rfq-1"
    assert flag_value(args, "--asset") == ASSET
    assert flag_value(args, "--collateral") == ASSET
    assert flag_value(args, "--valid_until") == "1767139500"
    assert "--is_put" in args
    assert "--is_taker_buy" in args

    call = sdk.quote_args("chan", "rfq-1", quote(isPut=False, isTakerBuy=False))
    assert "--is_put" not in call
    assert "--is_taker_buy" not in call


def test_quote_sends_premium_asset_only_when_set(sdk):
    with_asset = sdk.quote_args("chan", "rfq", quote(premiumAsset=HANDLER))
    assert flag_value(with_asset, "--premium_asset") == HANDLER
    assert "--premium_asset" not in sdk.quote_args("chan", "rfq", quote())


def test_premium_quote_carries_domain_and_request_id(sdk):
    domain = TypedDataDomain(
        name="PremiumOptionHandler", version="1", verifyingContract=HANDLER
    )
    args = sdk.premium_quote_args("req-1", quote(domain=domain))

    assert args[:2] == ["premium", "quote"]
    assert flag_value(args, "--request_id") == "req-1"
    assert flag_value(args, "--domain_name") == "PremiumOptionHandler"
    assert flag_value(args, "--domain_version") == "1"
    assert flag_value(args, "--domain_verifying_contract") == HANDLER


def test_premium_url_only_passed_when_given(sdk):
    assert "--url" not in sdk.premium_requests_args(MAKER)
    args = sdk.premium_requests_args(MAKER, "http://localhost:8080")
    assert flag_value(args, "--url") == "http://localhost:8080"


@pytest.mark.parametrize(
    "domain, message",
    [
        (None, "missing verifyingContract"),
        (
            TypedDataDomain(verifyingContract=HANDLER, salt="0x01"),
            "salt is not supported",
        ),
        (
            TypedDataDomain(verifyingContract=HANDLER, chainId=8453),
            "does not match the quote's chain 84532",
        ),
    ],
)
def test_premium_rejects_unsignable_domains(sdk, domain, message):
    with pytest.raises(ValueError, match=message):
        sdk.premium_quote_args("req", quote(domain=domain))


def test_premium_accepts_a_matching_hex_chain_id(sdk):
    domain = TypedDataDomain(verifyingContract=HANDLER, chainId="0x14a34")
    assert sdk.premium_quote_args("req", quote(domain=domain))


def test_websocket_quote_needs_a_whole_domain(sdk):
    with pytest.raises(ValueError, match="missing version, verifyingContract"):
        sdk.quote_args("chan", "rfq", quote(domain=TypedDataDomain(name="rysk")))


def test_premium_defaults_the_domain_name(sdk):
    args = sdk.premium_quote_args("req", quote(domain=TypedDataDomain(verifyingContract=HANDLER)))
    assert "--domain_name" not in args
    assert flag_value(args, "--domain_verifying_contract") == HANDLER


def test_premium_quote_batch_serialises_entries(sdk):
    domain = TypedDataDomain(verifyingContract=HANDLER)
    batch = json.loads(
        sdk.premium_quote_batch(
            [("req-0", quote(domain=domain)), ("req-1", quote(nonce="43", domain=domain))]
        )
    )

    assert len(batch) == 2
    assert batch[0]["requestId"] == "req-0"
    assert batch[1]["nonce"] == "43"
    assert batch[0]["domain"]["verifyingContract"] == HANDLER
    assert batch[0]["assetAddress"] == ASSET


def test_premium_quote_batch_refuses_unsignable_entries(sdk):
    with pytest.raises(ValueError, match="missing verifyingContract"):
        sdk.premium_quote_batch([("req", quote())])


def test_nonce_counter_never_repeats_or_rewinds(tmp_path):
    path = str(tmp_path / "counter")

    first = NonceCounter(path)
    issued = [first.next(), first.next(), first.next()]
    assert len(set(issued)) == len(issued), "handed out a nonce twice"
    assert all(nonce.isdigit() for nonce in issued)
    assert issued == sorted(issued, key=int), "nonces have to climb"

    # a restart continues from the file, not from zero
    assert int(NonceCounter(path).next()) > int(issued[-1])

    # a stored counter ahead of the clock still wins
    with open(path, "w") as f:
        f.write("99999999999999")
    assert NonceCounter(path).next() == "100000000000000"

    # and a corrupt file falls back to the clock rather than throwing
    with open(path, "w") as f:
        f.write("not a number")
    assert int(NonceCounter(path).next()) > 0


@pytest.mark.parametrize(
    "raw, expected",
    [
        ("3.2.0", (3, 2, 0)),
        ("v3.2.1", (3, 2, 1)),
        ("3.10.0", (3, 10, 0)),
        ("10.0.0", (10, 0, 0)),
        ("3.2.0-rc1", (3, 2, 0)),
        ("dev", None),
        ("", None),
    ],
)
def test_version_parsing(raw, expected):
    assert _parse_version(raw) == expected


def test_version_comparison_uses_every_component():
    # the old check compared the first character, so 10.x read as older than 3.x
    assert _parse_version("10.0.0") > _parse_version("3.2.0")
    assert _parse_version("3.10.0") > _parse_version("3.9.9")
    assert _parse_version("3.1.9") < _parse_version("3.2.0")


def test_predicates_accept_websocket_and_premium_requests():
    websocket = {
        "asset": ASSET,
        "assetName": "ETH",
        "chainId": 84532,
        "expiry": 1767225600,
        "isPut": False,
        "quantity": "1",
        "strike": "1",
        "taker": MAKER,
        "usd": ASSET,
        "collateralAsset": ASSET,
    }
    premium = {
        "id": "b7c2",
        "asset": ASSET,
        "chainId": 84532,
        "expiry": 1767225600,
        "isPut": False,
        "isTakerBuy": False,
        "quantity": "1",
        "strike": "1",
        "taker": MAKER,
        "usd": ASSET,
        "collateralAsset": ASSET,
        "validUntil": 1787740878814,
        "makers": [],
        "premiumAsset": ASSET,
        "isPremium": True,
        "createdAt": 1787740279,
        "typeDataDomain": {
            "name": "PremiumOptionHandler",
            "version": "1",
            "chainId": "0x14a34",
            "verifyingContract": HANDLER,
        },
    }

    assert is_request(websocket)
    assert is_request(premium)
    assert not is_request({**premium, "chainId": "84532"})
    assert not is_request({**premium, "isPremium": "yes"})
    assert not is_request(None)

    parsed = Request.from_json(json.dumps(premium))
    assert parsed.id == "b7c2"
    assert parsed.makers == []
    assert parsed.typeDataDomain.verifyingContract == HANDLER
    assert parsed.assetName is None

    # an older server's payload still parses, with the premium fields unset
    older = Request.from_json(json.dumps(websocket))
    assert older.id is None
    assert older.typeDataDomain is None


def test_remaining_predicates():
    signed = {**json.loads(json.dumps(quote().__dict__)), "signature": "0x00"}
    assert is_quote(signed)
    assert not is_quote({k: v for k, v in signed.items() if k != "signature"})
    assert is_transfer(
        {
            "user": MAKER,
            "amount": "1",
            "asset": ASSET,
            "chain_id": 1,
            "isDeposit": True,
            "nonce": "1",
        }
    )
    assert is_json_rpc_response({"jsonrpc": "2.0", "id": "1", "result": {}})
    assert not is_json_rpc_response({"jsonrpc": "2.0", "id": 1, "result": {}})
    assert is_quote_notification(
        {"rfqId": "1", "assetAddress": ASSET, "chainId": 84532, "newBest": "1", "yours": "1"}
    )
