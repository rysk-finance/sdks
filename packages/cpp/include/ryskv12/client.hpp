#pragma once

#include <array>
#include <functional>
#include <memory>
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

class Rysk;

/// A running cli: what `connect` returns, so commands can be written into it.
///
/// Join any thread inside read_lines before this is destroyed.
class Process {
 public:
  Process(Process&& other) noexcept;
  Process& operator=(Process&&) = delete;
  Process(const Process&) = delete;
  Process& operator=(const Process&) = delete;
  /// Kills the cli if it is still running, and reaps it.
  ~Process();

  /// Writes `args` to the cli's stdin as one json array line - how a `connect`
  /// is told to sign and send a quote, transfer, balances or positions. Safe to
  /// call from several threads. Throws Error once the cli is gone.
  void send(const std::vector<std::string>& args);

  /// Hands each line of stdout to `on_line` until the cli exits, returning its
  /// exit code. stderr, where the cli logs, is drained and dropped.
  int read_lines(const std::function<void(const std::string&)>& on_line);

  /// SIGTERM: this is how a connection is disconnected.
  void kill();

 private:
  friend class Rysk;
  struct State;
  explicit Process(std::unique_ptr<State> state);
  void close_stdin();
  Output drain(const std::function<void(const std::string&)>* on_line);

  std::unique_ptr<State> state_;
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

  /// A connection to `uri`. Spawn it, then `send` it the four commands below.
  std::vector<std::string> connect_args(const std::string& uri) const;

  /// `asset` is the erc20 to approve. Left out, the cli falls back to the
  /// chain's strike asset.
  std::vector<std::string> approve_args(std::int64_t chain_id, const std::string& amount,
                                        const std::string& rpc_url,
                                        std::optional<std::string> asset = std::nullopt) const;

  std::vector<std::string> balances_args(const std::string& account) const;
  std::vector<std::string> positions_args(const std::string& account) const;
  std::vector<std::string> transfer_args(const Transfer& transfer) const;

  /// Throws Error when the quote's domain is one the cli could not sign.
  std::vector<std::string> quote_args(const std::string& rfq_id, const Quote& quote) const;

  /// Opts an otoken in or out of redemption at expiry. Send it into a running
  /// `connect` on the maker endpoint.
  std::vector<std::string> will_redeem_args(std::int64_t chain_id, const std::string& account,
                                            const std::string& otoken, bool will_redeem,
                                            const std::string& nonce) const;

  /// Redeems the signer's own otokens that expired at or before `expiry`
  /// (default: now). A plain http call, so `run` it rather than `send` it.
  /// `url` defaults to this env's api.
  std::vector<std::string> self_redeem_args(
      std::int64_t chain_id, const std::string& nonce,
      std::optional<std::int64_t> expiry = std::nullopt,
      const std::vector<std::string>& exclude_assets = {},
      std::optional<std::string> url = std::nullopt) const;

  // ------------------------------------------------------------- premium rfq

  std::vector<std::string> premium_requests_args(
      const std::string& maker, std::optional<std::string> url = std::nullopt) const;
  std::vector<std::string> premium_quotes_args(
      const std::string& maker, std::optional<std::string> url = std::nullopt) const;
  std::vector<std::string> premium_quote_status_args(
      const std::string& id, std::optional<std::string> url = std::nullopt) const;
  std::vector<std::string> premium_quote_batch_args(
      const std::string& source, std::optional<std::string> url = std::nullopt) const;
  std::vector<std::string> premium_cancel_args(
      const std::string& id, std::int64_t chain_id, const std::string& nonce,
      std::optional<std::string> url = std::nullopt) const;

  /// Taker: the pools requests can be opened on.
  std::vector<std::string> premium_pools_args(std::optional<std::string> url = std::nullopt) const;
  /// Taker: opens the requests in `source` (a json array, a file path or "-" for
  /// stdin); this key has to be an rfq signer on each request's pool.
  std::vector<std::string> premium_request_args(
      const std::string& source, std::int64_t chain_id, const std::string& nonce,
      std::optional<std::string> url = std::nullopt) const;
  /// Taker: a pool's live requests.
  std::vector<std::string> premium_taker_requests_args(
      const std::string& taker, std::optional<std::string> url = std::nullopt) const;
  /// Taker: the live quotes on one request.
  std::vector<std::string> premium_request_quotes_args(
      const std::string& id, std::optional<std::string> url = std::nullopt) const;
  /// Taker: cancels a request and every quote on it.
  std::vector<std::string> premium_cancel_request_args(
      const std::string& id, std::int64_t chain_id, const std::string& nonce,
      std::optional<std::string> url = std::nullopt) const;
  /// Taker: takes a quote by id; the cli signs the confirmation and prints the
  /// settlement's transaction hash.
  std::vector<std::string> premium_execute_args(
      const std::string& quote_id, const std::string& nonce,
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

  /// Starts the cli and returns while it runs, for a `connect` that commands
  /// are sent into. The key travels as it does for `run`.
  Process spawn(const std::vector<std::string>& args) const;

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
  std::vector<std::string> domain_args(const Quote& quote, bool premium) const;
  std::vector<std::string> premium_url_args(const std::optional<std::string>& url) const;
  void version_check() const;
  void report_version(const std::string& found) const;
};

}  // namespace ryskv12
