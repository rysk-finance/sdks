export type HexString = `0x${string}`;

/**
 * EIP712 domain a quote has to be signed against. `chainId` is whatever the
 * server sent - go marshals it as a hex string - and is always the chain of the
 * request, so the CLI takes it from the quote itself.
 */
export type TypedDataDomain = {
  name?: string;
  version?: string;
  chainId?: string | number;
  verifyingContract?: HexString;
  salt?: string;
};

export type Request = {
  asset: HexString;
  assetName: string;
  chainId: number;
  expiry: number;
  isPut: boolean;
  isTakerBuy?: boolean;
  quantity: string;
  strike: string;
  taker: HexString;
  usd: HexString;
  collateralAsset: HexString;
  /** unix milliseconds */
  auctionDeadline?: number;
  isPremium?: boolean;
  premiumAsset?: HexString;
  isEIP1271?: boolean;
  /** present when the request wants its quotes signed against a custom domain */
  typeDataDomain?: TypedDataDomain;
};

export type Quote = {
  assetAddress: HexString;
  chainId: number;
  expiry: number;
  isPut: boolean;
  isTakerBuy: boolean;
  maker: HexString;
  nonce: string;
  price: string;
  quantity: string;
  strike: string;
  validUntil: number;
  usd: HexString;
  collateralAsset: HexString;
  /** asset the premium is paid in; sent with the quote but not signed */
  premiumAsset?: HexString;
  /** domain to sign against, usually the request's `typeDataDomain` */
  domain?: TypedDataDomain;
};

export type QuoteNotification = {
  rfqId: string;
  assetAddress: string;
  chainId: number;
  newBest: string;
  yours: string;
};

export type Transfer = {
  user: HexString;
  amount: string;
  asset: HexString;
  chain_id: number;
  is_deposit: boolean;
  nonce: string;
};

export type JSONRPCResponse = {
  jsonrpc: string;
  id: string;
  method: string;
  result: Record<string, any> | Array<any> | string;
};

export type JSONResponseHandler = (res: JSONRPCResponse) => void;

// A field the server may not send at all is only checked when it is there.
function isOptional(value: any, type: "string" | "number" | "boolean" | "object") {
  return value === undefined || value === null || typeof value === type;
}

// Type predicate for Request
export function isRequest(obj: any): obj is Request {
  return (
    typeof obj === "object" &&
    obj !== null &&
    typeof obj.asset === "string" &&
    typeof obj.assetName === "string" &&
    typeof obj.chainId === "number" &&
    typeof obj.expiry === "number" &&
    typeof obj.isPut === "boolean" &&
    typeof obj.quantity === "string" &&
    typeof obj.strike === "string" &&
    typeof obj.taker === "string" &&
    typeof obj.usd === "string" &&
    typeof obj.collateralAsset === "string" &&
    isOptional(obj.isTakerBuy, "boolean") &&
    isOptional(obj.auctionDeadline, "number") &&
    isOptional(obj.isPremium, "boolean") &&
    isOptional(obj.premiumAsset, "string") &&
    isOptional(obj.isEIP1271, "boolean") &&
    isOptional(obj.typeDataDomain, "object")
  );
}

// Type predicate for Quote
export function isQuote(obj: any): obj is Quote {
  return (
    typeof obj === "object" &&
    obj !== null &&
    typeof obj.assetAddress === "string" &&
    typeof obj.chainId === "number" &&
    typeof obj.expiry === "number" &&
    typeof obj.isPut === "boolean" &&
    typeof obj.isTakerBuy === "boolean" &&
    typeof obj.maker === "string" &&
    typeof obj.nonce === "string" &&
    typeof obj.price === "string" &&
    typeof obj.quantity === "string" &&
    typeof obj.signature === "string" &&
    typeof obj.strike === "string" &&
    typeof obj.validUntil === "number" &&
    typeof obj.usd === "string" &&
    typeof obj.collateralAsset === "string" &&
    isOptional(obj.premiumAsset, "string") &&
    isOptional(obj.domain, "object")
  );
}

// Type predicate for Transfer
export function isTransfer(obj: any): obj is Transfer {
  return (
    typeof obj === "object" &&
    obj !== null &&
    typeof obj.user === "string" &&
    typeof obj.amount === "string" &&
    typeof obj.asset === "string" &&
    typeof obj.chain_id === "number" &&
    typeof obj.isDeposit === "boolean" &&
    typeof obj.nonce === "string"
  );
}

// Type predicate for JSONRPCResponse
export function isJSONRPCResponse(obj: any): obj is JSONRPCResponse {
  return (
    typeof obj === "object" &&
    obj !== null &&
    typeof obj.jsonrpc === "string" &&
    typeof obj.id === "string" &&
    (typeof obj.result === "object" ||
      Array.isArray(obj.result) ||
      typeof obj.result === "string")
  );
}

// Type predicate for QuoteNotification
export function isQuoteNotification(obj: any): obj is QuoteNotification {
  return (
    typeof obj === "object" &&
    obj !== null &&
    typeof obj.rfqId === "string" &&
    typeof obj.assetAddress === "string" &&
    typeof obj.chainId === "number" &&
    typeof obj.newBest === "string" &&
    typeof obj.yours === "string"
  );
}
