from dataclasses import dataclass, fields, replace
import json
from typing import Any, Dict, List, Literal, Optional, Union


def _load(cls, data: Union[str, bytes, Dict[str, Any]]):
    """Builds a dataclass from a json string/bytes or an already parsed dict,
    taking each field by name; a field the server left out is None."""
    j = data if isinstance(data, dict) else json.loads(data)
    return cls(**{f.name: j.get(f.name) for f in fields(cls)})


@dataclass(frozen=True)
class TypedDataDomain:
    """EIP712 domain a quote has to be signed against. chainId is whatever the
    server sent - go marshals it as a hex string - and is always the chain of
    the request, so the CLI takes it from the quote itself."""

    name: Optional[str] = None
    version: Optional[str] = None
    chainId: Optional[Union[str, int]] = None
    verifyingContract: Optional[str] = None
    salt: Optional[str] = None


@dataclass(frozen=True)
class Request:
    asset: str
    chainId: int
    expiry: int
    isPut: bool
    quantity: str
    strike: str
    taker: str
    usd: str
    collateralAsset: str
    # server assigned id, on premium rfq requests - the websocket flow carries it
    # on the JSONRPC response instead
    id: Optional[str] = None
    assetName: Optional[str] = None  # absent on premium rfq requests
    createdAt: Optional[int] = None  # unix seconds, set by the server
    validUntil: Optional[int] = None  # unix milliseconds
    makers: Optional[List[str]] = None  # empty means open to every maker
    isTakerBuy: Optional[bool] = None
    auctionDeadline: Optional[int] = None  # unix milliseconds
    isPremium: Optional[bool] = None
    premiumAsset: Optional[str] = None
    isEIP1271: Optional[bool] = None
    # present when the request wants its quotes signed against a custom domain
    typeDataDomain: Optional[TypedDataDomain] = None

    @staticmethod
    def from_json(data: Union[str, bytes, Dict[str, Any]]) -> "Request":
        r = _load(Request, data)
        domain = r.typeDataDomain
        return replace(r, typeDataDomain=_load(TypedDataDomain, domain) if domain else None)


@dataclass(frozen=True)
class Quote:
    assetAddress: str
    chainId: int
    expiry: int
    isPut: bool
    isTakerBuy: bool
    maker: str
    nonce: str
    price: str
    quantity: str
    strike: str
    validUntil: int
    usd: str
    collateralAsset: str
    # asset the premium is paid in; sent with the quote but not signed
    premiumAsset: Optional[str] = None
    # domain to sign against, usually the request's typeDataDomain
    domain: Optional[TypedDataDomain] = None



@dataclass(frozen=True)
class Transfer:
    user: str
    amount: str
    asset: str
    chain_id: int
    is_deposit: bool
    nonce: str


@dataclass(frozen=True)
class ErrorData:
    """What the CLI reports instead of a result. Mirrors ErrorData in the CLI's
    jsonrpc.go; data is free form there, so it stays untyped here."""

    code: int
    message: str
    data: Optional[Any] = None


@dataclass(frozen=True)
class JSONRPCResponse:
    jsonrpc: Literal["2.0"]
    id: str
    # absent when the call failed - error carries the reason instead
    result: Union[Dict[str, Any], List[Any], str, None] = None
    # absent when the call succeeded
    error: Optional[ErrorData] = None

    @staticmethod
    def from_json(data: Union[str, bytes, Dict[str, Any]]) -> "JSONRPCResponse":
        r = _load(JSONRPCResponse, data)
        return replace(r, error=_load(ErrorData, r.error) if r.error else None)



def _is_optional(value: Any, expected: type) -> bool:
    """A field the server may not send at all is only checked when it is there."""
    return value is None or isinstance(value, expected)


def is_request(obj: Any) -> bool:
    """Type predicate for Request."""
    return (
        isinstance(obj, dict)
        and obj is not None
        and isinstance(obj.get("asset"), str)
        and isinstance(obj.get("chainId"), int)
        and isinstance(obj.get("expiry"), int)
        and isinstance(obj.get("isPut"), bool)
        and isinstance(obj.get("quantity"), str)
        and isinstance(obj.get("strike"), str)
        and isinstance(obj.get("taker"), str)
        and isinstance(obj.get("usd"), str)
        and isinstance(obj.get("collateralAsset"), str)
        and _is_optional(obj.get("id"), str)
        and _is_optional(obj.get("assetName"), str)
        and _is_optional(obj.get("createdAt"), int)
        and _is_optional(obj.get("validUntil"), int)
        and _is_optional(obj.get("makers"), list)
        and _is_optional(obj.get("isTakerBuy"), bool)
        and _is_optional(obj.get("auctionDeadline"), int)
        and _is_optional(obj.get("isPremium"), bool)
        and _is_optional(obj.get("premiumAsset"), str)
        and _is_optional(obj.get("isEIP1271"), bool)
        and _is_optional(obj.get("typeDataDomain"), dict)
    )


def is_quote(obj: Any) -> bool:
    """Type predicate for Quote."""
    return (
        isinstance(obj, dict)
        and obj is not None
        and isinstance(obj.get("assetAddress"), str)
        and isinstance(obj.get("chainId"), int)
        and isinstance(obj.get("expiry"), int)
        and isinstance(obj.get("isPut"), bool)
        and isinstance(obj.get("isTakerBuy"), bool)
        and isinstance(obj.get("maker"), str)
        and isinstance(obj.get("nonce"), str)
        and isinstance(obj.get("price"), str)
        and isinstance(obj.get("quantity"), str)
        and isinstance(obj.get("signature"), str)
        and isinstance(obj.get("strike"), str)
        and isinstance(obj.get("validUntil"), int)
        and isinstance(obj.get("usd"), str)
        and isinstance(obj.get("collateralAsset"), str)
        and _is_optional(obj.get("premiumAsset"), str)
        and _is_optional(obj.get("domain"), dict)
    )


def is_transfer(obj: Any) -> bool:
    """Type predicate for Transfer.

    This checks the shape the CLI puts on the socket, not the Transfer
    dataclass above: the dataclass mirrors the CLI's snake_case *flags*
    (--chain_id, --is_deposit), while the JSON the CLI marshals is camelCase.
    See the struct tags on Transfer in the CLI's types.go.
    """
    return (
        isinstance(obj, dict)
        and obj is not None
        and isinstance(obj.get("user"), str)
        and isinstance(obj.get("amount"), str)
        and isinstance(obj.get("asset"), str)
        and isinstance(obj.get("chainId"), int)
        and isinstance(obj.get("isDeposit"), bool)
        and isinstance(obj.get("nonce"), str)
    )


def is_error_data(obj: Any) -> bool:
    """Type predicate for ErrorData."""
    return (
        isinstance(obj, dict)
        and obj is not None
        and isinstance(obj.get("code"), int)
        and isinstance(obj.get("message"), str)
    )


def is_json_rpc_response(obj: Any) -> bool:
    """Type predicate for JSONRPCResponse.

    A failed call carries error and no result at all, so accepting only a well
    formed result would drop every error the CLI reports.
    """
    return (
        isinstance(obj, dict)
        and obj is not None
        and isinstance(obj.get("jsonrpc"), str)
        and isinstance(obj.get("id"), str)
        and (
            is_error_data(obj.get("error"))
            or isinstance(obj.get("result"), dict)
            or isinstance(obj.get("result"), list)
            or isinstance(obj.get("result"), str)
        )
    )


def is_quote_notification(obj: Any) -> bool:
    """Type predicate for QuoteNotification."""
    return (
        isinstance(obj, dict)
        and obj is not None
        and isinstance(obj.get("rfqId"), str)
        and isinstance(obj.get("assetAddress"), str)
        and isinstance(obj.get("chainId"), int)
        and isinstance(obj.get("newBest"), str)
        and isinstance(obj.get("yours"), str)
    )
