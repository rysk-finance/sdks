use serde::{Deserialize, Serialize};

/// A chain id as the server sends it. Go marshals it as a hex string, so both
/// forms have to round trip.
#[derive(Serialize, Deserialize, Clone, Debug, PartialEq, Eq)]
#[serde(untagged)]
pub enum ChainId {
    Number(i64),
    Text(String),
}

impl ChainId {
    /// The value as a number, parsing `0x`-prefixed text the way the other sdks
    /// do. `None` when the text is not a number at all.
    pub fn as_i64(&self) -> Option<i64> {
        match self {
            ChainId::Number(n) => Some(*n),
            ChainId::Text(s) => {
                let s = s.trim();
                match s.strip_prefix("0x").or_else(|| s.strip_prefix("0X")) {
                    Some(hex) => i64::from_str_radix(hex, 16).ok(),
                    None => s.parse().ok(),
                }
            }
        }
    }
}

/// EIP712 domain a quote has to be signed against. `chain_id` is always the
/// chain of the request, so the cli takes it from the quote itself.
#[derive(Serialize, Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct TypedDataDomain {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub version: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub chain_id: Option<ChainId>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub verifying_contract: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub salt: Option<String>,
}

/// An rfq to quote on.
#[derive(Serialize, Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Request {
    /// server assigned, on premium rfq requests - the websocket flow carries it
    /// on the JSONRPC response instead
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub id: Option<String>,
    pub asset: String,
    /// absent on premium rfq requests
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub asset_name: Option<String>,
    pub chain_id: i64,
    pub expiry: i64,
    pub is_put: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub is_taker_buy: Option<bool>,
    pub quantity: String,
    pub strike: String,
    pub taker: String,
    pub usd: String,
    pub collateral_asset: String,
    /// unix milliseconds, on premium rfq requests
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub valid_until: Option<i64>,
    /// empty means the request is open to every maker
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub makers: Option<Vec<String>>,
    /// unix seconds, set by the server
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub created_at: Option<i64>,
    /// unix milliseconds
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub auction_deadline: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub is_premium: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub premium_asset: Option<String>,
    // rename_all would give isEip1271
    #[serde(rename = "isEIP1271", default, skip_serializing_if = "Option::is_none")]
    pub is_eip1271: Option<bool>,
    /// present when the request wants its quotes signed against a custom domain
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub type_data_domain: Option<TypedDataDomain>,
}

/// A price on a request. The cli signs it; nothing here carries a signature.
#[derive(Serialize, Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Quote {
    pub asset_address: String,
    pub chain_id: i64,
    pub expiry: i64,
    pub is_put: bool,
    pub is_taker_buy: bool,
    pub maker: String,
    pub nonce: String,
    pub price: String,
    pub quantity: String,
    pub strike: String,
    pub valid_until: i64,
    pub usd: String,
    pub collateral_asset: String,
    /// asset the premium is paid in; sent with the quote but not signed
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub premium_asset: Option<String>,
    /// domain to sign against, usually the request's `type_data_domain`
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub domain: Option<TypedDataDomain>,
}

/// Where your price stands, so you can re-quote rather than wait.
#[derive(Serialize, Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct QuoteNotification {
    pub rfq_id: String,
    pub asset_address: String,
    pub chain_id: i64,
    pub new_best: String,
    pub yours: String,
}

/// A deposit or withdrawal.
///
/// The field names here are rust's; serde carries them to the camelCase the cli
/// puts on the socket, and the arg builder spells the cli's snake_case flags.
/// The other two sdks make you keep those three spellings straight by hand.
#[derive(Serialize, Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Transfer {
    pub user: String,
    pub amount: String,
    pub asset: String,
    pub chain_id: i64,
    pub is_deposit: bool,
    pub nonce: String,
}

/// What the cli reports instead of a result.
#[derive(Serialize, Deserialize, Clone, Debug, PartialEq)]
pub struct ErrorData {
    pub code: i64,
    pub message: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub data: Option<serde_json::Value>,
}

/// One line of the cli's stdout, once parsed.
#[derive(Serialize, Deserialize, Clone, Debug, PartialEq)]
pub struct JsonRpcResponse {
    pub jsonrpc: String,
    pub id: String,
    /// absent when the call failed - `error` carries the reason instead
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub result: Option<serde_json::Value>,
    /// absent when the call succeeded
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<ErrorData>,
}

impl JsonRpcResponse {
    /// Parses one line of cli output.
    pub fn from_json(data: &str) -> Result<Self, serde_json::Error> {
        serde_json::from_str(data)
    }

    /// The result as a `T`, or `None` when the call failed or carried something
    /// else. This is what the other sdks' `is_request` / `is_quote_notification`
    /// predicates are for.
    pub fn result_as<T: serde::de::DeserializeOwned>(&self) -> Option<T> {
        let raw = self.result.as_ref()?;
        serde_json::from_value(raw.clone()).ok()
    }
}
