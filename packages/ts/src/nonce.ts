import { readFileSync, writeFileSync } from "node:fs";

/**
 * File backed monotonic nonce source.
 *
 * A nonce is spent once per address and the api keys them on `(address, nonce)`
 * alone, so quotes, cancels and confirmations from one signing key all draw from
 * the same sequence. It has to survive a restart too: a counter that rewinds
 * hands out nonces the api has already seen, and every write starts failing.
 *
 * The clock is the floor, so a lost file only ever skips forward.
 */
export class NonceCounter {
  private _path: string;
  private _last: number;

  constructor(path: string = ".rysk-nonce") {
    this._path = path;
    this._last = 0;

    try {
      const stored = Number.parseInt(readFileSync(this._path, "utf8").trim(), 10);
      if (Number.isSafeInteger(stored)) {
        this._last = stored;
      }
    } catch {
      // no counter yet, the clock takes over
    }
  }

  /** The next unused nonce, as the decimal string the CLI takes. */
  public next(): string {
    const next = Math.max(Date.now(), this._last + 1);
    this._last = next;
    // persisted before it is handed out: a crash has to lose a nonce, never reuse one
    writeFileSync(this._path, String(next));
    return String(next);
  }

  /** The last nonce handed out, or 0 when none has been. */
  public get last(): number {
    return this._last;
  }
}
