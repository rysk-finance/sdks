//! Mirrors the typescript and python arg-builder suites: the three sdks have to
//! spell the cli's flags the same way, so these assert the same things.

use ryskv12::{Env, NonceCounter, Quote, Rysk, Transfer, TypedDataDomain};

const MAKER: &str = "0x1000000000000000000000000000000000000001";
const HANDLER: &str = "0x2000000000000000000000000000000000000002";
const ASSET: &str = "0x98d56648c9b7f3cb49531f4135115b5000ab1733";
const KEY: &str = "test-key";

/// A client whose cli path does not exist: the version check reports to stderr
/// and carries on, which is exactly what a caller without a binary yet gets.
fn sdk() -> Rysk {
    Rysk::builder(Env::Testnet, KEY)
        .cli_path("./does-not-exist")
        .build()
        .expect("a missing cli warns, it does not fail")
}

fn quote() -> Quote {
    Quote {
        asset_address: ASSET.into(),
        chain_id: 84532,
        expiry: 1767225600,
        is_put: true,
        is_taker_buy: false,
        maker: MAKER.into(),
        nonce: "42".into(),
        price: "1250000000000000000".into(),
        quantity: "1000000000000000000".into(),
        strike: "300000000000".into(),
        valid_until: 1767139500,
        usd: ASSET.into(),
        collateral_asset: ASSET.into(),
        premium_asset: None,
        domain: None,
    }
}

fn premium_domain() -> TypedDataDomain {
    TypedDataDomain {
        verifying_contract: Some(HANDLER.into()),
        ..Default::default()
    }
}

fn flag_value<'a>(args: &'a [String], flag: &str) -> &'a str {
    let i = args
        .iter()
        .position(|a| a == flag)
        .unwrap_or_else(|| panic!("{flag} not in {args:?}"));
    &args[i + 1]
}

fn has(args: &[String], flag: &str) -> bool {
    args.iter().any(|a| a == flag)
}

// --------------------------------------------------------------------- basics

#[test]
fn connect_and_disconnect() {
    let sdk = sdk();
    assert_eq!(
        sdk.connect_args("chan", "maker"),
        vec![
            "connect",
            "--channel_id",
            "chan",
            "--url",
            "wss://rip-testnet.rysk.finance/maker"
        ]
    );
    assert_eq!(
        sdk.disconnect_args("chan"),
        vec!["disconnect", "--channel_id", "chan"]
    );
}

#[test]
fn every_env_has_its_own_url() {
    assert_eq!(Env::Local.base_url(), "ws://localhost:8000/");
    assert_eq!(Env::Testnet.base_url(), "wss://rip-testnet.rysk.finance/");
    assert_eq!(Env::Mainnet.base_url(), "wss://v12.rysk.finance/");
}

#[test]
fn approve_leaves_the_asset_to_the_cli_unless_one_is_given() {
    let sdk = sdk();

    let fallback = sdk.approve_args(84532, "1", "https://rpc", None);
    assert!(
        !has(&fallback, "--asset"),
        "an absent asset must not reach the cli"
    );

    let explicit = sdk.approve_args(84532, "1", "https://rpc", Some(ASSET));
    assert_eq!(flag_value(&explicit, "--asset"), ASSET);
    assert_eq!(flag_value(&explicit, "--amount"), "1");
}

#[test]
fn approve_treats_an_empty_asset_as_absent() {
    // an empty string is not an address; passing it through would make the cli
    // parse "" as one rather than fall back
    assert!(!has(
        &sdk().approve_args(84532, "1", "https://rpc", Some("")),
        "--asset"
    ));
}

#[test]
fn transfer_flags_a_deposit_but_not_a_withdrawal() {
    let sdk = sdk();
    let base = Transfer {
        user: MAKER.into(),
        amount: "1".into(),
        asset: ASSET.into(),
        chain_id: 84532,
        is_deposit: true,
        nonce: "1".into(),
    };

    let deposit = sdk.transfer_args("chan", &base);
    assert!(has(&deposit, "--is_deposit"));
    assert_eq!(flag_value(&deposit, "--chain_id"), "84532");
    assert_eq!(flag_value(&deposit, "--amount"), "1");

    let withdrawal = Transfer {
        is_deposit: false,
        ..base
    };
    assert!(!has(
        &sdk.transfer_args("chan", &withdrawal),
        "--is_deposit"
    ));
}

// ---------------------------------------------------------------------- quote

#[test]
fn quote_passes_terms_and_flags() {
    let sdk = sdk();
    let args = sdk
        .quote_args(
            "chan",
            "rfq-1",
            &Quote {
                is_taker_buy: true,
                ..quote()
            },
        )
        .unwrap();

    assert_eq!(flag_value(&args, "--rfq_id"), "rfq-1");
    assert_eq!(flag_value(&args, "--asset"), ASSET);
    assert_eq!(flag_value(&args, "--collateral"), ASSET);
    assert_eq!(flag_value(&args, "--valid_until"), "1767139500");
    assert!(has(&args, "--is_put"));
    assert!(has(&args, "--is_taker_buy"));

    let neither = sdk
        .quote_args(
            "chan",
            "rfq-1",
            &Quote {
                is_put: false,
                is_taker_buy: false,
                ..quote()
            },
        )
        .unwrap();
    assert!(!has(&neither, "--is_put"));
    assert!(!has(&neither, "--is_taker_buy"));
}

#[test]
fn quote_sends_premium_asset_only_when_set() {
    let sdk = sdk();
    let with_asset = sdk
        .quote_args(
            "chan",
            "rfq",
            &Quote {
                premium_asset: Some(HANDLER.into()),
                ..quote()
            },
        )
        .unwrap();
    assert_eq!(flag_value(&with_asset, "--premium_asset"), HANDLER);

    let without = sdk.quote_args("chan", "rfq", &quote()).unwrap();
    assert!(!has(&without, "--premium_asset"));
}

#[test]
fn websocket_quote_needs_a_whole_domain() {
    let err = sdk()
        .quote_args(
            "chan",
            "rfq",
            &Quote {
                domain: Some(TypedDataDomain {
                    name: Some("rysk".into()),
                    ..Default::default()
                }),
                ..quote()
            },
        )
        .unwrap_err();
    assert!(
        err.to_string()
            .contains("missing version, verifyingContract"),
        "got {err}"
    );
}

#[test]
fn websocket_quote_without_a_domain_sends_no_domain_flags() {
    let args = sdk().quote_args("chan", "rfq", &quote()).unwrap();
    assert!(!has(&args, "--domain_name"));
    assert!(!has(&args, "--domain_verifying_contract"));
}

// -------------------------------------------------------------- premium quote

#[test]
fn premium_quote_carries_domain_and_request_id() {
    let args = sdk()
        .premium_quote_args(
            "req-1",
            &Quote {
                domain: Some(TypedDataDomain {
                    name: Some("PremiumOptionHandler".into()),
                    version: Some("1".into()),
                    verifying_contract: Some(HANDLER.into()),
                    ..Default::default()
                }),
                ..quote()
            },
            None,
        )
        .unwrap();

    assert_eq!(&args[..2], ["premium", "quote"]);
    assert_eq!(flag_value(&args, "--request_id"), "req-1");
    assert_eq!(flag_value(&args, "--domain_name"), "PremiumOptionHandler");
    assert_eq!(flag_value(&args, "--domain_version"), "1");
    assert_eq!(flag_value(&args, "--domain_verifying_contract"), HANDLER);
}

#[test]
fn premium_defaults_the_domain_name() {
    let args = sdk()
        .premium_quote_args(
            "req",
            &Quote {
                domain: Some(premium_domain()),
                ..quote()
            },
            None,
        )
        .unwrap();
    assert!(!has(&args, "--domain_name"));
    assert!(!has(&args, "--domain_version"));
    assert_eq!(flag_value(&args, "--domain_verifying_contract"), HANDLER);
}

#[test]
fn premium_url_only_passed_when_given() {
    let sdk = sdk();
    assert!(!has(&sdk.premium_requests_args(MAKER, None), "--url"));

    let args = sdk.premium_requests_args(MAKER, Some("http://localhost:8080"));
    assert_eq!(flag_value(&args, "--url"), "http://localhost:8080");

    // every premium subcommand takes it
    assert!(has(
        &sdk.premium_quotes_args(MAKER, Some("http://x")),
        "--url"
    ));
    assert!(has(
        &sdk.premium_quote_status_args("id", Some("http://x")),
        "--url"
    ));
    assert!(has(
        &sdk.premium_cancel_args("id", 1, "1", Some("http://x")),
        "--url"
    ));
    assert!(has(
        &sdk.premium_quote_batch_args("f.json", Some("http://x")),
        "--url"
    ));
}

#[test]
fn premium_rejects_a_domain_with_no_verifying_contract() {
    let err = sdk().premium_quote_args("req", &quote(), None).unwrap_err();
    assert!(
        err.to_string().contains("missing verifyingContract"),
        "got {err}"
    );
}

#[test]
fn premium_rejects_a_salted_domain() {
    let err = sdk()
        .premium_quote_args(
            "req",
            &Quote {
                domain: Some(TypedDataDomain {
                    salt: Some("0x01".into()),
                    ..premium_domain()
                }),
                ..quote()
            },
            None,
        )
        .unwrap_err();
    assert!(
        err.to_string().contains("salt is not supported"),
        "got {err}"
    );
}

#[test]
fn premium_rejects_a_domain_for_another_chain() {
    let err = sdk()
        .premium_quote_args(
            "req",
            &Quote {
                domain: Some(TypedDataDomain {
                    chain_id: Some(ryskv12::ChainId::Number(8453)),
                    ..premium_domain()
                }),
                ..quote()
            },
            None,
        )
        .unwrap_err();
    assert!(
        err.to_string()
            .contains("does not match the quote's chain 84532"),
        "got {err}"
    );
}

#[test]
fn premium_accepts_a_matching_hex_chain_id() {
    // the server marshals chain ids as hex strings, so 0x14a34 is 84532
    let args = sdk()
        .premium_quote_args(
            "req",
            &Quote {
                domain: Some(TypedDataDomain {
                    chain_id: Some(ryskv12::ChainId::Text("0x14a34".into())),
                    ..premium_domain()
                }),
                ..quote()
            },
            None,
        )
        .unwrap();
    assert_eq!(flag_value(&args, "--domain_verifying_contract"), HANDLER);
}

#[test]
fn premium_rejects_a_chain_id_that_is_not_a_number() {
    let err = sdk()
        .premium_quote_args(
            "req",
            &Quote {
                domain: Some(TypedDataDomain {
                    chain_id: Some(ryskv12::ChainId::Text("mainnet".into())),
                    ..premium_domain()
                }),
                ..quote()
            },
            None,
        )
        .unwrap_err();
    assert!(err.to_string().contains("does not match"), "got {err}");
}

// --------------------------------------------------------------------- batch

#[test]
fn premium_quote_batch_serialises_entries() {
    let sdk = sdk();
    let with_domain = Quote {
        domain: Some(premium_domain()),
        ..quote()
    };
    let json = sdk
        .premium_quote_batch(&[
            ("req-0".to_string(), with_domain.clone()),
            (
                "req-1".to_string(),
                Quote {
                    nonce: "43".into(),
                    ..with_domain
                },
            ),
        ])
        .unwrap();

    let batch: serde_json::Value = serde_json::from_str(&json).unwrap();
    let batch = batch.as_array().unwrap();

    assert_eq!(batch.len(), 2);
    assert_eq!(batch[0]["requestId"], "req-0");
    assert_eq!(batch[1]["nonce"], "43");
    assert_eq!(batch[0]["domain"]["verifyingContract"], HANDLER);
    assert_eq!(batch[0]["assetAddress"], ASSET);
    // the cli reads camelCase, whatever rust calls the fields
    assert_eq!(batch[0]["collateralAsset"], ASSET);
    assert_eq!(batch[0]["validUntil"], 1767139500);
    // an unset optional is left out rather than sent as null
    assert!(batch[0].get("premiumAsset").is_none());
}

#[test]
fn premium_quote_batch_refuses_unsignable_entries() {
    // nothing is written when one entry could not be signed
    let err = sdk()
        .premium_quote_batch(&[("req".to_string(), quote())])
        .unwrap_err();
    assert!(
        err.to_string().contains("missing verifyingContract"),
        "got {err}"
    );
}

// ------------------------------------------------------------------ the key

#[test]
fn no_arg_builder_carries_the_private_key() {
    let sdk = sdk();
    let with_domain = Quote {
        domain: Some(TypedDataDomain {
            name: Some("rysk".into()),
            version: Some("1".into()),
            verifying_contract: Some(HANDLER.into()),
            ..Default::default()
        }),
        ..quote()
    };

    let builders: Vec<Vec<String>> = vec![
        sdk.connect_args("chan", "maker"),
        sdk.disconnect_args("chan"),
        sdk.approve_args(84532, "1", "https://rpc", Some(ASSET)),
        sdk.balances_args("chan", MAKER),
        sdk.positions_args("chan", MAKER),
        sdk.transfer_args(
            "chan",
            &Transfer {
                user: MAKER.into(),
                amount: "1".into(),
                asset: ASSET.into(),
                chain_id: 84532,
                is_deposit: true,
                nonce: "1".into(),
            },
        ),
        sdk.quote_args("chan", "rfq", &with_domain).unwrap(),
        sdk.premium_requests_args(MAKER, None),
        sdk.premium_quotes_args(MAKER, None),
        sdk.premium_quote_status_args("id", None),
        sdk.premium_quote_args("rfq", &with_domain, None).unwrap(),
        sdk.premium_quote_batch_args("batch.json", None),
        sdk.premium_cancel_args("id", 84532, "43", None),
    ];

    for args in builders {
        let head = args.join(" ");
        assert!(
            !args.iter().any(|a| a == "--private_key"),
            "{head} leaks the key"
        );
        assert!(
            !args.iter().any(|a| a.contains(KEY)),
            "{head} leaks the key"
        );
    }
}

// ---------------------------------------------------------------- nonce

#[test]
fn nonce_counter_never_repeats_or_rewinds() {
    let dir = std::env::temp_dir().join(format!("rysk-nonce-test-{}", std::process::id()));
    std::fs::create_dir_all(&dir).unwrap();
    let path = dir.join("counter");
    let path = path.to_str().unwrap();

    let mut first = NonceCounter::new(path);
    let issued: Vec<String> = (0..3).map(|_| first.next().unwrap()).collect();

    let unique: std::collections::HashSet<&String> = issued.iter().collect();
    assert_eq!(unique.len(), issued.len(), "handed out a nonce twice");

    let mut ascending = issued.clone();
    ascending.sort_by_key(|n| n.parse::<u128>().unwrap());
    assert_eq!(ascending, issued, "went backwards");

    // a restart must not rewind: the file is the floor
    let mut restarted = NonceCounter::new(path);
    let after: u128 = restarted.next().unwrap().parse().unwrap();
    let before: u128 = issued.last().unwrap().parse().unwrap();
    assert!(after > before, "{after} did not beat {before}");

    // a counter with no file at all still starts from the clock, never 0
    let fresh = NonceCounter::new(dir.join("absent").to_str().unwrap());
    assert_eq!(fresh.last(), 0);

    std::fs::remove_dir_all(&dir).ok();
}
