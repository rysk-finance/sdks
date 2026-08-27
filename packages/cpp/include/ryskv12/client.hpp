#pragma once

#include <array>
#include <functional>
#include <optional>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

#include "ryskv12/models.hpp"

namespace ryskv12 {

extern const char* const kReleasesUrl;

/// Refused before the cli is ever spawned: a domain it would sign wrongly, or a
/// cli too old to understand the flags being built.
class Error : public std::runtime_error {
 public:
  explicit Error(const std::string& what) : std::runtime_error(what) {}
};

/// Which api the cli connects to.
enum class Env { Local, Testnet, Mainnet };
const char* base_url(Env env);

/// Parses a leading semver out of `4.0.0`, `v4.0.0` or `4.0.0-rc1`. nullopt for
/// anything without one, which is how a locally built cli reporting `dev` skips
/// the check rather than failing it.
std::optional<std::array<int, 3>> parse_version(const std::string& raw);

/// What a finished cli invocation said.
struct Output {
  int exit_code = 0;
  std::string out;
  std::string err;
};

/// Builds argument lists for the `ryskV12` cli and spawns it.
///
/// Every `*_args` method is pure: it returns what the cli would be given
/// without running anything, so the whole surface is testable without a binary.
class Rysk {
 public:
  struct Options {
    /// Where the cli binary lives.
    std::string cli_path = "./ryskV12";
    /// Throw instead of warning when the cli is older than this sdk needs.
    bool strict_version = false;
    /// Skip the startup version check entirely. Only for tests.
    bool skip_version_check = false;
  };

  Rysk(Env env, std::string private_key, Options options);
  Rysk(Env env, std::string private_key);

  const std::string& cli_path() const { return cli_path_; }

  // ------------------------------------------------------------ arg builders

  std::vector<std::string> connect_args(const std::string& channel_id,
                                        const std::string& uri) const;
  std::vector<std::string> disconnect_args(const std::string& channel_id) const;

  /// `asset` is the erc20 to approve. Left out, the cli falls back to the
  /// chain's strike asset.
  std::vector<std::string> approve_args(std::int64_t chain_id, const std::string& amount,
                                        const std::string& rpc_url,
                                        std::optional<std::string> asset = std::nullopt) const;

  std::vector<std::string> balances_args(const std::string& channel_id,
                                         const std::string& account) const;
  std::vector<std::string> positions_args(const std::string& channel_id,
                                          const std::string& account) const;
  std::vector<std::string> transfer_args(const std::string& channel_id,
                                         const Transfer& transfer) const;

  /// Throws Error when the quote's domain is one the cli could not sign.
  std::vector<std::string> quote_args(const std::string& channel_id, const std::string& rfq_id,
                                      const Quote& quote) const;

  // ------------------------------------------------------------- premium rfq

  std::vector<std::string> premium_requests_args(
      const std::string& maker, std::optional<std::string> url = std::nullopt) const;
  std::vector<std::string> premium_quotes_args(
      const std::string& maker, std::optional<std::string> url = std::nullopt) const;
  std::vector<std::string> premium_quote_status_args(
      const std::string& id, std::optional<std::string> url = std::nullopt) const;
  std::vector<std::string> premium_quote_args(
      const std::string& request_id, const Quote& quote,
      std::optional<std::string> url = std::nullopt) const;
  std::vector<std::string> premium_quote_batch_args(
      const std::string& source, std::optional<std::string> url = std::nullopt) const;
  std::vector<std::string> premium_cancel_args(
      const std::string& id, std::int64_t chain_id, const std::string& nonce,
      std::optional<std::string> url = std::nullopt) const;

  /// Serialises (request id, quote) pairs into the json array
  /// `premium_quote_batch_args` reads, throwing on a domain the cli could not
  /// sign before anything is written.
  std::string premium_quote_batch(
      const std::vector<std::pair<std::string, Quote>>& quotes) const;

  // ----------------------------------------------------------------- process

  /// Runs the cli to completion and collects everything it said.
  ///
  /// The private key is handed over through RYSK_PRIVATE_KEY rather than as an
  /// argument, so it never shows up in ps or a shell history. Spawning the cli
  /// yourself means setting that variable too.
  Output run(const std::vector<std::string>& args) const;

  /// Runs the cli and hands each line of its stdout to `on_line` as it arrives,
  /// returning the exit code once it closes.
  int run_lines(const std::vector<std::string>& args,
                const std::function<void(const std::string&)>& on_line) const;

  /// Downloads the cli release into the working directory as ./ryskV12 and
  /// makes it executable, returning that path. Blocking: the sdk cannot spawn a
  /// binary that is still downloading.
  std::string setup() const;

 private:
  Env env_;
  std::string cli_path_;
  std::string private_key_;
  bool strict_version_;

  std::string url(const std::string& uri) const;
  std::vector<std::string> domain_args(const Quote& quote) const;
  std::vector<std::string> premium_domain_args(const Quote& quote) const;
  std::vector<std::string> premium_url_args(const std::optional<std::string>& url) const;
  void version_check() const;
  void report_version(const std::string& found) const;
};

}  // namespace ryskv12
