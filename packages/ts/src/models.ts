export type HexString = `0x${string}`;

export type Request = {
  asset: HexString;
  assetName: string;
  chainId: number;
  expiry: number;
  isPut: boolean;
  quantity: string;
  strike: string;
  taker: HexString;
  usd: HexString;
  collateralAsset: HexString;
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
    typeof obj.collateralAsset === "string"
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
    typeof obj.collateralAsset === "string"
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
