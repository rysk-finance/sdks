#include "ryskv12/client.hpp"

#include <poll.h>
#include <sys/stat.h>
#include <signal.h>
#include <sys/wait.h>
#include <unistd.h>

#include <array>
#include <cctype>
#include <cstdio>
#include <cstring>
#include <iostream>
#include <sstream>

#include "ryskv12/fetch_script.hpp"

namespace ryskv12 {

const char* const kReleasesUrl = "https://github.com/rysk-finance/sdks/releases";

namespace {

/// Oldest cli this sdk will not warn about. Kept in step with the other sdks.
constexpr std::array<int, 3> kMinCliVersion{4, 0, 0};

void pair(std::vector<std::string>& args, const char* flag, std::string value) {
  args.emplace_back(flag);
  args.push_back(std::move(value));
}

bool set(const std::optional<std::string>& value) { return value && !value->empty(); }

std::string trim(const std::string& raw) {
  std::size_t begin = raw.find_first_not_of(" \t\n\r");
  if (begin == std::string::npos) return "";
  std::size_t end = raw.find_last_not_of(" \t\n\r");
  return raw.substr(begin, end - begin + 1);
}

/// Runs `path` with `args`, `RYSK_PRIVATE_KEY` set, and both pipes drained
/// concurrently so a chatty stderr cannot deadlock a long stdout.
Output spawn_and_drain(const std::string& path, const std::vector<std::string>& args,
                       const std::string& private_key,
                       const std::function<void(const std::string&)>* on_line) {
  int out_pipe[2];
  int err_pipe[2];
  if (::pipe(out_pipe) != 0) throw Error("could not create a pipe");
  if (::pipe(err_pipe) != 0) {
    ::close(out_pipe[0]);
    ::close(out_pipe[1]);
    throw Error("could not create a pipe");
  }

  pid_t pid = ::fork();
  if (pid < 0) {
    ::close(out_pipe[0]);
    ::close(out_pipe[1]);
    ::close(err_pipe[0]);
    ::close(err_pipe[1]);
    throw Error("could not fork");
  }

  if (pid == 0) {
    ::close(out_pipe[0]);
    ::close(err_pipe[0]);
    ::dup2(out_pipe[1], STDOUT_FILENO);
    ::dup2(err_pipe[1], STDERR_FILENO);
    ::close(out_pipe[1]);
    ::close(err_pipe[1]);

    // the key goes into the environment, never argv
    ::setenv("RYSK_PRIVATE_KEY", private_key.c_str(), 1);

    std::vector<char*> argv;
    argv.push_back(const_cast<char*>(path.c_str()));
    for (const std::string& arg : args) argv.push_back(const_cast<char*>(arg.c_str()));
    argv.push_back(nullptr);

    ::execv(path.c_str(), argv.data());
    // exec only returns on failure; 127 is the shell's convention for not found
    ::_exit(127);
  }

  ::close(out_pipe[1]);
  ::close(err_pipe[1]);

  Output result;
  std::string line_buffer;
  std::array<pollfd, 2> fds{};
  fds[0] = {out_pipe[0], POLLIN, 0};
  fds[1] = {err_pipe[0], POLLIN, 0};

  int open_fds = 2;
  while (open_fds > 0) {
    if (::poll(fds.data(), fds.size(), -1) < 0) {
      if (errno == EINTR) continue;
      break;
    }
    for (std::size_t i = 0; i < fds.size(); ++i) {
      if (fds[i].fd < 0 || !(fds[i].revents & (POLLIN | POLLHUP | POLLERR))) continue;

      char buffer[4096];
      ssize_t n = ::read(fds[i].fd, buffer, sizeof(buffer));
      if (n > 0) {
        std::string chunk(buffer, static_cast<std::size_t>(n));
        if (i == 0) {
          result.out += chunk;
          if (on_line) {
            line_buffer += chunk;
            std::size_t newline;
            while ((newline = line_buffer.find('\n')) != std::string::npos) {
              (*on_line)(line_buffer.substr(0, newline));
              line_buffer.erase(0, newline + 1);
            }
          }
        } else {
          result.err += chunk;
        }
        continue;
      }
      // 0 is eof, negative is an error we cannot recover per fd
      ::close(fds[i].fd);
      fds[i].fd = -1;
      --open_fds;
    }
  }
  if (on_line && !line_buffer.empty()) (*on_line)(line_buffer);

  int status = 0;
  while (::waitpid(pid, &status, 0) < 0 && errno == EINTR) {
  }
  if (WIFEXITED(status)) {
    result.exit_code = WEXITSTATUS(status);
  } else if (WIFSIGNALED(status)) {
    result.exit_code = 128 + WTERMSIG(status);
  }
  return result;
}

}  // namespace

const char* base_url(Env env) {
  switch (env) {
    case Env::Local: return "ws://localhost:8000/";
    case Env::Testnet: return "wss://rip-testnet.rysk.finance/";
    case Env::Mainnet: return "wss://v12.rysk.finance/";
  }
  return "";
}

std::optional<std::array<int, 3>> parse_version(const std::string& raw) {
  std::string text = trim(raw);
  if (!text.empty() && (text[0] == 'v' || text[0] == 'V')) text.erase(0, 1);

  std::array<int, 3> parts{};
  std::size_t pos = 0;
  for (int i = 0; i < 3; ++i) {
    if (i > 0) {
      if (pos >= text.size() || text[pos] != '.') return std::nullopt;
      ++pos;
    }
    std::size_t start = pos;
    while (pos < text.size() && std::isdigit(static_cast<unsigned char>(text[pos]))) ++pos;
    if (pos == start) return std::nullopt;
    parts[static_cast<std::size_t>(i)] = std::stoi(text.substr(start, pos - start));
  }
  return parts;
}

Rysk::Rysk(Env env, std::string private_key) : Rysk(env, std::move(private_key), Options{}) {}

Rysk::Rysk(Env env, std::string private_key, Options options)
    : env_(env),
      cli_path_(std::move(options.cli_path)),
      private_key_(std::move(private_key)),
      strict_version_(options.strict_version) {
  if (!options.skip_version_check) version_check();
}

std::string Rysk::url(const std::string& uri) const { return std::string(base_url(env_)) + uri; }

// ------------------------------------------------------------------- version

void Rysk::version_check() const {
  Output result;
  try {
    result = spawn_and_drain(cli_path_, {"version"}, private_key_, nullptr);
  } catch (const Error& e) {
    std::cerr << cli_path_ << " could not be run: " << e.what() << "\n";
    return;
  }
  if (result.exit_code == 127) {
    std::cerr << cli_path_ << " not found.\nDownload it here " << kReleasesUrl << ".\n";
    return;
  }

  std::string out = trim(result.out);
  // a cli old enough to have no version command predates every version we support
  if (result.err.find("No help topic for 'version'") != std::string::npos || out.empty()) {
    report_version("too old to report a version");
    return;
  }
  auto found = parse_version(out);
  // a locally built cli reports something like "dev"; nothing to compare
  if (!found) return;
  if (*found < kMinCliVersion) report_version(out);
}

void Rysk::report_version(const std::string& found) const {
  std::ostringstream message;
  message << cli_path_ << " is " << found << ", but this sdk needs " << kMinCliVersion[0] << "."
          << kMinCliVersion[1] << "." << kMinCliVersion[2]
          << " or newer. Commands added since " << found
          << " will fail with \"flag provided but not defined\".\nDownload it here "
          << kReleasesUrl << ".";
  if (strict_version_) throw Error(message.str());
  std::cerr << message.str() << "\n";
}

// --------------------------------------------------------------- arg builders

std::vector<std::string> Rysk::connect_args(const std::string& channel_id,
                                            const std::string& uri) const {
  return {"connect", "--channel_id", channel_id, "--url", url(uri)};
}

std::vector<std::string> Rysk::disconnect_args(const std::string& channel_id) const {
  return {"disconnect", "--channel_id", channel_id};
}

std::vector<std::string> Rysk::approve_args(std::int64_t chain_id, const std::string& amount,
                                            const std::string& rpc_url,
                                            std::optional<std::string> asset) const {
  std::vector<std::string> args{"approve"};
  pair(args, "--chain_id", std::to_string(chain_id));
  pair(args, "--amount", amount);
  pair(args, "--rpc_url", rpc_url);
  if (set(asset)) pair(args, "--asset", *asset);
  return args;
}

std::vector<std::string> Rysk::balances_args(const std::string& channel_id,
                                             const std::string& account) const {
  return {"balances", "--channel_id", channel_id, "--account", account};
}

std::vector<std::string> Rysk::positions_args(const std::string& channel_id,
                                              const std::string& account) const {
  return {"positions", "--channel_id", channel_id, "--account", account};
}

std::vector<std::string> Rysk::transfer_args(const std::string& channel_id,
                                             const Transfer& transfer) const {
  std::vector<std::string> args{"transfer"};
  pair(args, "--channel_id", channel_id);
  pair(args, "--chain_id", std::to_string(transfer.chain_id));
  pair(args, "--user", transfer.user);
  pair(args, "--asset", transfer.asset);
  pair(args, "--amount", transfer.amount);
  pair(args, "--nonce", transfer.nonce);
  if (transfer.is_deposit) args.emplace_back("--is_deposit");
  return args;
}

std::vector<std::string> Rysk::quote_args(const std::string& channel_id,
                                          const std::string& rfq_id, const Quote& quote) const {
  std::vector<std::string> args{"quote"};
  pair(args, "--channel_id", channel_id);
  pair(args, "--rfq_id", rfq_id);
  pair(args, "--asset", quote.asset_address);
  pair(args, "--chain_id", std::to_string(quote.chain_id));
  pair(args, "--expiry", std::to_string(quote.expiry));
  pair(args, "--maker", quote.maker);
  pair(args, "--nonce", quote.nonce);
  pair(args, "--price", quote.price);
  pair(args, "--quantity", quote.quantity);
  pair(args, "--strike", quote.strike);
  pair(args, "--valid_until", std::to_string(quote.valid_until));
  pair(args, "--usd", quote.usd);
  pair(args, "--collateral", quote.collateral_asset);
  if (set(quote.premium_asset)) pair(args, "--premium_asset", *quote.premium_asset);

  for (std::string& flag : domain_args(quote)) args.push_back(std::move(flag));
  if (quote.is_put) args.emplace_back("--is_put");
  if (quote.is_taker_buy) args.emplace_back("--is_taker_buy");
  return args;
}

namespace {

/// The two rejections both domains share: a salt cannot be signed, and a chain
/// that is not the quote's would be signed against the wrong domain.
void check_shared_domain(const TypedDataDomain& domain, std::int64_t quote_chain_id,
                         const char* what) {
  if (set(domain.salt)) throw Error(std::string(what) + ": salt is not supported");
  if (set(domain.chain_id)) {
    auto value = chain_id_value(*domain.chain_id);
    if (!value || *value != quote_chain_id) {
      throw Error(std::string(what) + ": chainId " + *domain.chain_id +
                  " does not match the quote's chain " + std::to_string(quote_chain_id));
    }
  }
}

}  // namespace

std::vector<std::string> Rysk::domain_args(const Quote& quote) const {
  if (!quote.domain) return {};
  const TypedDataDomain& domain = *quote.domain;
  check_shared_domain(domain, quote.chain_id, "quote domain");

  std::vector<std::string> missing;
  if (!set(domain.name)) missing.emplace_back("name");
  if (!set(domain.version)) missing.emplace_back("version");
  if (!set(domain.verifying_contract)) missing.emplace_back("verifyingContract");
  if (!missing.empty()) {
    std::string joined;
    for (std::size_t i = 0; i < missing.size(); ++i) {
      if (i) joined += ", ";
      joined += missing[i];
    }
    throw Error("quote domain: missing " + joined);
  }

  std::vector<std::string> args;
  pair(args, "--domain_name", *domain.name);
  pair(args, "--domain_version", *domain.version);
  pair(args, "--domain_verifying_contract", *domain.verifying_contract);
  return args;
}

// ----------------------------------------------------------------- premium rfq

std::vector<std::string> Rysk::premium_url_args(const std::optional<std::string>& url) const {
  if (!set(url)) return {};
  return {"--url", *url};
}

std::vector<std::string> Rysk::premium_domain_args(const Quote& quote) const {
  if (!quote.domain || !set(quote.domain->verifying_contract)) {
    throw Error(
        "premium quote domain: missing verifyingContract, pass the request's typeDataDomain");
  }
  const TypedDataDomain& domain = *quote.domain;
  check_shared_domain(domain, quote.chain_id, "premium quote domain");

  std::vector<std::string> args;
  if (set(domain.name)) pair(args, "--domain_name", *domain.name);
  if (set(domain.version)) pair(args, "--domain_version", *domain.version);
  pair(args, "--domain_verifying_contract", *domain.verifying_contract);
  return args;
}

std::vector<std::string> Rysk::premium_requests_args(const std::string& maker,
                                                     std::optional<std::string> url) const {
  std::vector<std::string> args{"premium", "requests"};
  pair(args, "--maker", maker);
  for (std::string& flag : premium_url_args(url)) args.push_back(std::move(flag));
  return args;
}

std::vector<std::string> Rysk::premium_quotes_args(const std::string& maker,
                                                   std::optional<std::string> url) const {
  std::vector<std::string> args{"premium", "quotes"};
  pair(args, "--maker", maker);
  for (std::string& flag : premium_url_args(url)) args.push_back(std::move(flag));
  return args;
}

std::vector<std::string> Rysk::premium_quote_status_args(const std::string& id,
                                                         std::optional<std::string> url) const {
  std::vector<std::string> args{"premium", "quote-status"};
  pair(args, "--id", id);
  for (std::string& flag : premium_url_args(url)) args.push_back(std::move(flag));
  return args;
}

std::vector<std::string> Rysk::premium_quote_args(const std::string& request_id,
                                                  const Quote& quote,
                                                  std::optional<std::string> url) const {
  std::vector<std::string> args{"premium", "quote"};
  pair(args, "--request_id", request_id);
  pair(args, "--asset", quote.asset_address);
  pair(args, "--chain_id", std::to_string(quote.chain_id));
  pair(args, "--expiry", std::to_string(quote.expiry));
  pair(args, "--strike", quote.strike);
  pair(args, "--quantity", quote.quantity);
  pair(args, "--usd", quote.usd);
  pair(args, "--collateral", quote.collateral_asset);
  pair(args, "--maker", quote.maker);
  pair(args, "--nonce", quote.nonce);
  pair(args, "--price", quote.price);
  pair(args, "--valid_until", std::to_string(quote.valid_until));

  for (std::string& flag : premium_domain_args(quote)) args.push_back(std::move(flag));
  for (std::string& flag : premium_url_args(url)) args.push_back(std::move(flag));
  if (quote.is_put) args.emplace_back("--is_put");
  if (quote.is_taker_buy) args.emplace_back("--is_taker_buy");
  return args;
}

std::vector<std::string> Rysk::premium_quote_batch_args(const std::string& source,
                                                        std::optional<std::string> url) const {
  std::vector<std::string> args{"premium", "quote"};
  pair(args, "--batch", source);
  for (std::string& flag : premium_url_args(url)) args.push_back(std::move(flag));
  return args;
}

std::string Rysk::premium_quote_batch(
    const std::vector<std::pair<std::string, Quote>>& quotes) const {
  Json::Array entries;
  entries.reserve(quotes.size());
  for (const auto& [request_id, quote] : quotes) {
    premium_domain_args(quote);  // throws before anything is written
    Json value = quote.to_json();
    Json::Object object = *value.as_object();
    object["requestId"] = Json(request_id);
    entries.emplace_back(std::move(object));
  }
  return Json(std::move(entries)).dump();
}

std::vector<std::string> Rysk::premium_cancel_args(const std::string& id, std::int64_t chain_id,
                                                   const std::string& nonce,
                                                   std::optional<std::string> url) const {
  std::vector<std::string> args{"premium", "cancel"};
  pair(args, "--id", id);
  pair(args, "--chain_id", std::to_string(chain_id));
  pair(args, "--nonce", nonce);
  for (std::string& flag : premium_url_args(url)) args.push_back(std::move(flag));
  return args;
}

// ---------------------------------------------------------------------- process

Output Rysk::run(const std::vector<std::string>& args) const {
  return spawn_and_drain(cli_path_, args, private_key_, nullptr);
}

int Rysk::run_lines(const std::vector<std::string>& args,
                    const std::function<void(const std::string&)>& on_line) const {
  return spawn_and_drain(cli_path_, args, private_key_, &on_line).exit_code;
}

std::string Rysk::setup() const {
  // the script ships with the library, written out through sh's stdin so there
  // is no temp file to clean up or race on
  std::string command = "sh -s";
  FILE* pipe = ::popen(command.c_str(), "w");
  if (!pipe) throw Error("could not start the download script");
  std::fputs(kFetchScript, pipe);
  int status = ::pclose(pipe);
  if (status != 0) throw Error("failed to download the cli");

  const char* downloaded = "ryskV12";
  if (::access(downloaded, F_OK) != 0) {
    // the script reports missing dependencies and still exits 0
    throw Error(std::string("the download script left no ") + downloaded);
  }
  ::chmod(downloaded, 0755);
  return downloaded;
}

}  // namespace ryskv12
