import { ChildProcess, exec, spawn } from "child_process";
import { EventEmitter } from "events";
import { createInterface } from "readline";

import { HexString, Quote, Transfer } from "./models.js";

class CliWebSocket extends EventEmitter {
  private _closed = false;

  constructor(private child: ChildProcess) {
    super();

    // The CLI prints one message per line, and a chunk of stdout can hold
    // several or part of one, so each line is a message.
    if (this.child.stdout) {
      createInterface({ input: this.child.stdout }).on("line", (line) =>
        this.emit("message", Buffer.from(line)),
      );
    }

    // The CLI logs to stderr, so it is not an error channel: a failed command
    // is a json-rpc error message (or, run on its own, a non-zero exit code),
    // and "error" is reserved for the process itself failing.
    this.child.stderr?.on("data", (data: Buffer) => {
      this.emit("stderr", data);
    });

    // Handle process close
    this.child.on("close", (code: number, _: string) => {
      this._closed = true;
      this.emit("close", code, "Process exited"); // Mimicking close event with code and reason
    });

    // Handle process error (e.g., spawn failure)
    this.child.on("error", (err: Error) => {
      this._closed = true;
      this.emit("error", err.toString());
      this.emit("close");
    });

    // deferred, so a listener attached right after execute() still hears it
    process.nextTick(() => this.emit("open"));
  }

  /** Kills the CLI process, which disconnects a connect. */
  public close(): void {
    if (!this._closed) {
      this.child.kill("SIGTERM");
    }
  }

  public send(args: Array<string>): void {
    this.child.stdin?.write(JSON.stringify(args) + "\n");
  }
}

export enum Env {
  LOCAL = 0,
  TESTNET = 1,
  MAINNET = 2,
}

const BASE_URLS: Record<Env, string> = {
  [Env.LOCAL]: "ws://localhost:8000/",
  [Env.TESTNET]: "wss://rip-testnet.rysk.finance/",
  [Env.MAINNET]: "wss://v12.rysk.finance/",
};

const RELEASES_URL = "https://github.com/rysk-finance/sdks/releases";

type Version = [number, number, number];

/** Parses a leading semver out of `3.2.0`, `v3.2.0` or `3.2.0-rc1`. */
function parseVersion(raw: string): Version | null {
  const match = raw.trim().match(/^v?(\d+)\.(\d+)\.(\d+)/);
  return match ? [Number(match[1]), Number(match[2]), Number(match[3])] : null;
}

/** Compares major, then minor, then patch - not just the first digit. */
function compareVersions(left: Version, right: Version) {
  return left[0] - right[0] || left[1] - right[1] || left[2] - right[2];
}

class Rysk {
  private _env: Env;
  private _cli_path: string;
  private _private_key: string;
  private _minSdkVersion: string = "5.1.0";
  private _strictVersion: boolean;

  /**
   * @param strictVersion throw instead of warning when the CLI is too old.
   */
  constructor(
    env: Env,
    privateKey: string,
    v12CliPath: string = "./ryskV12",
    strictVersion: boolean = false,
  ) {
    this._env = env;
    this._cli_path = v12CliPath;
    this._private_key = privateKey;
    this._strictVersion = strictVersion;
    this._sdkVersionCheck();
  }

  private _sdkVersionCheck() {
    exec([this._cli_path, "version"].join(" "), (error, stdout, stderr) => {
      if (error) {
        console.error(
          `${this._cli_path} could not be run: ${error.message}\nDownload it here ${RELEASES_URL}.`,
        );
        return;
      }

      // a cli old enough to have no version command predates every version we
      // support
      if (stderr.includes("No help topic for 'version'") || !stdout.trim()) {
        this._reportVersion("too old to report a version");
        return;
      }

      const found = parseVersion(stdout);
      if (!found) {
        // a locally built cli reports something like "dev"; nothing to compare
        return;
      }
      if (compareVersions(found, parseVersion(this._minSdkVersion)!) < 0) {
        this._reportVersion(stdout.trim());
      }
    });
  }

  private _reportVersion(found: string) {
    const message =
      `${this._cli_path} is ${found}, but this sdk needs ${this._minSdkVersion} or newer. ` +
      `Commands added since ${found} will fail with "flag provided but not defined".\n` +
      `Download it here ${RELEASES_URL}.`;
    if (this._strictVersion) {
      throw new Error(message);
    }
    console.error(message);
  }

  private _url(uri: string): string {
    return `${BASE_URLS[this._env]}${uri}`;
  }

  /**
   * Spawns the CLI. The private key is handed over through RYSK_PRIVATE_KEY in
   * the child's environment rather than as an argument, so it never shows up in
   * `ps` or a shell history. Spawning the CLI yourself means setting that
   * variable too.
   */
  public execute(args: Array<string> = []) {
    const childProcess = spawn(this._cli_path, args, {
      stdio: ["pipe", "pipe", "pipe"],
      env: { ...process.env, RYSK_PRIVATE_KEY: this._private_key },
    });
    return new CliWebSocket(childProcess);
  }

  /**
   * Holds a websocket open. Its stdout carries the server's messages; send it
   * the balances, positions, transfer and quote args with `send`, and end it
   * with `close`.
   */
  public connectArgs(uri: string) {
    return ["connect", "--url", this._url(uri)];
  }

  /**
   * @param asset erc20 to approve. Left out, the cli falls back to the chain's
   * strike asset, which is what every caller got before this was accepted.
   */
  public approveArgs(
    chainId: number,
    amount: string,
    rpcURL: string,
    asset?: HexString,
  ) {
    return [
      "approve",
      "--chain_id",
      chainId.toString(),
      "--amount",
      amount,
      "--rpc_url",
      rpcURL,
      ...(asset ? ["--asset", asset] : []),
    ];
  }

  public balancesArgs(account: string) {
    return ["balances", "--account", account];
  }

  public transferArgs(transfer: Transfer) {
    return [
      "transfer",
      "--chain_id",
      transfer.chain_id.toString(),
      "--user",
      transfer.user,
      "--asset",
      transfer.asset,
      "--amount",
      transfer.amount,
      "--nonce",
      transfer.nonce,
      ...(transfer.is_deposit ? ["--is_deposit"] : []),
    ];
  }

  public positionsArgs(account: string) {
    return ["positions", "--account", account];
  }

  /**
   * Opts an otoken in or out of redemption at expiry. Send it into a running
   * `connect` on the maker endpoint.
   */
  public willRedeemArgs(
    chainId: number,
    account: string,
    otoken: HexString,
    willRedeem: boolean,
    nonce: string,
  ) {
    return [
      "will_redeem",
      "--chain_id",
      chainId.toString(),
      "--account",
      account,
      "--otoken",
      otoken,
      "--nonce",
      nonce,
      ...(willRedeem ? ["--will_redeem"] : []),
    ];
  }

  /**
   * Redeems the signer's own otokens that expired at or before `expiry`
   * (default: now). A plain http call, so run it with `execute`, not `send`.
   * `url` defaults to this env's api.
   */
  public selfRedeemArgs(
    chainId: number,
    nonce: string,
    expiry?: number,
    excludeAssets: Array<HexString> = [],
    url?: string,
  ) {
    return [
      "self-redeem",
      "--url",
      url ?? BASE_URLS[this._env].replace(/^ws/, "http").replace(/\/$/, ""),
      "--chain_id",
      chainId.toString(),
      "--nonce",
      nonce,
      ...(expiry !== undefined ? ["--expiry", expiry.toString()] : []),
      ...(excludeAssets.length ? ["--exclude_assets", excludeAssets.join(",")] : []),
    ];
  }

  /**
   * Turns a quote's domain into the CLI's domain flags, rejecting one the CLI
   * could not sign - a salt, or another chain's chainId - rather than signing
   * against the wrong domain. The websocket quote takes the domain all or
   * nothing; a premium quote has to name the pool's option handler, and the CLI
   * defaults its name and version.
   */
  private _domainArgs(quote: Quote, premium = false): Array<string> {
    const domain = quote.domain;
    if (!domain && !premium) {
      return [];
    }
    if (domain?.salt) {
      throw new Error("quote domain: salt is not supported");
    }
    if (domain?.chainId !== undefined && domain?.chainId !== null) {
      if (Number(domain!.chainId) !== quote.chainId) {
        throw new Error(
          `quote domain: chainId ${domain!.chainId} does not match the quote's chain ${quote.chainId}`,
        );
      }
    }

    const required = premium
      ? (["verifyingContract"] as const)
      : (["name", "version", "verifyingContract"] as const);
    const missing = required.filter((field) => !domain?.[field]);
    if (missing.length) {
      throw new Error(
        `quote domain: missing ${missing.join(", ")}${premium ? ", pass the request's typeDataDomain" : ""}`,
      );
    }

    return [
      ...(domain!.name ? ["--domain_name", domain!.name] : []),
      ...(domain!.version ? ["--domain_version", domain!.version] : []),
      "--domain_verifying_contract",
      domain!.verifyingContract!,
    ];
  }

  /**
   * Base url of the premium rfq api. Left out of the args when not given, so the
   * CLI's own default (production) applies; pass it for a local or staging api.
   */
  private _premium(cmd: string, url: string | undefined, ...flags: string[]) {
    return ["premium", cmd, ...flags, ...(url ? ["--url", url] : [])];
  }

  /** Requests this maker may quote, each carrying the domain to sign against. */
  public premiumRequestsArgs(maker: string, url?: string) {
    return this._premium("requests", url, "--maker", maker);
  }

  /** This maker's live quotes - the only place quote ids come from. */
  public premiumQuotesArgs(maker: string, url?: string) {
    return this._premium("quotes", url, "--maker", maker);
  }

  /** One quote by id, whatever its status. */
  public premiumQuoteStatusArgs(id: string, url?: string) {
    return this._premium("quote-status", url, "--id", id);
  }

  /**
   * Posts a batch of quotes in one call, reading them from `source` - a file
   * path, or "-" for stdin. Build the file with `premiumQuoteBatch`.
   */
  public premiumQuoteBatchArgs(source: string, url?: string) {
    return this._premium("quote", url, "--batch", source);
  }

  /**
   * Serialises quotes into the json array `premiumQuoteBatchArgs` reads, failing
   * on a domain the CLI could not sign before anything is written.
   */
  public premiumQuoteBatch(quotes: Array<{ requestId: string; quote: Quote }>) {
    return JSON.stringify(
      quotes.map(({ requestId, quote }) => {
        this._domainArgs(quote, true);
        return { ...quote, requestId };
      }),
    );
  }

  /**
   * Pulls one of this maker's quotes. The nonce is spent once and shares its
   * keyspace with quote nonces, so draw it from the same counter.
   */
  public premiumCancelArgs(
    id: string,
    chainId: number,
    nonce: string,
    url?: string,
  ) {
    return this._premium("cancel", url, "--id", id, "--chain_id", chainId.toString(), "--nonce", nonce);
  }

  /** Taker: the pools requests can be opened on. */
  public premiumPoolsArgs(url?: string) {
    return this._premium("pools", url);
  }

  /**
   * Taker: opens the requests in `source` - a json array as the api takes them,
   * as a file path or "-" for stdin. This key has to be an rfq signer on each
   * request's pool (its `taker`); the nonce authenticates the post.
   */
  public premiumRequestArgs(
    source: string,
    chainId: number,
    nonce: string,
    url?: string,
  ) {
    return this._premium("request", url, "--batch", source, "--chain_id", chainId.toString(), "--nonce", nonce);
  }

  /** Taker: a pool's live requests. */
  public premiumTakerRequestsArgs(taker: string, url?: string) {
    return this._premium("taker-requests", url, "--taker", taker);
  }

  /** Taker: the live quotes on one request. */
  public premiumRequestQuotesArgs(id: string, url?: string) {
    return this._premium("request-quotes", url, "--id", id);
  }

  /** Taker: cancels a request and every quote on it. */
  public premiumCancelRequestArgs(
    id: string,
    chainId: number,
    nonce: string,
    url?: string,
  ) {
    return this._premium("cancel-request", url, "--id", id, "--chain_id", chainId.toString(), "--nonce", nonce);
  }

  /**
   * Taker: takes a quote by id. The CLI signs the confirmation and the api
   * settles it on chain; it prints the transaction hash.
   */
  public premiumExecuteArgs(quoteId: string, nonce: string, url?: string) {
    return this._premium("execute", url, "--quote_id", quoteId, "--nonce", nonce);
  }

  public quoteArgs(rfqId: string, quote: Quote) {
    return [
      "quote",
      "--rfq_id",
      rfqId,
      "--asset",
      quote.assetAddress,
      "--chain_id",
      quote.chainId.toString(),
      "--expiry",
      quote.expiry.toString(),
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
      quote.validUntil.toString(),
      "--usd",
      quote.usd,
      "--collateral",
      quote.collateralAsset,
      ...(quote.premiumAsset ? ["--premium_asset", quote.premiumAsset] : []),
      ...this._domainArgs(quote),
      ...(quote.isPut ? ["--is_put"] : []),
      ...(quote.isTakerBuy ? ["--is_taker_buy"] : []),
    ];
  }
}

export { NonceCounter } from "./nonce.js";

export default Rysk;
