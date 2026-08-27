import { ChildProcess, exec, spawn } from "child_process";
import { EventEmitter } from "events";
import Stream from "stream";

import { HexString, Quote, Transfer } from "./models";

enum ReadyState {
  CONNECTING = 0,
  OPEN = 1,
  CLOSING = 2,
  CLOSED = 3,
}
class CliWebSocket extends EventEmitter {
  private _childProcess: ChildProcess | null = null;
  public readyState: ReadyState = ReadyState.CONNECTING;
  public stdout: Stream.Readable | null;
  public stderr: Stream.Readable | null;

  // Constructor is private to enforce static factory method for clarity
  constructor(childProcess: ChildProcess) {
    super();
    this._childProcess = childProcess;
    this.readyState = ReadyState.OPEN; // The process is spawned, so it's "open" from this perspective
    this.stdout = this._childProcess.stdout;
    this.stderr = this._childProcess.stderr;

    // Handle stdout data as "messages"
    this._childProcess?.stdout?.on("data", (data: Buffer) => {
      this.emit("message", data); // Assuming text messages
    });

    // The CLI logs to stderr, so it is not an error channel: a failed command is
    // a non-zero exit code, and "error" is reserved for the process itself
    // failing.
    this._childProcess?.stderr?.on("data", (data: Buffer) => {
      this.emit("stderr", data);
    });

    // Handle process close
    this._childProcess.on("close", (code: number, _: string) => {
      this.readyState = ReadyState.CLOSED;
      this.emit("close", code, "Process exited"); // Mimicking close event with code and reason
    });

    // Handle process error (e.g., spawn failure)
    this._childProcess.on("error", (err: Error) => {
      this.readyState = ReadyState.CLOSED;
      this.emit("error", err.toString());
      this.emit("close");
    });

    this.emit("open");
  }

  /**
   * Closes the CLI process.
   * @param _code Optional close code.
   * @param reason Optional reason for closing.
   */
  public close(_code?: number, _reason?: string): void {
    if (
      this.readyState === ReadyState.CLOSING ||
      this.readyState === ReadyState.CLOSED
    ) {
      return;
    }
    this.readyState = ReadyState.CLOSING;
    this._childProcess?.kill("SIGTERM"); // Send termination signal
    // The 'close' event listener will handle the state change to CLOSED
  }

  // Mimic WebSocket event handlers for convenience, though EventEmitter is primary
  public onopen?: () => void;
  public onmessage?: (msg: Buffer) => void;
  public onclose?: () => void;
  public onerror?: (event: Buffer) => void;
  public onstderr?: (msg: Buffer) => void;

  // Override emit to trigger on* handlers
  public emit(eventName: string | symbol, ...args: any[]): boolean {
    if (eventName === "open" && this.onopen) {
      this.onopen();
    } else if (eventName === "message" && this.onmessage) {
      this.onmessage(args[0]);
    } else if (eventName === "close" && this.onclose) {
      this.onclose();
    } else if (eventName === "error" && this.onerror) {
      this.onerror(args[0]);
    } else if (eventName === "stderr" && this.onstderr) {
      this.onstderr(args[0]);
    }
    return super.emit(eventName, ...args);
  }
}

export enum Env {
  LOCAL = 0,
  TESTNET = 1,
  MAINNET = 2,
}

interface EnvConfigPayload {
  base_url: string;
}

class EnvConfig implements EnvConfigPayload {
  public readonly base_url: string;

  constructor(base_url: string) {
    this.base_url = base_url;
  }
}

const ENV_CONFIGS: { [key in Env]: EnvConfig } = {
  [Env.LOCAL]: new EnvConfig("ws://localhost:8000/"),
  [Env.TESTNET]: new EnvConfig("wss://rip-testnet.rysk.finance/"),
  [Env.MAINNET]: new EnvConfig("wss://v12.rysk.finance/"),
};

const RELEASES_URL = "https://github.com/rysk-finance/ryskV12/releases";

type Version = [number, number, number];

/** Parses a leading semver out of `3.2.0`, `v3.2.0` or `3.2.0-rc1`. */
function parseVersion(raw: string): Version | null {
  const match = raw.trim().match(/^v?(\d+)\.(\d+)\.(\d+)/);
  return match ? [Number(match[1]), Number(match[2]), Number(match[3])] : null;
}

/** Compares major, then minor, then patch - not just the first digit. */
function compareVersions(left: Version, right: Version) {
  const [leftMajor, leftMinor, leftPatch] = left;
  const [rightMajor, rightMinor, rightPatch] = right;

  if (leftMajor !== rightMajor) return leftMajor < rightMajor ? -1 : 1;
  if (leftMinor !== rightMinor) return leftMinor < rightMinor ? -1 : 1;
  if (leftPatch !== rightPatch) return leftPatch < rightPatch ? -1 : 1;
  return 0;
}

class Rysk {
  private _env: Env;
  private _cli_path: string;
  private _private_key: string;
  private _minSdkVersion: string = "4.0.0";
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
    return `${ENV_CONFIGS[this._env].base_url}${uri}`;
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

  public connectArgs(channelId: string, uri: string) {
    return ["connect", "--channel_id", channelId, "--url", this._url(uri)];
  }

  public disconnectArgs(channelId: string) {
    return ["disconnect", "--channel_id", channelId];
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

  public balancesArgs(channelId: string, account: string) {
    return ["balances", "--channel_id", channelId, "--account", account];
  }

  public transferArgs(channelId: string, transfer: Transfer) {
    return [
      "transfer",
      "--channel_id",
      channelId,
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
      transfer.is_deposit ? "--is_deposit" : "",
    ];
  }

  public positionsArgs(channelId: string, account: string) {
    return ["positions", "--channel_id", channelId, "--account", account];
  }

  /**
   * Turns a request's domain into the CLI's domain flags. The CLI takes the
   * domain's chainId from the quote, so a domain for another chain, or one using
   * a salt, cannot be signed and is rejected here rather than silently signed
   * against the wrong domain.
   */
  private _domainArgs(quote: Quote): Array<string> {
    const domain = quote.domain;
    if (!domain) {
      return [];
    }

    if (domain.salt) {
      throw new Error("quote domain: salt is not supported");
    }
    if (domain.chainId !== undefined && domain.chainId !== null) {
      const domainChainId = Number(domain.chainId);
      if (domainChainId !== quote.chainId) {
        throw new Error(
          `quote domain: chainId ${domain.chainId} does not match the quote's chain ${quote.chainId}`,
        );
      }
    }

    const missing = (["name", "version", "verifyingContract"] as const).filter(
      (field) => !domain[field],
    );
    if (missing.length) {
      throw new Error(`quote domain: missing ${missing.join(", ")}`);
    }

    return [
      "--domain_name",
      domain.name!,
      "--domain_version",
      domain.version!,
      "--domain_verifying_contract",
      domain.verifyingContract!,
    ];
  }

  /**
   * Base url of the premium rfq api. Left out of the args when not given, so the
   * CLI's own default (production) applies; pass it for a local or staging api.
   */
  private _premiumURLArgs(url?: string): Array<string> {
    return url ? ["--url", url] : [];
  }

  /**
   * The premium api signs against the pool's option handler, which arrives on
   * the request as `typeDataDomain`. Only the verifying contract is required -
   * the CLI defaults the name and version - but a domain it cannot sign is
   * rejected here rather than passed on.
   */
  private _premiumDomainArgs(quote: Quote): Array<string> {
    const domain = quote.domain;
    if (!domain?.verifyingContract) {
      throw new Error(
        "premium quote domain: missing verifyingContract, pass the request's typeDataDomain",
      );
    }
    if (domain.salt) {
      throw new Error("premium quote domain: salt is not supported");
    }
    if (domain.chainId !== undefined && domain.chainId !== null) {
      if (Number(domain.chainId) !== quote.chainId) {
        throw new Error(
          `premium quote domain: chainId ${domain.chainId} does not match the quote's chain ${quote.chainId}`,
        );
      }
    }

    return [
      ...(domain.name ? ["--domain_name", domain.name] : []),
      ...(domain.version ? ["--domain_version", domain.version] : []),
      "--domain_verifying_contract",
      domain.verifyingContract,
    ];
  }

  /** Requests this maker may quote, each carrying the domain to sign against. */
  public premiumRequestsArgs(maker: string, url?: string) {
    return [
      "premium",
      "requests",
      "--maker",
      maker,
      ...this._premiumURLArgs(url),
    ];
  }

  /** This maker's live quotes - the only place quote ids come from. */
  public premiumQuotesArgs(maker: string, url?: string) {
    return [
      "premium",
      "quotes",
      "--maker",
      maker,
      ...this._premiumURLArgs(url),
    ];
  }

  /** One quote by id, whatever its status. */
  public premiumQuoteStatusArgs(id: string, url?: string) {
    return [
      "premium",
      "quote-status",
      "--id",
      id,
      ...this._premiumURLArgs(url),
    ];
  }

  /**
   * Signs and posts one quote. The terms have to be the request's - the api
   * rebuilds the signed message from the stored request - and `validUntil` is in
   * seconds, strictly between now+2min and now+10min.
   */
  public premiumQuoteArgs(requestId: string, quote: Quote, url?: string) {
    return [
      "premium",
      "quote",
      "--request_id",
      requestId,
      "--asset",
      quote.assetAddress,
      "--chain_id",
      quote.chainId.toString(),
      "--expiry",
      quote.expiry.toString(),
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
      quote.validUntil.toString(),
      ...this._premiumDomainArgs(quote),
      ...this._premiumURLArgs(url),
      quote.isPut ? "--is_put" : "",
      quote.isTakerBuy ? "--is_taker_buy" : "",
    ];
  }

  /**
   * Posts a batch of quotes in one call, reading them from `source` - a file
   * path, or "-" for stdin. Build the file with `premiumQuoteBatch`.
   */
  public premiumQuoteBatchArgs(source: string, url?: string) {
    return [
      "premium",
      "quote",
      "--batch",
      source,
      ...this._premiumURLArgs(url),
    ];
  }

  /**
   * Serialises quotes into the json array `premiumQuoteBatchArgs` reads, failing
   * on a domain the CLI could not sign before anything is written.
   */
  public premiumQuoteBatch(quotes: Array<{ requestId: string; quote: Quote }>) {
    return JSON.stringify(
      quotes.map(({ requestId, quote }) => {
        this._premiumDomainArgs(quote);
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
    return [
      "premium",
      "cancel",
      "--id",
      id,
      "--chain_id",
      chainId.toString(),
      "--nonce",
      nonce,
      ...this._premiumURLArgs(url),
    ];
  }

  public quoteArgs(channelId: string, rfqId: string, quote: Quote) {
    return [
      "quote",
      "--channel_id",
      channelId,
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
      quote.isPut ? "--is_put" : "",
      quote.isTakerBuy ? "--is_taker_buy" : "",
    ];
  }
}

export { NonceCounter } from "./nonce.js";

export default Rysk;
