import asyncio
from dataclasses import asdict, dataclass
from enum import Enum
import os
from os import path
import subprocess
from subprocess import PIPE, Popen
import sys
import json
import re
from typing import List, Optional, Tuple

from .models import Quote, Transfer


RELEASES_URL = "https://github.com/rysk-finance/ryskV12/releases"


def _parse_version(raw: str) -> Optional[Tuple[int, int, int]]:
    """Parses a leading semver out of "3.2.0", "v3.2.0" or "3.2.0-rc1"."""
    match = re.match(r"v?(\d+)\.(\d+)\.(\d+)", raw.strip())
    if match is None:
        return None
    return tuple(int(part) for part in match.groups())  # type: ignore[return-value]


class Env(Enum):
    LOCAL = 0
    TESTNET = 1
    MAINNET = 2


@dataclass(frozen=True)
class EnvConfig:
    base_url: str


ENV_CONFIGS = {
    Env.LOCAL: EnvConfig("ws://localhost:8000/"),
    Env.TESTNET: EnvConfig("wss://rip-testnet.rysk.finance/"),
    Env.MAINNET: EnvConfig("wss://v12.rysk.finance/")
}


class Rysk:
    _env: Env
    _cli_path: str
    _private_key: str
    _min_sdk_version: str = "4.0.0"

    def __init__(
        self,
        env: Env,
        private_key: str,
        v12_cli_path: str = "./ryskV12",
        strict_version: bool = False,
    ):
        """strict_version raises instead of warning when the CLI is too old."""
        self._env = env
        self._cli_path = v12_cli_path
        self._private_key = private_key
        self._strict_version = strict_version
        self._sdk_version_check()

    def _url(self, uri: str):
        return f"{ENV_CONFIGS.get(self._env).base_url}{uri}"
    
    def setup(self) -> str:
        """Downloads the CLI release into the working directory as ./ryskV12 and
        makes it executable, returning that path.

        This blocks: the SDK cannot spawn a binary that is still downloading.
        """
        script_path = path.join(
            path.dirname(path.abspath(__file__)), "scripts/fetch_latest_release.sh"
        )
        os.chmod(script_path, 0o755)

        result = subprocess.run([script_path], capture_output=True, text=True)
        if result.returncode != 0:
            raise RuntimeError(
                f"failed to download the cli: {(result.stderr or result.stdout).strip()}"
            )

        downloaded = "ryskV12"
        if not path.exists(downloaded):
            # the script reports missing dependencies on stdout and still exits 0
            raise RuntimeError(
                f"the download script left no {downloaded}: {(result.stdout or result.stderr).strip()}"
            )
        os.chmod(downloaded, 0o755)
        return downloaded

    def _sdk_version_check(self):
        try:
            result = subprocess.run(
                [self._cli_path, "version"],
                capture_output=True,
                text=True,
                check=False,
            )
        except FileNotFoundError:
            print(
                f"{self._cli_path} not found.\nDownload it here {RELEASES_URL}.",
                file=sys.stderr,
            )
            return
        except Exception as e:
            print(f"{self._cli_path} could not be run: {e}", file=sys.stderr)
            return

        stdout = result.stdout.strip()
        stderr = result.stderr.strip()

        # a cli old enough to have no version command predates every version we support
        if "No help topic for 'version'" in stderr or not stdout:
            self._report_version("too old to report a version")
            return

        found = _parse_version(stdout)
        if found is None:
            # a locally built cli reports something like "dev"; nothing to compare
            return
        if found < _parse_version(self._min_sdk_version):
            self._report_version(stdout)

    def _report_version(self, found: str):
        message = (
            f"{self._cli_path} is {found}, but this sdk needs {self._min_sdk_version} "
            f"or newer. Commands added since {found} will fail with "
            f'"flag provided but not defined".\nDownload it here {RELEASES_URL}.'
        )
        if self._strict_version:
            raise RuntimeError(message)
        print(message, file=sys.stderr)

    def _child_env(self):
        """The private key is handed to the CLI through RYSK_PRIVATE_KEY rather
        than as an argument, so it never shows up in ps or a shell history.
        Spawning the CLI yourself means setting that variable too."""
        return {**os.environ, "RYSK_PRIVATE_KEY": self._private_key}

    def execute(self, args: List[str] = []):
        return Popen(
            [self._cli_path, *args],
            shell=False,
            stdout=PIPE,
            stderr=PIPE,
            text=True,
            env=self._child_env(),
        )

    async def execute_async(self, args: List[str] = [], callback = print):
        process = await asyncio.create_subprocess_exec(
            self._cli_path,
            *args,
            stdout=PIPE,
            stderr=PIPE,
            env=self._child_env(),
        )
        while True:
            line = await process.stdout.readline()
            if not line:
                break
            callback(line)  
        return await process.wait()

    def connect_args(self, channel_id: str, uri: str):
        return ["connect", "--channel_id", channel_id, "--url", self._url(uri)]

    def disconnect_args(self, channel_id: str):
        return ["disconnect", "--channel_id", channel_id]

    def approve_args(
        self, chain_id: int, amount: str, rpc_url: str, asset: Optional[str] = None
    ):
        """asset is the erc20 to approve. Left out, the CLI falls back to the
        chain's strike asset, which is what every caller got before this was
        accepted."""
        base = [
            "approve",
            "--chain_id",
            str(chain_id),
            "--amount",
            amount,
            "--rpc_url",
            rpc_url,
        ]
        if asset:
            base += ["--asset", asset]
        return base

    def balances_args(self, channel_id: str, account: str):
        return ["balances", "--channel_id", channel_id, "--account", account]

    def transfer_args(self, channel_id: str, transfer: Transfer):
        base = [
            "transfer",
            "--channel_id",
            channel_id,
            "--chain_id",
            str(transfer.chain_id),
            "--user",
            transfer.user,
            "--asset",
            transfer.asset,
            "--amount",
            transfer.amout,
            "--nonce",
            transfer.nonce,
        ]
        if transfer.is_deposit:
            base.append("--is_deposit")
        return base

    def positions_args(self, channel_id: str, account: str):
        return ["positions", "--channel_id", channel_id, "--account", account]

    def _premium_url_args(self, url: Optional[str]) -> List[str]:
        """Base url of the premium rfq api. Left out of the args when not given,
        so the CLI's own default (production) applies; pass it for a local or
        staging api."""
        return ["--url", url] if url else []

    def _premium_domain_args(self, quote: Quote) -> List[str]:
        """The premium api signs against the pool's option handler, which arrives
        on the request as typeDataDomain. Only the verifying contract is required
        - the CLI defaults the name and version - but a domain it cannot sign is
        rejected here rather than passed on."""
        domain = quote.domain
        if domain is None or not domain.verifyingContract:
            raise ValueError(
                "premium quote domain: missing verifyingContract, pass the request's typeDataDomain"
            )
        if domain.salt:
            raise ValueError("premium quote domain: salt is not supported")
        if domain.chainId is not None and int(str(domain.chainId), 0) != quote.chainId:
            raise ValueError(
                f"premium quote domain: chainId {domain.chainId} does not match the quote's chain {quote.chainId}"
            )

        args: List[str] = []
        if domain.name:
            args.extend(["--domain_name", domain.name])
        if domain.version:
            args.extend(["--domain_version", domain.version])
        args.extend(["--domain_verifying_contract", domain.verifyingContract])
        return args

    def premium_requests_args(self, maker: str, url: Optional[str] = None):
        """Requests this maker may quote, each carrying the domain to sign against."""
        return ["premium", "requests", "--maker", maker, *self._premium_url_args(url)]

    def premium_quotes_args(self, maker: str, url: Optional[str] = None):
        """This maker's live quotes - the only place quote ids come from."""
        return ["premium", "quotes", "--maker", maker, *self._premium_url_args(url)]

    def premium_quote_status_args(self, id: str, url: Optional[str] = None):
        """One quote by id, whatever its status."""
        return ["premium", "quote-status", "--id", id, *self._premium_url_args(url)]

    def premium_quote_args(self, request_id: str, quote: Quote, url: Optional[str] = None):
        """Signs and posts one quote. The terms have to be the request's - the api
        rebuilds the signed message from the stored request - and validUntil is in
        seconds, strictly between now+2min and now+10min."""
        base = [
            "premium",
            "quote",
            "--request_id",
            request_id,
            "--asset",
            quote.assetAddress,
            "--chain_id",
            str(quote.chainId),
            "--expiry",
            str(quote.expiry),
            "--strike",
            quote.strike,
            "--quantity",
            quote.quantity,
            "--usd",
            quote.usd,
            "--collateral",
            quote.collateralAsset,
            "--maker",
            quote.maker,
            "--nonce",
            quote.nonce,
            "--price",
            quote.price,
            "--valid_until",
            str(quote.validUntil),
            *self._premium_domain_args(quote),
            *self._premium_url_args(url),
        ]
        if quote.isPut:
            base.append("--is_put")
        if quote.isTakerBuy:
            base.append("--is_taker_buy")
        return base

    def premium_quote_batch_args(self, source: str, url: Optional[str] = None):
        """Posts a batch of quotes in one call, reading them from source - a file
        path, or "-" for stdin. Build the file with premium_quote_batch."""
        return [
            "premium",
            "quote",
            "--batch",
            source,
            *self._premium_url_args(url),
        ]

    def premium_quote_batch(self, quotes: List[Tuple[str, Quote]]) -> str:
        """Serialises (request id, quote) pairs into the json array
        premium_quote_batch_args reads, failing on a domain the CLI could not sign
        before anything is written."""
        entries = []
        for request_id, quote in quotes:
            self._premium_domain_args(quote)
            entry = asdict(quote)
            entry["requestId"] = request_id
            entries.append({k: v for k, v in entry.items() if v is not None})
        return json.dumps(entries)

    def premium_cancel_args(
        self, id: str, chain_id: int, nonce: str, url: Optional[str] = None
    ):
        """Pulls one of this maker's quotes. The nonce is spent once and shares its
        keyspace with quote nonces, so draw it from the same counter."""
        return [
            "premium",
            "cancel",
            "--id",
            id,
            "--chain_id",
            str(chain_id),
            "--nonce",
            nonce,
            *self._premium_url_args(url),
        ]

    def quote_args(self, channel_id: str, rfq_id: str, quote: Quote):
        base = [
            "quote",
            "--channel_id",
            channel_id,
            "--rfq_id",
            rfq_id,
            "--asset",
            quote.assetAddress,
            "--chain_id",
            str(quote.chainId),
            "--expiry",
            f"{quote.expiry}",
            "--maker",
            quote.maker,
            "--nonce",
            quote.nonce,
            "--price",
            quote.price,
            "--quantity",
            quote.quantity,
            "--strike",
            quote.strike,
            "--valid_until",
            str(quote.validUntil),
            "--usd",
            quote.usd,
            "--collateral",
            quote.collateralAsset,
        ]
        if quote.premiumAsset:
            base.extend(["--premium_asset", quote.premiumAsset])
        base.extend(self._domain_args(quote))
        if quote.isPut:
            base.append("--is_put")
        if quote.isTakerBuy:
            base.append("--is_taker_buy")
        return base

    def _domain_args(self, quote: Quote) -> List[str]:
        """Turns a request's domain into the CLI's domain flags. The CLI takes
        the domain's chainId from the quote, so a domain for another chain, or
        one using a salt, cannot be signed and is rejected here rather than
        silently signed against the wrong domain."""
        domain = quote.domain
        if domain is None:
            return []

        if domain.salt:
            raise ValueError("quote domain: salt is not supported")
        if domain.chainId is not None and int(str(domain.chainId), 0) != quote.chainId:
            raise ValueError(
                f"quote domain: chainId {domain.chainId} does not match the quote's chain {quote.chainId}"
            )

        missing = [
            field
            for field in ("name", "version", "verifyingContract")
            if not getattr(domain, field)
        ]
        if missing:
            raise ValueError(f"quote domain: missing {', '.join(missing)}")

        return [
            "--domain_name",
            domain.name,
            "--domain_version",
            domain.version,
            "--domain_verifying_contract",
            domain.verifyingContract,
        ]

