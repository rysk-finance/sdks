use std::fs;
use std::io;
use std::time::{SystemTime, UNIX_EPOCH};

/// File backed monotonic nonce source.
///
/// A nonce is spent once per address and the api keys them on (address, nonce)
/// alone, so quotes, cancels and confirmations from one signing key all draw
/// from the same sequence. It has to survive a restart too: a counter that
/// rewinds hands out nonces the api has already seen, and every write starts
/// failing.
///
/// The clock is the floor, so a lost file only ever skips forward.
pub struct NonceCounter {
    path: String,
    last: u128,
}

impl NonceCounter {
    /// Reads any counter already at `path`; a missing or unreadable one just
    /// leaves the clock in charge.
    pub fn new(path: impl Into<String>) -> Self {
        let path = path.into();
        let last = fs::read_to_string(&path)
            .ok()
            .and_then(|s| s.trim().parse().ok())
            .unwrap_or(0);
        Self { path, last }
    }

    /// The next unused nonce, as the decimal string the cli takes.
    ///
    /// Named `next` to match the typescript and python sdks rather than to be
    /// an `Iterator`, which would have to swallow the write error.
    #[allow(clippy::should_implement_trait)]
    ///
    /// Persisted before it is handed out: a crash has to lose a nonce, never
    /// reuse one, so a failed write is an error rather than a silent skip.
    pub fn next(&mut self) -> io::Result<String> {
        let millis = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_millis())
            .unwrap_or(0);
        let nonce = millis.max(self.last + 1);
        fs::write(&self.path, nonce.to_string())?;
        self.last = nonce;
        Ok(nonce.to_string())
    }

    /// The last nonce handed out, or 0 when none has been.
    pub fn last(&self) -> u128 {
        self.last
    }
}

impl Default for NonceCounter {
    fn default() -> Self {
        Self::new(".rysk-nonce")
    }
}
