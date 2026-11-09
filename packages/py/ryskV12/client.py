import asyncio
from dataclasses import asdict
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


ENV_URLS = {
    Env.LOCAL: "ws://localhost:8000/",
    Env.TESTNET: "wss://rip-testnet.rysk.finance/",
    Env.MAINNET: "wss://v12.rysk.finance/",
}


class Rysk:
    _env: Env
    _cli_path: str
    _private_key: str
    _min_sdk_version: str = "5.1.0"

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
        self._readers = set()
        self._sdk_version_check()

    def _url(self, uri: str):
        return f"{ENV_URLS[self._env]}{uri}"
    
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
        return "ryskV12"  # the script chmods it

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
            stdin=PIPE,
            stdout=PIPE,
            stderr=PIPE,
            text=True,
            env=self._child_env(),
        )

    async def execute_async(self, args: List[str] = [], callback = print):
        """Spawns the CLI and returns the process straight away, handing each line
        of its stdout to callback in the background. `await process.wait()` for
        the exit code."""
        process = await asyncio.create_subprocess_exec(
            self._cli_path,
            *args,
            stdin=PIPE,
            stdout=PIPE,
            stderr=PIPE,
            env=self._child_env(),
        )

        async def read():
            while line := await process.stdout.readline():
                callback(line)

        # the loop only holds tasks weakly, so keep this one until it finishes
        reader = asyncio.create_task(read())
        self._readers.add(reader)
        reader.add_done_callback(self._readers.discard)
        return process

    def send(self, process, args: List[str]):
        """Writes a command - quote, transfer, balances or positions args - into
        a running connect, which signs and sends it over its websocket: no
        process per command. Takes a process from execute or execute_async.
        Disconnect with `process.terminate()`."""
        line = json.dumps(args) + "\n"
        if isinstance(process, Popen):
            process.stdin.write(line)
            process.stdin.flush()
        else:
            process.stdin.write(line.encode())

    def connect_args(self, uri: str):
        return ["connect", "--url", self._url(uri)]

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

    def balances_args(self, account: str):
        return ["balances", "--account", account]

    def transfer_args(self, transfer: Transfer):
        base = [
            "transfer",
            "--chain_id",
            str(transfer.chain_id),
            "--user",
            transfer.user,
            "--asset",
            transfer.asset,
            "--amount",
            transfer.amount,
            "--nonce",
            transfer.nonce,
        ]
        if transfer.is_deposit:
            base.append("--is_deposit")
        return base

    def positions_args(self, account: str):
        return ["positions", "--account", account]

    def will_redeem_args(
        self, chain_id: int, account: str, otoken: str, will_redeem: bool, nonce: str
    ):
        """Opts an otoken in or out of redemption at expiry. Send it into a
        running connect on the maker endpoint."""
        args = [
            "will_redeem",
            "--chain_id",
            str(chain_id),
            "--account",
            account,
            "--otoken",
            otoken,
            "--nonce",
            nonce,
        ]
        if will_redeem:
            args.append("--will_redeem")
        return args

    def self_redeem_args(
        self,
        chain_id: int,
        nonce: str,
        expiry: Optional[int] = None,
        exclude_assets: Optional[List[str]] = None,
        url: Optional[str] = None,
    ):
        """Redeems the signer's own otokens that expired at or before expiry
        (default: now). A plain http call, so run it with execute, not send.
        url defaults to this env's api."""
        if url is None:
            url = "http" + ENV_URLS[self._env][2:].rstrip("/")
        args = ["self-redeem", "--url", url, "--chain_id", str(chain_id), "--nonce", nonce]
        if expiry is not None:
            args += ["--expiry", str(expiry)]
        if exclude_assets:
            args += ["--exclude_assets", ",".join(exclude_assets)]
        return args

    def _premium(self, cmd: str, url: Optional[str], *flags: str) -> List[str]:
        """url is the premium rfq api's base. Left out, the CLI's own default
        (production) applies; pass it for a local or staging api."""
        return ["premium", cmd, *flags, *(["--url", url] if url else [])]

    def premium_requests_args(self, maker: str, url: Optional[str] = None):
        """Requests this maker may quote, each carrying the domain to sign against."""
        return self._premium("requests", url, "--maker", maker)

    def premium_quotes_args(self, maker: str, url: Optional[str] = None):
        """This maker's live quotes - the only place quote ids come from."""
        return self._premium("quotes", url, "--maker", maker)

    def premium_quote_status_args(self, id: str, url: Optional[str] = None):
        """One quote by id, whatever its status."""
        return self._premium("quote-status", url, "--id", id)

    def premium_quote_batch_args(self, source: str, url: Optional[str] = None):
        """Posts a batch of quotes in one call, reading them from source - a file
        path, or "-" for stdin. Build the file with premium_quote_batch."""
        return self._premium("quote", url, "--batch", source)

    def premium_quote_batch(self, quotes: List[Tuple[str, Quote]]) -> str:
        """Serialises (request id, quote) pairs into the json array
        premium_quote_batch_args reads, failing on a domain the CLI could not sign
        before anything is written."""
        entries = []
        for request_id, quote in quotes:
            self._domain_args(quote, True)
            entry = asdict(quote)
            entry["requestId"] = request_id
            entries.append({k: v for k, v in entry.items() if v is not None})
        return json.dumps(entries)

    def premium_cancel_args(
        self, id: str, chain_id: int, nonce: str, url: Optional[str] = None
    ):
        """Pulls one of this maker's quotes. The nonce is spent once and shares its
        keyspace with quote nonces, so draw it from the same counter."""
        return self._premium("cancel", url, "--id", id, "--chain_id", str(chain_id), "--nonce", nonce)

    def premium_pools_args(self, url: Optional[str] = None):
        """Taker: the pools requests can be opened on."""
        return self._premium("pools", url)

    def premium_request_args(
        self, source: str, chain_id: int, nonce: str, url: Optional[str] = None
    ):
        """Taker: opens the requests in source - a json array file path, or "-" for
        stdin. This key has to be an rfq signer on each request's pool."""
        return self._premium("request", url, "--batch", source, "--chain_id", str(chain_id), "--nonce", nonce)

    def premium_taker_requests_args(self, taker: str, url: Optional[str] = None):
        """Taker: a pool's live requests."""
        return self._premium("taker-requests", url, "--taker", taker)

    def premium_request_quotes_args(self, id: str, url: Optional[str] = None):
        """Taker: the live quotes on one request."""
        return self._premium("request-quotes", url, "--id", id)

    def premium_cancel_request_args(
        self, id: str, chain_id: int, nonce: str, url: Optional[str] = None
    ):
        """Taker: cancels a request and every quote on it."""
        return self._premium("cancel-request", url, "--id", id, "--chain_id", str(chain_id), "--nonce", nonce)

    def premium_execute_args(self, quote_id: str, nonce: str, url: Optional[str] = None):
        """Taker: takes a quote by id. The CLI signs the confirmation, the api
        settles it on chain, and the transaction hash is printed."""
        return self._premium("execute", url, "--quote_id", quote_id, "--nonce", nonce)

    def quote_args(self, rfq_id: str, quote: Quote):
        base = [
            "quote",
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

    def _domain_args(self, quote: Quote, premium: bool = False) -> List[str]:
        """Turns a quote's domain into the CLI's domain flags, rejecting one the
        CLI could not sign - a salt, or another chain's chainId - rather than
        signing against the wrong domain. The websocket quote takes the domain
        all or nothing; a premium quote has to name the pool's option handler,
        and the CLI defaults its name and version."""
        domain = quote.domain
        if domain is None and not premium:
            return []
        if domain is not None and domain.salt:
            raise ValueError("quote domain: salt is not supported")
        if domain is not None and domain.chainId is not None and int(str(domain.chainId), 0) != quote.chainId:
            raise ValueError(
                f"quote domain: chainId {domain.chainId} does not match the quote's chain {quote.chainId}"
            )

        required = ("verifyingContract",) if premium else ("name", "version", "verifyingContract")
        missing = [field for field in required if not getattr(domain, field, None)]
        if missing:
            hint = ", pass the request's typeDataDomain" if premium else ""
            raise ValueError(f"quote domain: missing {', '.join(missing)}{hint}")

        args: List[str] = []
        if domain.name:
            args.extend(["--domain_name", domain.name])
        if domain.version:
            args.extend(["--domain_version", domain.version])
        args.extend(["--domain_verifying_contract", domain.verifyingContract])
        return args

