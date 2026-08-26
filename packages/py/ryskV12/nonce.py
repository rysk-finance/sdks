import time


class NonceCounter:
    """File backed monotonic nonce source.

    A nonce is spent once per address and the api keys them on (address, nonce)
    alone, so quotes, cancels and confirmations from one signing key all draw from
    the same sequence. It has to survive a restart too: a counter that rewinds
    hands out nonces the api has already seen, and every write starts failing.

    The clock is the floor, so a lost file only ever skips forward.
    """

    def __init__(self, path: str = ".rysk-nonce"):
        self._path = path
        self._last = 0

        try:
            with open(self._path) as f:
                self._last = int(f.read().strip())
        except (FileNotFoundError, ValueError):
            pass  # no counter yet, the clock takes over

    def next(self) -> str:
        """The next unused nonce, as the decimal string the CLI takes."""
        nonce = max(int(time.time() * 1000), self._last + 1)
        self._last = nonce
        # persisted before it is handed out: a crash has to lose a nonce, never
        # reuse one
        with open(self._path, "w") as f:
            f.write(str(nonce))
        return str(nonce)

    @property
    def last(self) -> int:
        """The last nonce handed out, or 0 when none has been."""
        return self._last
