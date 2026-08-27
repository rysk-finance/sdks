use std::io::{BufRead, BufReader};
use std::process::{Child, Command, Stdio};

use crate::error::Error;
use crate::models::{Quote, Transfer, TypedDataDomain};

pub const RELEASES_URL: &str = "https://github.com/rysk-finance/sdks/releases";

/// Oldest cli this sdk will not warn about. Kept in step with the other sdks'
/// `_minSdkVersion` / `_min_sdk_version`.
const MIN_CLI_VERSION: (u64, u64, u64) = (4, 0, 0);

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
pub struct RyskBuilder {
    env: Env,
    cli_path: String,
    private_key: String,
    strict_version: bool,
}

impl RyskBuilder {
    /// Where the cli binary lives. Defaults to `./ryskV12`.
    pub fn cli_path(mut self, path: impl Into<String>) -> Self {
        self.cli_path = path.into();
        self
    }

    /// Fail instead of warning when the cli is older than this sdk needs.
    pub fn strict_version(mut self, strict: bool) -> Self {
        self.strict_version = strict;
        self
    }

    /// Checks the cli version, then hands back the client.
    pub fn build(self) -> Result<Rysk, Error> {
        let rysk = Rysk {
            env: self.env,
            cli_path: self.cli_path,
            private_key: self.private_key,
            strict_version: self.strict_version,
        };
        rysk.version_check()?;
        Ok(rysk)
    }
}

impl Rysk {
    /// A client against `./ryskV12`, warning rather than failing on an old cli.
    pub fn new(env: Env, private_key: impl Into<String>) -> Result<Self, Error> {
        Self::builder(env, private_key).build()
    }

    pub fn builder(env: Env, private_key: impl Into<String>) -> RyskBuilder {
        RyskBuilder {
            env,
            cli_path: DEFAULT_CLI_PATH.to_string(),
            private_key: private_key.into(),
            strict_version: false,
        }
    }

    pub fn cli_path(&self) -> &str {
        &self.cli_path
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
        use std::io::Write;

        // The script ships inside the crate, so it is written out and run rather
        // than shelled out to from a path that only exists in a repo checkout.
        let mut child = Command::new("sh")
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

    pub fn connect_args(&self, channel_id: &str, uri: &str) -> Vec<String> {
        vec![
            "connect".into(),
            "--channel_id".into(),
            channel_id.into(),
            "--url".into(),
            self.url(uri),
        ]
    }

    pub fn disconnect_args(&self, channel_id: &str) -> Vec<String> {
        vec![
            "disconnect".into(),
            "--channel_id".into(),
            channel_id.into(),
        ]
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

    pub fn balances_args(&self, channel_id: &str, account: &str) -> Vec<String> {
        vec![
            "balances".into(),
            "--channel_id".into(),
            channel_id.into(),
            "--account".into(),
            account.into(),
        ]
    }

    pub fn positions_args(&self, channel_id: &str, account: &str) -> Vec<String> {
        vec![
            "positions".into(),
            "--channel_id".into(),
            channel_id.into(),
            "--account".into(),
            account.into(),
        ]
    }

    pub fn transfer_args(&self, channel_id: &str, transfer: &Transfer) -> Vec<String> {
        let mut args = vec!["transfer".to_string()];
        pair(&mut args, "--channel_id", channel_id);
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

    pub fn quote_args(
        &self,
        channel_id: &str,
        rfq_id: &str,
        quote: &Quote,
    ) -> Result<Vec<String>, Error> {
        let mut args = vec!["quote".to_string()];
        pair(&mut args, "--channel_id", channel_id);
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
        args.extend(self.domain_args(quote)?);
        if quote.is_put {
            args.push("--is_put".into());
        }
        if quote.is_taker_buy {
            args.push("--is_taker_buy".into());
        }
        Ok(args)
    }

    /// Turns a request's domain into the cli's domain flags. The cli takes the
    /// domain's chain id from the quote, so a domain for another chain, or one
    /// using a salt, cannot be signed and is rejected here rather than silently
    /// signed against the wrong domain.
    fn domain_args(&self, quote: &Quote) -> Result<Vec<String>, Error> {
        let Some(domain) = quote.domain.as_ref() else {
            return Ok(Vec::new());
        };
        check_shared_domain(domain, quote.chain_id, "quote domain")?;

        let missing: Vec<&str> = [
            ("name", domain.name.as_deref()),
            ("version", domain.version.as_deref()),
            ("verifyingContract", domain.verifying_contract.as_deref()),
        ]
        .into_iter()
        .filter(|(_, v)| v.map_or(true, str::is_empty))
        .map(|(k, _)| k)
        .collect();
        if !missing.is_empty() {
            return Err(Error::Domain(format!(
                "quote domain: missing {}",
                missing.join(", ")
            )));
        }

        let mut args = Vec::new();
        pair(&mut args, "--domain_name", domain.name.as_deref().unwrap());
        pair(
            &mut args,
            "--domain_version",
            domain.version.as_deref().unwrap(),
        );
        pair(
            &mut args,
            "--domain_verifying_contract",
            domain.verifying_contract.as_deref().unwrap(),
        );
        Ok(args)
    }

    // ------------------------------------------------------------ premium rfq

    /// Base url of the premium rfq api. Left out of the args when not given, so
    /// the cli's own default (production) applies; pass it for a local or
    /// staging api.
    fn premium_url_args(&self, url: Option<&str>) -> Vec<String> {
        match url.filter(|u| !u.is_empty()) {
            Some(url) => vec!["--url".to_string(), url.to_string()],
            None => Vec::new(),
        }
    }

    /// The premium api signs against the pool's option handler, which arrives on
    /// the request as `typeDataDomain`. Only the verifying contract is
    /// required (the cli defaults the name and version), but a domain it
    /// cannot sign is rejected here rather than passed on.
    fn premium_domain_args(&self, quote: &Quote) -> Result<Vec<String>, Error> {
        let verifying = quote
            .domain
            .as_ref()
            .and_then(|d| d.verifying_contract.as_deref())
            .filter(|v| !v.is_empty())
            .ok_or_else(|| {
                Error::Domain(
                    "premium quote domain: missing verifyingContract, pass the request's \
                     typeDataDomain"
                        .to_string(),
                )
            })?;
        let domain = quote.domain.as_ref().expect("checked above");
        check_shared_domain(domain, quote.chain_id, "premium quote domain")?;

        let mut args = Vec::new();
        if let Some(name) = domain.name.as_deref().filter(|n| !n.is_empty()) {
            pair(&mut args, "--domain_name", name);
        }
        if let Some(version) = domain.version.as_deref().filter(|v| !v.is_empty()) {
            pair(&mut args, "--domain_version", version);
        }
        pair(&mut args, "--domain_verifying_contract", verifying);
        Ok(args)
    }

    /// Requests this maker may quote, each carrying the domain to sign against.
    pub fn premium_requests_args(&self, maker: &str, url: Option<&str>) -> Vec<String> {
        let mut args = vec!["premium".to_string(), "requests".to_string()];
        pair(&mut args, "--maker", maker);
        args.extend(self.premium_url_args(url));
        args
    }

    /// This maker's live quotes: the only place quote ids come from.
    pub fn premium_quotes_args(&self, maker: &str, url: Option<&str>) -> Vec<String> {
        let mut args = vec!["premium".to_string(), "quotes".to_string()];
        pair(&mut args, "--maker", maker);
        args.extend(self.premium_url_args(url));
        args
    }

    /// One quote by id, whatever its status.
    pub fn premium_quote_status_args(&self, id: &str, url: Option<&str>) -> Vec<String> {
        let mut args = vec!["premium".to_string(), "quote-status".to_string()];
        pair(&mut args, "--id", id);
        args.extend(self.premium_url_args(url));
        args
    }

    /// Signs and posts one quote. The terms have to be the request's - the api
    /// rebuilds the signed message from the stored request, and `valid_until`
    /// is in seconds, strictly between now+2min and now+10min.
    pub fn premium_quote_args(
        &self,
        request_id: &str,
        quote: &Quote,
        url: Option<&str>,
    ) -> Result<Vec<String>, Error> {
        let mut args = vec!["premium".to_string(), "quote".to_string()];
        pair(&mut args, "--request_id", request_id);
        pair(&mut args, "--asset", &quote.asset_address);
        pair(&mut args, "--chain_id", quote.chain_id.to_string());
        pair(&mut args, "--expiry", quote.expiry.to_string());
        pair(&mut args, "--strike", &quote.strike);
        pair(&mut args, "--quantity", &quote.quantity);
        pair(&mut args, "--usd", &quote.usd);
        pair(&mut args, "--collateral", &quote.collateral_asset);
        pair(&mut args, "--maker", &quote.maker);
        pair(&mut args, "--nonce", &quote.nonce);
        pair(&mut args, "--price", &quote.price);
        pair(&mut args, "--valid_until", quote.valid_until.to_string());
        args.extend(self.premium_domain_args(quote)?);
        args.extend(self.premium_url_args(url));
        if quote.is_put {
            args.push("--is_put".into());
        }
        if quote.is_taker_buy {
            args.push("--is_taker_buy".into());
        }
        Ok(args)
    }

    /// Posts a batch of quotes in one call, reading them from `source`: a file
    /// path, or `-` for stdin. Build the file with [`Rysk::premium_quote_batch`].
    pub fn premium_quote_batch_args(&self, source: &str, url: Option<&str>) -> Vec<String> {
        let mut args = vec!["premium".to_string(), "quote".to_string()];
        pair(&mut args, "--batch", source);
        args.extend(self.premium_url_args(url));
        args
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
            self.premium_domain_args(quote)?;
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
        let mut args = vec!["premium".to_string(), "cancel".to_string()];
        pair(&mut args, "--id", id);
        pair(&mut args, "--chain_id", chain_id.to_string());
        pair(&mut args, "--nonce", nonce);
        args.extend(self.premium_url_args(url));
        args
    }
}

/// The two rejections both domains share: a salt cannot be signed, and a chain
/// that is not the quote's would be signed against the wrong domain.
fn check_shared_domain(
    domain: &TypedDataDomain,
    quote_chain_id: i64,
    what: &str,
) -> Result<(), Error> {
    if domain.salt.as_deref().is_some_and(|s| !s.is_empty()) {
        return Err(Error::Domain(format!("{what}: salt is not supported")));
    }
    if let Some(chain) = domain.chain_id.as_ref() {
        if chain.as_i64() != Some(quote_chain_id) {
            let shown = match chain {
                crate::models::ChainId::Number(n) => n.to_string(),
                crate::models::ChainId::Text(s) => s.clone(),
            };
            return Err(Error::Domain(format!(
                "{what}: chainId {shown} does not match the quote's chain {quote_chain_id}"
            )));
        }
    }
    Ok(())
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
        assert!(parse_version("3.2.0") < Some(MIN_CLI_VERSION));
        assert!(parse_version("4.0.0") >= Some(MIN_CLI_VERSION));
    }
}
