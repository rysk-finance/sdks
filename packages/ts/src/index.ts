import { ChildProcess, exec, spawn } from "child_process";
import { EventEmitter } from "events";
import Stream from "stream";

import { Quote, Transfer } from "./models";

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
    this.stdout = this._childProcess.stdout; // backwards compatibility
    this.stderr = this._childProcess.stdout; // backwards compatibility

    // Handle stdout data as "messages"
    this._childProcess?.stdout?.on("data", (data: Buffer) => {
      this.emit("message", data); // Assuming text messages
    });

    // Handle stderr as "errors"
    this._childProcess?.stderr?.on("data", (data: Buffer) => {
      this.emit("error", new Error(data.toString()));
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

class Rysk {
  private _env: Env;
  private _cli_path: string;
  private _private_key: string;
  private _minSdkVersion: string = "3.0.3";

  constructor(env: Env, privateKey: string, v12CliPath: string = "./ryskV12") {
    this._env = env;
    this._cli_path = v12CliPath;
    this._private_key = privateKey;
    this._sdkVersionCheck();
  }

  private _sdkVersionCheck() {
    exec([this._cli_path, "version"].join(" "), (error, stdout, stderr) => {
      if (error) {
        console.error(`exec error: ${error}`);
        return;
      }
      switch (true) {
        case stderr.includes("No help topic for 'version'"):
        case !stdout:
        case parseFloat(stdout.at(0)!) < parseFloat(this._minSdkVersion.at(0)!):
          console.error(
            `${this._cli_path} version too low: min ${this._minSdkVersion}.\nDownload it here https://github.com/rysk-finance/ryskV12/releases.`,
          );
        default:
          return;
      }
    });
  }

  private _url(uri: string): string {
    return `${ENV_CONFIGS[this._env].base_url}${uri}`;
  }

  public execute(args: Array<string> = []) {
    const childProcess = spawn(this._cli_path, args, {
      stdio: ["pipe", "pipe", "pipe"],
    });
    return new CliWebSocket(childProcess);
  }

  public connectArgs(channelId: string, uri: string) {
    return ["connect", "--channel_id", channelId, "--url", this._url(uri)];
  }

  public disconnectArgs(channelId: string) {
    return ["disconnect", "--channel_id", channelId];
  }

  public approveArgs(chainId: number, amount: string, rpcURL: string) {
    return [
      "approve",
      "--chain_id",
      chainId.toString(),
      "--amount",
      amount,
      "--rpc_url",
      rpcURL,
      "--private_key",
      this._private_key,
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
      "--private_key",
      this._private_key,
      transfer.is_deposit ? "--is_deposit" : "",
    ];
  }

  public positionsArgs(channelId: string, account: string) {
    return ["positions", "--channel_id", channelId, "--account", account];
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
      "--private_key",
      this._private_key,
      quote.isPut ? "--is_put" : "",
      quote.isTakerBuy ? "--is_taker_buy" : "",
    ];
  }
}

export default Rysk;
