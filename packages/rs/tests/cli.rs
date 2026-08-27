//! Drives the real cli binary against a fake premium api: the arg builders and
//! the binary have to agree on flags, the key has to travel in the environment,
//! and a rejected quote has to fail the command.
//!
//! Skipped when no cli is present. `make dev-bin` from the repo root builds one.

use std::collections::HashMap;
use std::io::{BufRead, BufReader, Read, Write};
use std::net::TcpListener;
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{SystemTime, UNIX_EPOCH};

use ryskv12::{Env, Quote, Rysk, TypedDataDomain};

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

    let args = sdk
        .premium_quote_args("req-1", &quote(), Some(&api.url))
        .unwrap();
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

    let dir = std::env::temp_dir().join(format!("rysk-rs-batch-{}", std::process::id()));
    std::fs::create_dir_all(&dir).unwrap();
    let path = dir.join("batch.json");
    std::fs::write(&path, sdk.premium_quote_batch(&entries).unwrap()).unwrap();

    let args = sdk.premium_quote_batch_args(path.to_str().unwrap(), Some(&api.url));
    let (code, _, stderr) = run(&sdk, &args);
    assert_eq!(code, 0, "{stderr}");

    let calls = api.calls();
    assert_eq!(calls.len(), 1, "a batch has to be one request");
    let posted: serde_json::Value = serde_json::from_str(&calls[0].body).unwrap();
    assert_eq!(posted.as_array().unwrap().len(), 5);

    std::fs::remove_dir_all(&dir).ok();
}

#[test]
fn a_rejected_quote_fails_the_command() {
    let sdk = sdk_or_skip!();
    let api = FakeApi::json_ok(r#"{"failures":[{"error":"request expired","quote":{}}]}"#);

    let args = sdk
        .premium_quote_args("req-1", &quote(), Some(&api.url))
        .unwrap();
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
