#pragma once

#include <cstdint>
#include <string>

namespace ryskv12 {

/// File backed monotonic nonce source.
///
/// A nonce is spent once per address and the api keys them on (address, nonce)
/// alone, so quotes, cancels and confirmations from one signing key all draw
/// from the same sequence. It has to survive a restart too: a counter that
/// rewinds hands out nonces the api has already seen, and every write starts
/// failing.
///
/// The clock is the floor, so a lost file only ever skips forward.
class NonceCounter {
 public:
  explicit NonceCounter(std::string path = ".rysk-nonce");

  /// The next unused nonce, as the decimal string the cli takes.
  ///
  /// Persisted before it is handed out: a crash has to lose a nonce, never
  /// reuse one. Throws std::runtime_error if the counter cannot be written,
  /// rather than handing out one that would not survive a restart.
  std::string next();

  /// The last nonce handed out, or 0 when none has been.
  std::uint64_t last() const { return last_; }

 private:
  std::string path_;
  std::uint64_t last_ = 0;
};

}  // namespace ryskv12
