use std::io::{BufRead, BufReader, Write};
use std::process::{Child, ChildStdin, Command, Stdio};

use crate::error::Error;
use crate::models::{Quote, Transfer, TypedDataDomain};

pub const RELEASES_URL: &str = "https://github.com/rysk-finance/sdks/releases";

/// Oldest cli this sdk will not warn about. Kept in step with the other sdks'
/// `_minSdkVersion` / `_min_sdk_version`.
const MIN_CLI_VERSION: (u64, u64, u64) = (5, 3, 0);

const DEFAULT_CLI_PATH: &str = "./ryskV12";

/// The download script the published crate carries, so `setup` works from a
/// crates.io install with no repo checked out.
const FETCH_SCRIPT: &str = include_str!("../scripts/fetch_latest_release.sh");

/// Which api the cli connects to.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Env {
    Local,
    Testnet,
    Mainnet,
}

impl Env {
    pub fn base_url(&self) -> &'static str {
        match self {
            Env::Local => "ws://localhost:8000/",
            Env::Testnet => "wss://rip-testnet.rysk.finance/",
            Env::Mainnet => "wss://v12.rysk.finance/",
        }
    }
}

/// Parses a leading semver out of `4.0.0`, `v4.0.0` or `4.0.0-rc1`.
///
/// `None` for anything without one, which is how a locally built cli reporting
/// `dev` skips the check instead of failing it.
fn parse_version(raw: &str) -> Option<(u64, u64, u64)> {
    let raw = raw.trim();
    let raw = raw.strip_prefix('v').unwrap_or(raw);
    let mut parts = raw.split('.');
    let major = take_leading_digits(parts.next()?)?;
    let minor = take_leading_digits(parts.next()?)?;
    // the patch may carry a prerelease suffix: 0-rc1
    let patch = take_leading_digits(parts.next()?)?;
    Some((major, minor, patch))
}

fn take_leading_digits(s: &str) -> Option<u64> {
    let digits: String = s.chars().take_while(|c| c.is_ascii_digit()).collect();
    if digits.is_empty() {
        return None;
    }
    digits.parse().ok()
}

fn pair(args: &mut Vec<String>, flag: &str, value: impl AsRef<str>) {
    args.push(flag.to_string());
    args.push(value.as_ref().to_string());
}

/// Writes one command into a running `connect`, which signs it and sends it over
/// its websocket - no process per command. `args` are what a `*_args` builder
/// returns, e.g. [`Rysk::quote_args`].
///
/// Take `child.stdin` from a connect spawned with [`Rysk::execute`] and keep it.
/// Dropping it stops further commands but not the connection; `child.kill()`
/// disconnects. A command the cli rejects comes back on stdout as a json-rpc
/// error, under the id the server would have answered with.
pub fn send(stdin: &mut ChildStdin, args: &[String]) -> Result<(), Error> {
    let mut line = serde_json::to_string(args)?;
    line.push('\n');
    stdin.write_all(line.as_bytes())?;
    Ok(stdin.flush()?)
}

/// Builds argument lists for the `ryskV12` cli and spawns it.
///
/// Every `*_args` method is pure: it returns what the cli would be given
/// without running anything, so the whole surface is testable without a binary.
pub struct Rysk {
    env: Env,
    cli_path: String,
    private_key: String,
    strict_version: bool,
}

/// Configures a [`Rysk`] before the cli version is checked.
pub struct RyskBuilder(Rysk);

impl RyskBuilder {
    /// Where the cli binary lives. Defaults to `./ryskV12`.
    pub fn cli_path(mut self, path: impl Into<String>) -> Self {
        self.0.cli_path = path.into();
        self
    }

    /// Fail instead of warning when the cli is older than this sdk needs.
    pub fn strict_version(mut self, strict: bool) -> Self {
        self.0.strict_version = strict;
        self
    }

    /// Checks the cli version, then hands back the client.
    pub fn build(self) -> Result<Rysk, Error> {
        self.0.version_check()?;
        Ok(self.0)
    }
}

impl Rysk {
    /// A client against `./ryskV12`, warning rather than failing on an old cli.
    pub fn new(env: Env, private_key: impl Into<String>) -> Result<Self, Error> {
        Self::builder(env, private_key).build()
    }

    pub fn builder(env: Env, private_key: impl Into<String>) -> RyskBuilder {
        RyskBuilder(Rysk {
            env,
            cli_path: DEFAULT_CLI_PATH.to_string(),
            private_key: private_key.into(),
            strict_version: false,
        })
    }

    fn url(&self, uri: &str) -> String {
        format!("{}{}", self.env.base_url(), uri)
    }

    // ---------------------------------------------------------------- process

    /// The private key is handed to the cli through `RYSK_PRIVATE_KEY` rather
    /// than as an argument, so it never shows up in `ps` or a shell history.
    /// Spawning the cli yourself means setting that variable too.
    fn command(&self, args: &[String]) -> Command {
        let mut cmd = Command::new(&self.cli_path);
        cmd.args(args)
            .env("RYSK_PRIVATE_KEY", &self.private_key)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        cmd
    }

    /// Spawns the cli. The caller owns the child's pipes.
    pub fn execute(&self, args: &[String]) -> Result<Child, Error> {
        Ok(self.command(args).spawn()?)
    }

    /// Spawns the cli and hands each line of its stdout to `on_line`, returning
    /// the exit status once it closes. This is the blocking twin of the other
    /// sdks' `execute_async`.
    ///
    /// The child keeps its stdin, so a connect run through this stays up but only
    /// listens: to [`send`] it commands, spawn it with [`Rysk::execute`] instead.
    pub fn execute_lines(
        &self,
        args: &[String],
        mut on_line: impl FnMut(&str),
    ) -> Result<std::process::ExitStatus, Error> {
        let mut child = self.execute(args)?;
        if let Some(stdout) = child.stdout.take() {
            for line in BufReader::new(stdout).lines() {
                on_line(&line?);
            }
        }
        Ok(child.wait()?)
    }

    /// Downloads the cli release into the working directory as `./ryskV12` and
    /// makes it executable, returning that path.
    ///
    /// This blocks: the sdk cannot spawn a binary that is still downloading.
    #[cfg(unix)]
    pub fn setup(&self) -> Result<String, Error> {
        // The script ships inside the crate, so it is written out and run rather
        // than shelled out to from a path that only exists in a repo checkout.
        let mut child = Command::new("bash") // the script uses [[ ]], which dash (debian /bin/sh) lacks
            .arg("-s")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()?;
        child
            .stdin
            .take()
            .expect("stdin was piped")
            .write_all(FETCH_SCRIPT.as_bytes())?;
        let out = child.wait_with_output()?;

        let stdout = String::from_utf8_lossy(&out.stdout).trim().to_string();
        let stderr = String::from_utf8_lossy(&out.stderr).trim().to_string();
        if !out.status.success() {
            let detail = if stderr.is_empty() { &stdout } else { &stderr };
            return Err(Error::Io(std::io::Error::other(format!(
                "failed to download the cli: {detail}"
            ))));
        }

        let downloaded = "ryskV12";
        if !std::path::Path::new(downloaded).exists() {
            // the script reports missing dependencies on stdout and still exits 0
            let detail = if stdout.is_empty() { &stderr } else { &stdout };
            return Err(Error::Io(std::io::Error::other(format!(
                "the download script left no {downloaded}: {detail}"
            ))));
        }
        Ok(downloaded.to_string())
    }

    // ---------------------------------------------------------------- version

    fn version_check(&self) -> Result<(), Error> {
        let out = match Command::new(&self.cli_path).arg("version").output() {
            Ok(out) => out,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                eprintln!(
                    "{} not found.\nDownload it here {RELEASES_URL}.",
                    self.cli_path
                );
                return Ok(());
            }
            Err(e) => {
                eprintln!("{} could not be run: {e}", self.cli_path);
                return Ok(());
            }
        };

        let stdout = String::from_utf8_lossy(&out.stdout).trim().to_string();
        let stderr = String::from_utf8_lossy(&out.stderr);

        // a cli old enough to have no version command predates every version we support
        if stderr.contains("No help topic for 'version'") || stdout.is_empty() {
            return self.report_version("too old to report a version");
        }

        match parse_version(&stdout) {
            // a locally built cli reports something like "dev"; nothing to compare
            None => Ok(()),
            Some(found) if found < MIN_CLI_VERSION => self.report_version(&stdout),
            Some(_) => Ok(()),
        }
    }

    fn report_version(&self, found: &str) -> Result<(), Error> {
        let (ma, mi, pa) = MIN_CLI_VERSION;
        let message = format!(
            "{} is {found}, but this sdk needs {ma}.{mi}.{pa} or newer. Commands added \
             since {found} will fail with \"flag provided but not defined\".\n\
             Download it here {RELEASES_URL}.",
            self.cli_path
        );
        if self.strict_version {
            return Err(Error::CliVersion(message));
        }
        eprintln!("{message}");
        Ok(())
    }

    // ----------------------------------------------------------- arg builders

    /// Commands for this connection go in on its stdin, through [`send`].
    pub fn connect_args(&self, uri: &str) -> Vec<String> {
        vec!["connect".into(), "--url".into(), self.url(uri)]
    }

    /// `asset` is the erc20 to approve. Left out, the cli falls back to the
    /// chain's strike asset.
    pub fn approve_args(
        &self,
        chain_id: i64,
        amount: &str,
        rpc_url: &str,
        asset: Option<&str>,
    ) -> Vec<String> {
        let mut args = vec!["approve".to_string()];
        pair(&mut args, "--chain_id", chain_id.to_string());
        pair(&mut args, "--amount", amount);
        pair(&mut args, "--rpc_url", rpc_url);
        if let Some(asset) = asset.filter(|a| !a.is_empty()) {
            pair(&mut args, "--asset", asset);
        }
        args
    }

    pub fn balances_args(&self, account: &str) -> Vec<String> {
        vec!["balances".into(), "--account".into(), account.into()]
    }

    pub fn positions_args(&self, account: &str) -> Vec<String> {
        vec!["positions".into(), "--account".into(), account.into()]
    }

    pub fn transfer_args(&self, transfer: &Transfer) -> Vec<String> {
        let mut args = vec!["transfer".to_string()];
        pair(&mut args, "--chain_id", transfer.chain_id.to_string());
        pair(&mut args, "--user", &transfer.user);
        pair(&mut args, "--asset", &transfer.asset);
        pair(&mut args, "--amount", &transfer.amount);
        pair(&mut args, "--nonce", &transfer.nonce);
        if transfer.is_deposit {
            args.push("--is_deposit".into());
        }
        args
    }

    pub fn quote_args(&self, rfq_id: &str, quote: &Quote) -> Result<Vec<String>, Error> {
        let mut args = vec!["quote".to_string()];
        pair(&mut args, "--rfq_id", rfq_id);
        pair(&mut args, "--asset", &quote.asset_address);
        pair(&mut args, "--chain_id", quote.chain_id.to_string());
        pair(&mut args, "--expiry", quote.expiry.to_string());
        pair(&mut args, "--maker", &quote.maker);
        pair(&mut args, "--nonce", &quote.nonce);
        pair(&mut args, "--price", &quote.price);
        pair(&mut args, "--quantity", &quote.quantity);
        pair(&mut args, "--strike", &quote.strike);
        pair(&mut args, "--valid_until", quote.valid_until.to_string());
        pair(&mut args, "--usd", &quote.usd);
        pair(&mut args, "--collateral", &quote.collateral_asset);
        if let Some(premium) = quote.premium_asset.as_deref().filter(|p| !p.is_empty()) {
            pair(&mut args, "--premium_asset", premium);
        }
        args.extend(domain_args(quote, false)?);
        if quote.is_put {
            args.push("--is_put".into());
        }
        if quote.is_taker_buy {
            args.push("--is_taker_buy".into());
        }
        Ok(args)
    }

    // ------------------------------------------------------------ premium rfq

    /// Opts an otoken in or out of redemption at expiry. Send it into a running
    /// `connect` on the maker endpoint.
    pub fn will_redeem_args(
        &self,
        chain_id: i64,
        account: &str,
        otoken: &str,
        will_redeem: bool,
        nonce: &str,
    ) -> Vec<String> {
        let mut args = vec!["will_redeem".to_string()];
        pair(&mut args, "--chain_id", chain_id.to_string());
        pair(&mut args, "--account", account);
        pair(&mut args, "--otoken", otoken);
        pair(&mut args, "--nonce", nonce);
        if will_redeem {
            args.push("--will_redeem".into());
        }
        args
    }

    /// Redeems the signer's own otokens that expired at or before `expiry`
    /// (default: now). A plain http call, so run it with `execute`, not `send`.
    /// `url` defaults to this env's api.
    pub fn self_redeem_args(
        &self,
        chain_id: i64,
        nonce: &str,
        expiry: Option<i64>,
        exclude_assets: &[&str],
        url: Option<&str>,
    ) -> Vec<String> {
        let default_url = format!("http{}", self.env.base_url()[2..].trim_end_matches('/'));
        let mut args = vec!["self-redeem".to_string()];
        pair(&mut args, "--url", url.unwrap_or(&default_url));
        pair(&mut args, "--chain_id", chain_id.to_string());
        pair(&mut args, "--nonce", nonce);
        if let Some(expiry) = expiry {
            pair(&mut args, "--expiry", expiry.to_string());
        }
        if !exclude_assets.is_empty() {
            pair(&mut args, "--exclude_assets", exclude_assets.join(","));
        }
        args
    }

    /// `premium <sub>` with its flags, then `--url` last. The url is left out
    /// when not given, so the cli's own default (production) applies; pass it
    /// for a local or staging api.
    fn premium(&self, sub: &str, flags: &[(&str, &str)], url: Option<&str>) -> Vec<String> {
        let mut args = vec!["premium".to_string(), sub.to_string()];
        for (flag, value) in flags {
            pair(&mut args, flag, value);
        }
        if let Some(url) = url.filter(|u| !u.is_empty()) {
            pair(&mut args, "--url", url);
        }
        args
    }

    /// Requests this maker may quote, each carrying the domain to sign against.
    pub fn premium_requests_args(&self, maker: &str, url: Option<&str>) -> Vec<String> {
        self.premium("requests", &[("--maker", maker)], url)
    }

    /// This maker's live quotes: the only place quote ids come from.
    pub fn premium_quotes_args(&self, maker: &str, url: Option<&str>) -> Vec<String> {
        self.premium("quotes", &[("--maker", maker)], url)
    }

    /// An account's settled trades, newest first, indexed from the chain: how a maker learns which of its quotes were filled.
    pub fn premium_trades_args(
        &self,
        account: &str,
        chain_id: Option<i64>,
        pool: Option<&str>,
        url: Option<&str>,
    ) -> Vec<String> {
        let chain_id = chain_id.filter(|id| *id != 0).map(|id| id.to_string());
        let mut flags = vec![("--account", account)];
        if let Some(id) = &chain_id {
            flags.push(("--chain_id", id));
        }
        if let Some(pool) = pool.filter(|p| !p.is_empty()) {
            flags.push(("--pool", pool));
        }
        self.premium("trades", &flags, url)
    }

    /// One quote by id, whatever its status.
    pub fn premium_quote_status_args(&self, id: &str, url: Option<&str>) -> Vec<String> {
        self.premium("quote-status", &[("--id", id)], url)
    }

    /// Posts a batch of quotes in one call, reading them from `source`: a file
    /// path, or `-` for stdin. Build the file with [`Rysk::premium_quote_batch`].
    pub fn premium_quote_batch_args(&self, source: &str, url: Option<&str>) -> Vec<String> {
        self.premium("quote", &[("--batch", source)], url)
    }

    /// Serialises (request id, quote) pairs into the json array
    /// [`Rysk::premium_quote_batch_args`] reads, failing on a domain the cli
    /// could not sign before anything is written.
    pub fn premium_quote_batch(&self, quotes: &[(String, Quote)]) -> Result<String, Error> {
        #[derive(serde::Serialize)]
        struct Entry<'a> {
            #[serde(flatten)]
            quote: &'a Quote,
            #[serde(rename = "requestId")]
            request_id: &'a str,
        }

        let mut entries = Vec::with_capacity(quotes.len());
        for (request_id, quote) in quotes {
            domain_args(quote, true)?;
            entries.push(Entry {
                quote,
                request_id: request_id.as_str(),
            });
        }
        Ok(serde_json::to_string(&entries)?)
    }

    /// Pulls one of this maker's quotes. The nonce is spent once and shares its
    /// keyspace with quote nonces, so draw it from the same counter.
    pub fn premium_cancel_args(
        &self,
        id: &str,
        chain_id: i64,
        nonce: &str,
        url: Option<&str>,
    ) -> Vec<String> {
        self.premium(
            "cancel",
            &[
                ("--id", id),
                ("--chain_id", &chain_id.to_string()),
                ("--nonce", nonce),
            ],
            url,
        )
    }

    /// Taker: the pools requests can be opened on.
    pub fn premium_pools_args(&self, url: Option<&str>) -> Vec<String> {
        self.premium("pools", &[], url)
    }

    /// Taker: opens the requests in `source` (a json array, file path or `-` for
    /// stdin). This key has to be an rfq signer on each request's pool.
    pub fn premium_request_args(
        &self,
        source: &str,
        chain_id: i64,
        nonce: &str,
        url: Option<&str>,
    ) -> Vec<String> {
        self.premium(
            "request",
            &[
                ("--batch", source),
                ("--chain_id", &chain_id.to_string()),
                ("--nonce", nonce),
            ],
            url,
        )
    }

    /// Taker: a pool's live requests.
    pub fn premium_taker_requests_args(&self, taker: &str, url: Option<&str>) -> Vec<String> {
        self.premium("taker-requests", &[("--taker", taker)], url)
    }

    /// Taker: the live quotes on one request.
    pub fn premium_request_quotes_args(&self, id: &str, url: Option<&str>) -> Vec<String> {
        self.premium("request-quotes", &[("--id", id)], url)
    }

    /// Taker: cancels a request and every quote on it.
    pub fn premium_cancel_request_args(
        &self,
        id: &str,
        chain_id: i64,
        nonce: &str,
        url: Option<&str>,
    ) -> Vec<String> {
        self.premium(
            "cancel-request",
            &[
                ("--id", id),
                ("--chain_id", &chain_id.to_string()),
                ("--nonce", nonce),
            ],
            url,
        )
    }

    /// Taker: takes a quote by id; the cli signs the confirmation and prints
    /// the settlement's transaction hash.
    pub fn premium_execute_args(
        &self,
        quote_id: &str,
        nonce: &str,
        url: Option<&str>,
    ) -> Vec<String> {
        self.premium(
            "execute",
            &[("--quote_id", quote_id), ("--nonce", nonce)],
            url,
        )
    }
}

/// Turns a quote's domain into the cli's domain flags, rejecting one the cli
/// could not sign - a salt, or another chain's chain id - rather than signing
/// against the wrong domain. The websocket quote takes the domain all or
/// nothing; a premium quote has to name the pool's option handler, and the cli
/// defaults its name and version.
fn domain_args(quote: &Quote, premium: bool) -> Result<Vec<String>, Error> {
    let domain = match quote.domain.as_ref() {
        None if !premium => return Ok(Vec::new()),
        d => d,
    };
    let field =
        |f: fn(&TypedDataDomain) -> Option<&str>| domain.and_then(f).filter(|v| !v.is_empty());
    if field(|d| d.salt.as_deref()).is_some() {
        return Err(Error::Domain("quote domain: salt is not supported".into()));
    }
    if let Some(chain) = domain.and_then(|d| d.chain_id.as_ref()) {
        if chain.as_i64() != Some(quote.chain_id) {
            let shown = match chain {
                crate::models::ChainId::Number(n) => n.to_string(),
                crate::models::ChainId::Text(s) => s.clone(),
            };
            return Err(Error::Domain(format!(
                "quote domain: chainId {shown} does not match the quote's chain {}",
                quote.chain_id
            )));
        }
    }

    let name = field(|d| d.name.as_deref());
    let version = field(|d| d.version.as_deref());
    let verifying = field(|d| d.verifying_contract.as_deref());
    let required = [
        ("name", name.is_some() || premium),
        ("version", version.is_some() || premium),
        ("verifyingContract", verifying.is_some()),
    ];
    let missing: Vec<&str> = required
        .into_iter()
        .filter(|(_, ok)| !ok)
        .map(|(k, _)| k)
        .collect();
    if !missing.is_empty() {
        return Err(Error::Domain(format!(
            "quote domain: missing {}{}",
            missing.join(", "),
            if premium {
                ", pass the request's typeDataDomain"
            } else {
                ""
            }
        )));
    }

    let mut args = Vec::new();
    if let Some(name) = name {
        pair(&mut args, "--domain_name", name);
    }
    if let Some(version) = version {
        pair(&mut args, "--domain_version", version);
    }
    pair(&mut args, "--domain_verifying_contract", verifying.unwrap());
    Ok(args)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_the_versions_a_cli_reports() {
        assert_eq!(parse_version("4.0.0"), Some((4, 0, 0)));
        assert_eq!(parse_version("v4.0.0"), Some((4, 0, 0)));
        assert_eq!(parse_version(" 4.0.0\n"), Some((4, 0, 0)));
        assert_eq!(parse_version("4.0.0-rc1"), Some((4, 0, 0)));
        assert_eq!(parse_version("10.2.3"), Some((10, 2, 3)));
        // a locally built cli, which must not be compared at all
        assert_eq!(parse_version("dev"), None);
        assert_eq!(parse_version(""), None);
        assert_eq!(parse_version("4.0"), None);
    }

    #[test]
    fn version_ordering_is_numeric_not_lexical() {
        assert!(parse_version("4.10.0") > parse_version("4.9.0"));
        assert!(parse_version("4.9.9") < Some(MIN_CLI_VERSION));
        assert!(parse_version("5.3.0") >= Some(MIN_CLI_VERSION));
    }
}
