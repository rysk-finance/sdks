//! Drives the real cli binary against a fake premium api: the arg builders and
//! the binary have to agree on flags, the key has to travel in the environment,
//! and a rejected quote has to fail the command.
//!
//! Skipped when no cli is present. `make dev-bin` from the repo root builds one.

use std::collections::HashMap;
use std::io::{BufRead, BufReader, Read, Write};
use std::net::TcpListener;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{SystemTime, UNIX_EPOCH};

use ryskv12::{Env, Quote, Request, Rysk, TypedDataDomain};

/// well known test key; its address is the maker below
const PK: &str = "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318";
const MAKER: &str = "0x2c7536E3605D9C16a7a3D7b1898e529396a65c23";
const HANDLER: &str = "0x54f1cc396e08f0defbe956bcddf6abe46d61cb48";
const ASSET: &str = "0x98d56648c9b7f3cb49531f4135115b5000ab1733";

fn cli_path() -> String {
    std::env::var("RYSK_CLI_PATH").unwrap_or_else(|_| "./ryskV12".to_string())
}

/// `None`, and a note on stderr, when there is no binary to drive. Rust has no
/// skip, so every test here returns early instead.
fn sdk() -> Option<Rysk> {
    let cli = cli_path();
    if !std::path::Path::new(&cli).exists() {
        eprintln!("no cli at {cli}; run `make dev-bin`");
        return None;
    }
    Some(
        Rysk::builder(Env::Testnet, PK)
            .cli_path(&cli)
            .build()
            .expect("building a client must not fail"),
    )
}

macro_rules! sdk_or_skip {
    () => {
        match sdk() {
            Some(sdk) => sdk,
            None => return,
        }
    };
}

fn millis() -> u128 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_millis()
}

fn quote() -> Quote {
    Quote {
        asset_address: ASSET.into(),
        chain_id: 84532,
        expiry: 1767225600,
        is_put: true,
        is_taker_buy: false,
        maker: MAKER.into(),
        nonce: millis().to_string(),
        price: "1250000000000000000".into(),
        quantity: "1000000000000000000".into(),
        strike: "300000000000".into(),
        // the api takes a window of now+2min .. now+10min
        valid_until: (millis() / 1000) as i64 + 300,
        usd: ASSET.into(),
        collateral_asset: ASSET.into(),
        premium_asset: None,
        domain: Some(TypedDataDomain {
            verifying_contract: Some(HANDLER.into()),
            ..Default::default()
        }),
    }
}

#[derive(Clone, Debug)]
struct Call {
    method: String,
    path: String,
    headers: HashMap<String, String>,
    body: String,
}

/// Records what reached it and answers with whatever `respond` returns for the
/// nth call.
struct FakeApi {
    url: String,
    seen: Arc<Mutex<Vec<Call>>>,
}

impl FakeApi {
    fn new(respond: impl Fn(usize) -> (u16, Vec<u8>) + Send + 'static) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind a port");
        let url = format!("http://{}", listener.local_addr().unwrap());
        let seen = Arc::new(Mutex::new(Vec::new()));
        let recorded = Arc::clone(&seen);

        thread::spawn(move || {
            for stream in listener.incoming() {
                let Ok(stream) = stream else { break };
                let mut reader = BufReader::new(match stream.try_clone() {
                    Ok(s) => s,
                    Err(_) => break,
                });

                let mut start = String::new();
                if reader.read_line(&mut start).is_err() || start.is_empty() {
                    continue;
                }
                let mut parts = start.split_whitespace();
                let method = parts.next().unwrap_or_default().to_string();
                let path = parts.next().unwrap_or_default().to_string();

                let mut headers = HashMap::new();
                loop {
                    let mut line = String::new();
                    if reader.read_line(&mut line).is_err() || line.trim().is_empty() {
                        break;
                    }
                    if let Some((k, v)) = line.split_once(':') {
                        headers.insert(k.trim().to_lowercase(), v.trim().to_string());
                    }
                }

                let len: usize = headers
                    .get("content-length")
                    .and_then(|v| v.parse().ok())
                    .unwrap_or(0);
                let mut body = vec![0u8; len];
                if len > 0 && reader.read_exact(&mut body).is_err() {
                    continue;
                }

                let nth = {
                    let mut seen = recorded.lock().unwrap();
                    seen.push(Call {
                        method,
                        path,
                        headers,
                        body: String::from_utf8_lossy(&body).to_string(),
                    });
                    seen.len()
                };

                let (status, payload) = respond(nth);
                let mut stream = stream;
                let head = format!(
                    "HTTP/1.1 {status} X\r\nContent-Type: application/json\r\n\
                     Content-Length: {}\r\nConnection: close\r\n\r\n",
                    payload.len()
                );
                let _ = stream.write_all(head.as_bytes());
                let _ = stream.write_all(&payload);
                let _ = stream.flush();
            }
        });

        Self { url, seen }
    }

    fn json_ok(body: &'static str) -> Self {
        Self::new(move |_| (200, body.as_bytes().to_vec()))
    }

    fn calls(&self) -> Vec<Call> {
        self.seen.lock().unwrap().clone()
    }
}

/// Runs the cli and collects everything it said.
fn run(sdk: &Rysk, args: &[String]) -> (i32, String, String) {
    let child = sdk.execute(args).expect("spawn the cli");
    let out = child.wait_with_output().expect("wait for the cli");
    (
        out.status.code().unwrap_or(-1),
        String::from_utf8_lossy(&out.stdout).trim().to_string(),
        String::from_utf8_lossy(&out.stderr).trim().to_string(),
    )
}

/// Writes quotes as a batch file and returns the args that post it.
fn quote_batch_args(sdk: &Rysk, url: Option<&str>, entries: &[(String, Quote)]) -> Vec<String> {
    static N: AtomicUsize = AtomicUsize::new(0);
    let path = std::env::temp_dir().join(format!(
        "rysk-rs-batch-{}-{}.json",
        std::process::id(),
        N.fetch_add(1, Ordering::Relaxed)
    ));
    std::fs::write(&path, sdk.premium_quote_batch(entries).unwrap()).unwrap();
    sdk.premium_quote_batch_args(path.to_str().unwrap(), url)
}

#[test]
fn the_cli_reports_a_version_the_sdk_accepts() {
    let sdk = sdk_or_skip!();
    let (code, stdout, stderr) = run(&sdk, &["version".to_string()]);
    assert_eq!(code, 0, "{stderr}");
    assert!(!stdout.is_empty(), "version printed nothing");
}

#[test]
fn the_cli_signs_and_posts_what_the_sdk_built() {
    let sdk = sdk_or_skip!();
    let api = FakeApi::json_ok(r#"{"failures":[]}"#);

    let args = quote_batch_args(&sdk, Some(&api.url), &[("req-1".into(), quote())]);
    let (code, stdout, stderr) = run(&sdk, &args);
    assert_eq!(code, 0, "{stderr}");
    assert_eq!(stdout, r#"{"failures":[]}"#);

    let calls = api.calls();
    assert_eq!(calls.len(), 1);
    assert_eq!(calls[0].method, "POST");
    assert_eq!(calls[0].path, "/api/quotes");

    let posted: serde_json::Value = serde_json::from_str(&calls[0].body).unwrap();
    let posted = posted.as_array().unwrap();
    assert_eq!(posted.len(), 1);

    let mut keys: Vec<&String> = posted[0].as_object().unwrap().keys().collect();
    keys.sort();
    assert_eq!(
        keys,
        [
            "maker",
            "nonce",
            "price",
            "requestId",
            "signature",
            "validUntil"
        ]
    );

    // the terms are not posted: the server rebuilds them from the stored request
    let sig = posted[0]["signature"].as_str().unwrap();
    assert!(
        sig.len() == 132
            && sig.starts_with("0x")
            && sig[2..].chars().all(|c| c.is_ascii_hexdigit()),
        "not a signature: {sig}"
    );
}

#[test]
fn a_batch_is_one_call() {
    let sdk = sdk_or_skip!();
    let api = FakeApi::json_ok(r#"{"failures":[]}"#);

    let now = millis();
    let entries: Vec<(String, Quote)> = (0..5)
        .map(|i| {
            (
                format!("req-{i}"),
                Quote {
                    nonce: (now + i).to_string(),
                    ..quote()
                },
            )
        })
        .collect();

    let args = quote_batch_args(&sdk, Some(&api.url), &entries);
    let (code, _, stderr) = run(&sdk, &args);
    assert_eq!(code, 0, "{stderr}");

    let calls = api.calls();
    assert_eq!(calls.len(), 1, "a batch has to be one request");
    let posted: serde_json::Value = serde_json::from_str(&calls[0].body).unwrap();
    assert_eq!(posted.as_array().unwrap().len(), 5);
}

#[test]
fn a_rejected_quote_fails_the_command() {
    let sdk = sdk_or_skip!();
    let api = FakeApi::json_ok(r#"{"failures":[{"error":"request expired","quote":{}}]}"#);

    let args = quote_batch_args(&sdk, Some(&api.url), &[("req-1".into(), quote())]);
    let (code, stdout, stderr) = run(&sdk, &args);

    assert_ne!(code, 0, "the api answers 200, the exit code is the result");
    assert!(stdout.contains("request expired"), "{stdout}");
    assert!(stderr.contains("1 of 1 quotes rejected"), "{stderr}");
}

#[test]
fn rate_limit_and_missing_routes_are_told_apart() {
    let sdk = sdk_or_skip!();
    let api = FakeApi::new(|nth| {
        if nth == 1 {
            (429, b"cc".to_vec())
        } else {
            (404, b"404 page not found".to_vec())
        }
    });

    let args = sdk.premium_requests_args(MAKER, Some(&api.url));
    let (code, _, stderr) = run(&sdk, &args);
    assert_ne!(code, 0);
    assert!(stderr.contains("rate limited"), "{stderr}");

    let (code, _, stderr) = run(&sdk, &args);
    assert_ne!(code, 0);
    assert!(stderr.contains("no rfq routes"), "{stderr}");
}

#[test]
fn the_key_travels_in_the_environment_not_argv() {
    let sdk = sdk_or_skip!();
    let api = FakeApi::new(|_| (204, Vec::new()));

    let args = sdk.premium_cancel_args("24361", 84532, "43", Some(&api.url));
    assert!(!args.iter().any(|a| a == "--private_key"));
    assert!(!args.iter().any(|a| a.contains(PK)), "the key is in argv");

    // and the cli still authenticates, so it did get the key
    let (code, _, stderr) = run(&sdk, &args);
    assert_eq!(code, 0, "{stderr}");

    let calls = api.calls();
    assert_eq!(calls.len(), 1);
    assert!(
        calls[0]
            .headers
            .keys()
            .any(|k| k.contains("signature") || k.contains("auth") || k.contains("maker")),
        "no auth header among {:?}",
        calls[0].headers.keys().collect::<Vec<_>>()
    );
}

#[test]
fn send_writes_one_json_line_of_args() {
    // cat stands in for connect: what goes in on stdin comes straight back
    let mut child = std::process::Command::new("cat")
        .stdin(std::process::Stdio::piped())
        .stdout(std::process::Stdio::piped())
        .spawn()
        .expect("cat must spawn");
    let args: Vec<String> = vec!["quote".into(), "--rfq_id".into(), "a \"b\"\n".into()];
    ryskv12::send(child.stdin.as_mut().unwrap(), &args).unwrap();

    let mut line = String::new();
    BufReader::new(child.stdout.take().unwrap())
        .read_line(&mut line)
        .unwrap();
    child.kill().ok();
    child.wait().ok();
    assert_eq!(serde_json::from_str::<Vec<String>>(&line).unwrap(), args);
}

/// Opt in against the real testnet api (`make e2e` reads the keys from the
/// repo's .env): a taker opens an rfq only the maker may quote, the maker polls
/// for it, quotes, cancels and quotes again, and the taker takes the trade on
/// chain. The trade is real but costs the maker 1 wei; it relies on the maker's
/// premium allowance for the pool's option handler, which the go e2e sets up.
#[test]
fn testnet_open_an_rfq_quote_cancel_requote_and_take_the_trade() {
    let env = |name: &str| std::env::var(name).ok().filter(|v| !v.is_empty());
    let (Some(key), Some(maker), Some(taker_key), Some(pool_addr)) = (
        env("RYSK_E2E_PRIVATE_KEY"),
        env("RYSK_E2E_MAKER"),
        env("RYSK_E2E_TAKER_PRIVATE_KEY"),
        env("RYSK_E2E_POOL"),
    ) else {
        eprintln!(
            "set RYSK_E2E_PRIVATE_KEY, RYSK_E2E_MAKER, RYSK_E2E_TAKER_PRIVATE_KEY and \
             RYSK_E2E_POOL to run against testnet"
        );
        return;
    };
    if sdk().is_none() {
        return;
    }
    let (maker, pool_addr) = (maker.to_lowercase(), pool_addr.to_lowercase());
    let url = env("RYSK_E2E_PREMIUM_URL").unwrap_or("https://insti-testnet.rysk.finance".into());
    let url = Some(url.as_str());
    // base sepolia's strike asset, which a request's usd has to be
    const USD: &str = "0x98d56648c9b7f3cb49531f4135115b5000ab1733";
    let client = |key: String| {
        Rysk::builder(Env::Testnet, key)
            .cli_path(cli_path())
            .build()
            .expect("building a client must not fail")
    };
    let (mk, tk) = (client(key), client(taker_key));
    let ok = |sdk: &Rysk, args: Vec<String>| {
        let (code, stdout, stderr) = run(sdk, &args);
        assert_eq!(code, 0, "{}: {stderr}\n{stdout}", args[1]);
        stdout
    };
    let list = |sdk: &Rysk, args: Vec<String>| -> Vec<serde_json::Value> {
        let out = ok(sdk, args);
        serde_json::from_str(if out.is_empty() { "[]" } else { &out }).expect("a json array")
    };
    let as_text = |v: &serde_json::Value| v.as_str().map(String::from).unwrap_or(v.to_string());
    // unix nanoseconds: spent once per address, fresh across runs
    let last = std::cell::Cell::new(0u128);
    let nonce = || {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        last.set(now.max(last.get() + 1));
        last.get().to_string()
    };

    // 1. the taker opens a request on the pool, only this maker may quote
    let pool = list(&tk, tk.premium_pools_args(url))
        .into_iter()
        .find(|p| p["address"].as_str().map(str::to_lowercase) == Some(pool_addr.clone()))
        .unwrap_or_else(|| panic!("pool {pool_addr} is not listed"));
    let chain_id = pool["chainId"].as_i64().expect("pool chainId");
    // next friday 08:00 utc at least a week out; 1970-01-01 was a thursday
    let mut day = (millis() / 1000 / 86400) as i64 + 7;
    while (day + 4) % 7 != 5 {
        day += 1;
    }
    let dir = std::env::temp_dir().join(format!("rysk-rs-e2e-{}", std::process::id()));
    std::fs::create_dir_all(&dir).unwrap();
    let file = dir.join("requests.json");
    let requests = serde_json::json!([{
        "asset": pool["underlying"],
        "chainId": chain_id,
        "expiry": day * 86400 + 8 * 3600,
        "isPut": pool["type"] == "Put",
        "isTakerBuy": false,
        "quantity": "1000000000000000000",
        "strike": "100000000000000",
        "taker": pool["address"],
        "usd": USD,
        "collateralAsset": pool["asset"],
        "premiumAsset": pool["asset"],
        "isPremium": true,
        "makers": [maker],
        "validUntil": millis() as u64 + 5 * 60_000,
    }]);
    std::fs::write(&file, requests.to_string()).unwrap();
    ok(
        &tk,
        tk.premium_request_args(file.to_str().unwrap(), chain_id, &nonce(), url),
    );
    std::fs::remove_dir_all(&dir).ok();

    // 2. the maker polls its requests until that one shows up
    let mut request = None;
    for _ in 0..30 {
        request = list(&mk, mk.premium_requests_args(&maker, url))
            .into_iter()
            .filter_map(|r| serde_json::from_value::<Request>(r).ok())
            .rfind(|r| r.taker.to_lowercase() == pool_addr);
        if request.is_some() {
            break;
        }
        thread::sleep(std::time::Duration::from_secs(1));
    }
    let request =
        request.unwrap_or_else(|| panic!("no request from {pool_addr} listed for {maker}"));
    let request_id = request.id.clone().expect("a listed request has an id");

    let quote = || {
        let n = nonce();
        let quote = Quote {
            asset_address: request.asset.clone(),
            chain_id: request.chain_id,
            expiry: request.expiry,
            is_put: request.is_put,
            is_taker_buy: request.is_taker_buy.unwrap_or(false),
            maker: maker.clone(),
            nonce: n.clone(),
            price: "1".into(),
            quantity: request.quantity.clone(),
            strike: request.strike.clone(),
            valid_until: (millis() / 1000) as i64 + 300,
            usd: request.usd.clone(),
            collateral_asset: request.collateral_asset.clone(),
            premium_asset: None,
            domain: request.type_data_domain.clone(),
        };
        ok(
            &mk,
            quote_batch_args(&mk, url, &[(request_id.clone(), quote)]),
        );
        n
    };
    let on_book = |n: &str| {
        list(&mk, mk.premium_quotes_args(&maker, url))
            .iter()
            .find(|q| as_text(&q["nonce"]) == n)
            .map(|q| as_text(&q["id"]))
    };

    // 3-4. quote, then cancel it off the book
    let first = quote();
    let id = on_book(&first)
        .unwrap_or_else(|| panic!("quote with nonce {first} is not in the maker's live quotes"));
    ok(
        &mk,
        mk.premium_cancel_args(&id, request.chain_id, &nonce(), url),
    );
    assert_eq!(
        on_book(&first),
        None,
        "quote {id} is still live after cancel"
    );

    // 5-6. quote again, and the taker takes it from the request's book
    let second = quote();
    let picked = list(&tk, tk.premium_request_quotes_args(&request_id, url))
        .into_iter()
        .find(|q| as_text(&q["nonce"]) == second)
        .map(|q| as_text(&q["id"]))
        .unwrap_or_else(|| {
            panic!("quote with nonce {second} is not on request {request_id}'s book")
        });
    let tx = ok(&tk, tk.premium_execute_args(&picked, &nonce(), url));
    assert!(
        tx.len() == 66 && tx.starts_with("0x") && tx[2..].chars().all(|c| c.is_ascii_hexdigit()),
        "not a transaction hash: {tx}"
    );
    assert_eq!(
        on_book(&second),
        None,
        "quote {picked} is still live after the trade"
    );
}
