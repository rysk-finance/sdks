//! The other sdks hand-write `is_request` / `is_transfer` predicates to check a
//! payload's shape. In rust that job is deserialisation, so these assert the
//! same acceptances and rejections against the wire shapes those predicates
//! were written for.

use ryskv12::{ChainId, JsonRpcResponse, QuoteNotification, Request, Transfer};

const MAKER: &str = "0x1000000000000000000000000000000000000001";
const HANDLER: &str = "0x2000000000000000000000000000000000000002";
const ASSET: &str = "0x98d56648c9b7f3cb49531f4135115b5000ab1733";

fn websocket_request() -> String {
    format!(
        r#"{{
          "asset": "{ASSET}",
          "assetName": "ETH",
          "chainId": 84532,
          "expiry": 1767225600,
          "isPut": false,
          "quantity": "1",
          "strike": "1",
          "taker": "{MAKER}",
          "usd": "{ASSET}",
          "collateralAsset": "{ASSET}"
        }}"#
    )
}

fn premium_request() -> String {
    format!(
        r#"{{
          "id": "b7c2",
          "asset": "{ASSET}",
          "chainId": 84532,
          "expiry": 1767225600,
          "isPut": false,
          "isTakerBuy": false,
          "quantity": "1",
          "strike": "1",
          "taker": "{MAKER}",
          "usd": "{ASSET}",
          "collateralAsset": "{ASSET}",
          "validUntil": 1787740878814,
          "makers": [],
          "premiumAsset": "{ASSET}",
          "isPremium": true,
          "isEIP1271": false,
          "createdAt": 1787740279,
          "typeDataDomain": {{
            "name": "PremiumOptionHandler",
            "version": "1",
            "chainId": "0x14a34",
            "verifyingContract": "{HANDLER}"
          }}
        }}"#
    )
}

#[test]
fn both_request_shapes_parse() {
    let premium: Request = serde_json::from_str(&premium_request()).unwrap();
    assert_eq!(premium.id.as_deref(), Some("b7c2"));
    assert_eq!(premium.makers, Some(vec![]));
    assert_eq!(premium.is_eip1271, Some(false));
    assert_eq!(
        premium
            .type_data_domain
            .as_ref()
            .unwrap()
            .verifying_contract
            .as_deref(),
        Some(HANDLER)
    );
    // absent on premium requests, and absent is not empty
    assert_eq!(premium.asset_name, None);

    // an older server's payload still parses, with the premium fields unset
    let older: Request = serde_json::from_str(&websocket_request()).unwrap();
    assert_eq!(older.id, None);
    assert!(older.type_data_domain.is_none());
    assert_eq!(older.asset_name.as_deref(), Some("ETH"));
}

#[test]
fn a_hex_chain_id_on_the_domain_survives_the_trip() {
    let req: Request = serde_json::from_str(&premium_request()).unwrap();
    let chain = req.type_data_domain.unwrap().chain_id.unwrap();
    assert_eq!(chain, ChainId::Text("0x14a34".into()));
    assert_eq!(chain.as_i64(), Some(84532), "0x14a34 is 84532");
    // and the numeric form parses too
    assert_eq!(ChainId::Number(84532).as_i64(), Some(84532));
    assert_eq!(ChainId::Text("84532".into()).as_i64(), Some(84532));
    assert_eq!(ChainId::Text("mainnet".into()).as_i64(), None);
}

#[test]
fn a_wrongly_typed_field_is_rejected_rather_than_coerced() {
    // the predicates in the other sdks exist to catch exactly these
    let quoted_chain = premium_request().replace(r#""chainId": 84532"#, r#""chainId": "84532""#);
    assert!(serde_json::from_str::<Request>(&quoted_chain).is_err());

    let text_bool = premium_request().replace(r#""isPremium": true"#, r#""isPremium": "yes""#);
    assert!(serde_json::from_str::<Request>(&text_bool).is_err());

    // a required field missing is not a Request either
    let no_taker = premium_request().replace(&format!(r#""taker": "{MAKER}","#), "");
    assert!(serde_json::from_str::<Request>(&no_taker).is_err());
}

#[test]
fn transfer_round_trips_as_the_camel_case_the_cli_marshals() {
    let transfer = Transfer {
        user: MAKER.into(),
        amount: "1".into(),
        asset: ASSET.into(),
        chain_id: 84532,
        is_deposit: true,
        nonce: "1".into(),
    };
    let json: serde_json::Value = serde_json::to_value(&transfer).unwrap();

    // the cli's struct tags say chainId and isDeposit, not chain_id
    assert_eq!(json["chainId"], 84532);
    assert_eq!(json["isDeposit"], true);
    assert!(json.get("chain_id").is_none());
    assert!(json.get("is_deposit").is_none());

    let back: Transfer = serde_json::from_value(json).unwrap();
    assert_eq!(back, transfer);
}

#[test]
fn a_successful_response_carries_a_result_and_no_error() {
    let res =
        JsonRpcResponse::from_json(r#"{"jsonrpc":"2.0","id":"7","result":{"ok":true}}"#).unwrap();
    assert!(res.error.is_none());
    assert_eq!(res.result.as_ref().unwrap()["ok"], true);
}

#[test]
fn a_failed_call_is_still_a_response() {
    // the cli sends error and omits result entirely
    let res = JsonRpcResponse::from_json(
        r#"{"jsonrpc":"2.0","id":"1","error":{"code":-32000,"message":"nope","data":{"why":"late"}}}"#,
    )
    .unwrap();

    assert!(res.result.is_none());
    let err = res.error.unwrap();
    assert_eq!(err.code, -32000);
    assert_eq!(err.message, "nope");
    assert_eq!(err.data.unwrap()["why"], "late");
}

#[test]
fn a_response_needs_its_envelope() {
    // no id is not a response, however good the result looks
    assert!(JsonRpcResponse::from_json(r#"{"jsonrpc":"2.0","result":{}}"#).is_err());
    // nor is a numeric id
    assert!(JsonRpcResponse::from_json(r#"{"jsonrpc":"2.0","id":1,"result":{}}"#).is_err());
}

#[test]
fn result_as_narrows_to_the_payload_or_gives_up() {
    let notification = format!(
        r#"{{"jsonrpc":"2.0","id":"1","result":{{
             "rfqId":"1","assetAddress":"{ASSET}","chainId":84532,
             "newBest":"1","yours":"1"}}}}"#
    );
    let res = JsonRpcResponse::from_json(&notification).unwrap();

    let parsed: QuoteNotification = res.result_as().expect("a notification");
    assert_eq!(parsed.rfq_id, "1");
    assert_eq!(parsed.new_best, "1");

    // the same payload is not a Request, and asking says so rather than panicking
    assert!(res.result_as::<Request>().is_none());

    // an error response has no result to narrow at all
    let failed = JsonRpcResponse::from_json(
        r#"{"jsonrpc":"2.0","id":"1","error":{"code":1,"message":"x"}}"#,
    )
    .unwrap();
    assert!(failed.result_as::<QuoteNotification>().is_none());
}
