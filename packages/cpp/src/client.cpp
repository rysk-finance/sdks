#include "ryskv12/client.hpp"

#include <fcntl.h>
#include <poll.h>
#include <sys/stat.h>
#include <signal.h>
#include <sys/wait.h>
#include <unistd.h>

#include <array>
#include <atomic>
#include <cctype>
#include <cstdio>
#include <cstring>
#include <iostream>
#include <mutex>
#include <sstream>

#include "ryskv12/fetch_script.hpp"

namespace ryskv12 {

const char* const kReleasesUrl = "https://github.com/rysk-finance/sdks/releases";

namespace {

/// Oldest cli this sdk will not warn about. Kept in step with the other sdks.
constexpr std::array<int, 3> kMinCliVersion{5, 1, 0};

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

void close_fd(int& fd) {
  if (fd >= 0) ::close(fd);
  fd = -1;
}

}  // namespace

struct Process::State {
  std::atomic<pid_t> pid{-1};
  int in = -1;
  int out = -1;
  int err = -1;
  std::mutex send_mutex;
};

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
    result = run({"version"});
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

std::vector<std::string> Rysk::connect_args(const std::string& uri) const {
  return {"connect", "--url", url(uri)};
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

std::vector<std::string> Rysk::balances_args(const std::string& account) const {
  return {"balances", "--account", account};
}

std::vector<std::string> Rysk::positions_args(const std::string& account) const {
  return {"positions", "--account", account};
}

std::vector<std::string> Rysk::transfer_args(const Transfer& transfer) const {
  std::vector<std::string> args{"transfer"};
  pair(args, "--chain_id", std::to_string(transfer.chain_id));
  pair(args, "--user", transfer.user);
  pair(args, "--asset", transfer.asset);
  pair(args, "--amount", transfer.amount);
  pair(args, "--nonce", transfer.nonce);
  if (transfer.is_deposit) args.emplace_back("--is_deposit");
  return args;
}

std::vector<std::string> Rysk::quote_args(const std::string& rfq_id, const Quote& quote) const {
  std::vector<std::string> args{"quote"};
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

  for (std::string& flag : domain_args(quote, false)) args.push_back(std::move(flag));
  if (quote.is_put) args.emplace_back("--is_put");
  if (quote.is_taker_buy) args.emplace_back("--is_taker_buy");
  return args;
}

/// Turns a quote's domain into the cli's domain flags, rejecting one the cli
/// could not sign - a salt, or another chain's chainId - rather than signing
/// against the wrong domain. The websocket quote takes the domain all or
/// nothing; a premium quote has to name the pool's option handler, and the cli
/// defaults its name and version.
std::vector<std::string> Rysk::domain_args(const Quote& quote, bool premium) const {
  if (!quote.domain && !premium) return {};
  const TypedDataDomain empty;
  const TypedDataDomain& domain = quote.domain ? *quote.domain : empty;
  if (set(domain.salt)) throw Error("quote domain: salt is not supported");
  if (set(domain.chain_id)) {
    auto value = chain_id_value(*domain.chain_id);
    if (!value || *value != quote.chain_id) {
      throw Error("quote domain: chainId " + *domain.chain_id +
                  " does not match the quote's chain " + std::to_string(quote.chain_id));
    }
  }

  std::string missing;
  auto require = [&](const std::optional<std::string>& field, const char* name) {
    if (!set(field)) missing += (missing.empty() ? "" : ", ") + std::string(name);
  };
  if (!premium) {
    require(domain.name, "name");
    require(domain.version, "version");
  }
  require(domain.verifying_contract, "verifyingContract");
  if (!missing.empty()) {
    throw Error("quote domain: missing " + missing +
                (premium ? ", pass the request's typeDataDomain" : ""));
  }

  std::vector<std::string> args;
  if (set(domain.name)) pair(args, "--domain_name", *domain.name);
  if (set(domain.version)) pair(args, "--domain_version", *domain.version);
  pair(args, "--domain_verifying_contract", *domain.verifying_contract);
  return args;
}

std::vector<std::string> Rysk::will_redeem_args(std::int64_t chain_id, const std::string& account,
                                              const std::string& otoken, bool will_redeem,
                                              const std::string& nonce) const {
  std::vector<std::string> args{"will_redeem"};
  pair(args, "--chain_id", std::to_string(chain_id));
  pair(args, "--account", account);
  pair(args, "--otoken", otoken);
  pair(args, "--nonce", nonce);
  if (will_redeem) args.emplace_back("--will_redeem");
  return args;
}

std::vector<std::string> Rysk::self_redeem_args(std::int64_t chain_id, const std::string& nonce,
                                              std::optional<std::int64_t> expiry,
                                              const std::vector<std::string>& exclude_assets,
                                              std::optional<std::string> url) const {
  // ws://host/ -> http://host, wss://host/ -> https://host
  std::string base = std::string("http") + (base_url(env_) + 2);
  if (!base.empty() && base.back() == '/') base.pop_back();

  std::vector<std::string> args{"self-redeem"};
  pair(args, "--url", url ? *url : base);
  pair(args, "--chain_id", std::to_string(chain_id));
  pair(args, "--nonce", nonce);
  if (expiry) pair(args, "--expiry", std::to_string(*expiry));
  if (!exclude_assets.empty()) {
    std::string joined;
    for (const std::string& asset : exclude_assets) joined += (joined.empty() ? "" : ",") + asset;
    pair(args, "--exclude_assets", joined);
  }
  return args;
}

// ----------------------------------------------------------------- premium rfq

std::vector<std::string> Rysk::premium_url_args(const std::optional<std::string>& url) const {
  if (!set(url)) return {};
  return {"--url", *url};
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
    domain_args(quote, true);  // throws before anything is written
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

std::vector<std::string> Rysk::premium_pools_args(std::optional<std::string> url) const {
  std::vector<std::string> args{"premium", "pools"};
  for (std::string& flag : premium_url_args(url)) args.push_back(std::move(flag));
  return args;
}

std::vector<std::string> Rysk::premium_request_args(const std::string& source,
                                                    std::int64_t chain_id,
                                                    const std::string& nonce,
                                                    std::optional<std::string> url) const {
  std::vector<std::string> args{"premium", "request"};
  pair(args, "--batch", source);
  pair(args, "--chain_id", std::to_string(chain_id));
  pair(args, "--nonce", nonce);
  for (std::string& flag : premium_url_args(url)) args.push_back(std::move(flag));
  return args;
}

std::vector<std::string> Rysk::premium_taker_requests_args(const std::string& taker,
                                                           std::optional<std::string> url) const {
  std::vector<std::string> args{"premium", "taker-requests"};
  pair(args, "--taker", taker);
  for (std::string& flag : premium_url_args(url)) args.push_back(std::move(flag));
  return args;
}

std::vector<std::string> Rysk::premium_request_quotes_args(const std::string& id,
                                                           std::optional<std::string> url) const {
  std::vector<std::string> args{"premium", "request-quotes"};
  pair(args, "--id", id);
  for (std::string& flag : premium_url_args(url)) args.push_back(std::move(flag));
  return args;
}

std::vector<std::string> Rysk::premium_cancel_request_args(const std::string& id,
                                                           std::int64_t chain_id,
                                                           const std::string& nonce,
                                                           std::optional<std::string> url) const {
  std::vector<std::string> args{"premium", "cancel-request"};
  pair(args, "--id", id);
  pair(args, "--chain_id", std::to_string(chain_id));
  pair(args, "--nonce", nonce);
  for (std::string& flag : premium_url_args(url)) args.push_back(std::move(flag));
  return args;
}

std::vector<std::string> Rysk::premium_execute_args(const std::string& quote_id,
                                                    const std::string& nonce,
                                                    std::optional<std::string> url) const {
  std::vector<std::string> args{"premium", "execute"};
  pair(args, "--quote_id", quote_id);
  pair(args, "--nonce", nonce);
  for (std::string& flag : premium_url_args(url)) args.push_back(std::move(flag));
  return args;
}

// ---------------------------------------------------------------------- process

Process::Process(std::unique_ptr<State> state) : state_(std::move(state)) {}

Process::Process(Process&& other) noexcept = default;

Process::~Process() {
  if (!state_) return;  // moved from
  close_fd(state_->in);
  pid_t pid = state_->pid.exchange(-1);
  if (pid > 0) {
    ::kill(pid, SIGTERM);
    while (::waitpid(pid, nullptr, 0) < 0 && errno == EINTR) {
    }
  }
  close_fd(state_->out);
  close_fd(state_->err);
}

void Process::send(const std::vector<std::string>& args) {
  Json::Array array(args.begin(), args.end());
  std::string line = Json(std::move(array)).dump() + "\n";

  // a line is longer than PIPE_BUF, so two unguarded writers could interleave
  std::lock_guard<std::mutex> lock(state_->send_mutex);
  if (state_->in < 0) throw Error("the cli's stdin is closed");
  std::size_t written = 0;
  while (written < line.size()) {
    ssize_t n = ::write(state_->in, line.data() + written, line.size() - written);
    if (n < 0) {
      if (errno == EINTR) continue;
      throw Error(std::string("could not write to the cli: ") + std::strerror(errno));
    }
    written += static_cast<std::size_t>(n);
  }
}

int Process::read_lines(const std::function<void(const std::string&)>& on_line) {
  return drain(&on_line).exit_code;
}

void Process::kill() {
  pid_t pid = state_->pid;
  if (pid > 0) ::kill(pid, SIGTERM);
}

void Process::close_stdin() {
  std::lock_guard<std::mutex> lock(state_->send_mutex);
  close_fd(state_->in);
}

/// Drains stdout and stderr concurrently, so a chatty stderr cannot deadlock a
/// long stdout, then reaps. With `on_line` stdout goes to it and nothing is
/// kept, since a connection runs for as long as it is wanted.
Output Process::drain(const std::function<void(const std::string&)>* on_line) {
  Output result;
  std::string line_buffer;
  std::array<pollfd, 2> fds{};
  fds[0] = {state_->out, POLLIN, 0};
  fds[1] = {state_->err, POLLIN, 0};

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
        if (i == 1) {
          if (!on_line) result.err += chunk;
        } else if (!on_line) {
          result.out += chunk;
        } else {
          line_buffer += chunk;
          std::size_t newline;
          while ((newline = line_buffer.find('\n')) != std::string::npos) {
            (*on_line)(line_buffer.substr(0, newline));
            line_buffer.erase(0, newline + 1);
          }
        }
        continue;
      }
      // 0 is eof, negative is an error we cannot recover per fd
      fds[i].fd = -1;
      --open_fds;
    }
  }
  close_fd(state_->out);
  close_fd(state_->err);
  if (on_line && !line_buffer.empty()) (*on_line)(line_buffer);

  // whoever takes the pid reaps it, so the destructor never waits twice
  pid_t pid = state_->pid.exchange(-1);
  int status = 0;
  if (pid > 0) {
    while (::waitpid(pid, &status, 0) < 0 && errno == EINTR) {
    }
  }
  if (WIFEXITED(status)) {
    result.exit_code = WEXITSTATUS(status);
  } else if (WIFSIGNALED(status)) {
    result.exit_code = 128 + WTERMSIG(status);
  }
  return result;
}

/// Forks the cli with `args` and `RYSK_PRIVATE_KEY` set, stdin, stdout and
/// stderr all piped back to the parent.
Process Rysk::spawn(const std::vector<std::string>& args) const {
  const std::string& path = cli_path_;
  // A cli that has died turns the next send into SIGPIPE, which would kill the
  // host. Ignoring it, unless the host already handles it, makes the write fail
  // with EPIPE instead. The child puts it back before exec.
  struct sigaction current {};
  if (::sigaction(SIGPIPE, nullptr, &current) == 0 && current.sa_handler == SIG_DFL) {
    ::signal(SIGPIPE, SIG_IGN);
  }

  // [read, write] for each of stdin, stdout and stderr
  std::array<int, 6> fds{-1, -1, -1, -1, -1, -1};
  for (int i = 0; i < 3; ++i) {
    if (::pipe(&fds[static_cast<std::size_t>(i * 2)]) != 0) {
      for (int& fd : fds) close_fd(fd);
      throw Error("could not create a pipe");
    }
  }
  // close on exec, so a cli spawned later cannot hold this one's pipes open
  for (int fd : fds) ::fcntl(fd, F_SETFD, FD_CLOEXEC);

  pid_t pid = ::fork();
  if (pid < 0) {
    for (int& fd : fds) close_fd(fd);
    throw Error("could not fork");
  }

  if (pid == 0) {
    ::dup2(fds[0], STDIN_FILENO);
    ::dup2(fds[3], STDOUT_FILENO);
    ::dup2(fds[5], STDERR_FILENO);
    ::signal(SIGPIPE, SIG_DFL);

    // the key goes into the environment, never argv
    ::setenv("RYSK_PRIVATE_KEY", private_key_.c_str(), 1);

    std::vector<char*> argv;
    argv.push_back(const_cast<char*>(path.c_str()));
    for (const std::string& arg : args) argv.push_back(const_cast<char*>(arg.c_str()));
    argv.push_back(nullptr);

    ::execv(path.c_str(), argv.data());
    // exec only returns on failure; 127 is the shell's convention for not found
    ::_exit(127);
  }

  close_fd(fds[0]);
  close_fd(fds[3]);
  close_fd(fds[5]);
  auto state = std::make_unique<Process::State>();
  state->pid = pid;
  state->in = fds[1];
  state->out = fds[2];
  state->err = fds[4];
  return Process(std::move(state));
}

Output Rysk::run(const std::vector<std::string>& args) const {
  Process process = spawn(args);
  process.close_stdin();  // a one-shot command reading stdin sees eof
  return process.drain(nullptr);
}

int Rysk::run_lines(const std::vector<std::string>& args,
                    const std::function<void(const std::string&)>& on_line) const {
  Process process = spawn(args);
  process.close_stdin();
  return process.read_lines(on_line);
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
