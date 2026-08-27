// Drives the real cli binary against a fake premium api: the arg builders and
// the binary have to agree on flags, the key has to travel in the environment,
// and a rejected quote has to fail the command.
//
// Skipped when no cli is present. `make dev-bin` from the repo root builds one.

#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>

#include <atomic>
#include <cctype>
#include <cstdlib>
#include <chrono>
#include <cstdio>
#include <fstream>
#include <functional>
#include <mutex>
#include <string>
#include <thread>
#include <vector>

#include "harness.hpp"
#include "ryskv12/client.hpp"

using namespace ryskv12;

/// well known test key; its address is the maker below
static const char* const PK =
    "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318";
static const char* const MAKER = "0x2c7536E3605D9C16a7a3D7b1898e529396a65c23";
static const char* const HANDLER = "0x54f1cc396e08f0defbe956bcddf6abe46d61cb48";
static const char* const ASSET = "0x98d56648c9b7f3cb49531f4135115b5000ab1733";

static std::string cli_path() {
  const char* from_env = std::getenv("RYSK_CLI_PATH");
  return from_env ? from_env : "./ryskV12";
}

static bool have_cli() {
  static bool announced = false;
  if (::access(cli_path().c_str(), X_OK) == 0) return true;
  if (!announced) {
    announced = true;
    std::cerr << "no cli at " << cli_path() << "; run `make dev-bin`\n";
  }
  return false;
}

static std::int64_t millis() {
  return std::chrono::duration_cast<std::chrono::milliseconds>(
             std::chrono::system_clock::now().time_since_epoch())
      .count();
}

static Rysk sdk() {
  Rysk::Options options;
  options.cli_path = cli_path();
  return Rysk(Env::Testnet, PK, options);
}

static Quote quote() {
  Quote q;
  q.asset_address = ASSET;
  q.chain_id = 84532;
  q.expiry = 1767225600;
  q.is_put = true;
  q.is_taker_buy = false;
  q.maker = MAKER;
  q.nonce = std::to_string(millis());
  q.price = "1250000000000000000";
  q.quantity = "1000000000000000000";
  q.strike = "300000000000";
  // the api takes a window of now+2min .. now+10min
  q.valid_until = millis() / 1000 + 300;
  q.usd = ASSET;
  q.collateral_asset = ASSET;
  TypedDataDomain domain;
  domain.verifying_contract = HANDLER;
  q.domain = domain;
  return q;
}

struct Call {
  std::string method;
  std::string path;
  std::string headers;  // lowercased, one per line
  std::string body;
};

/// Records what reached it and answers with whatever `respond` returns for the
/// nth call.
class FakeApi {
 public:
  using Responder = std::function<std::pair<int, std::string>(int)>;

  explicit FakeApi(Responder respond) : respond_(std::move(respond)) {
    listen_fd_ = ::socket(AF_INET, SOCK_STREAM, 0);
    int reuse = 1;
    ::setsockopt(listen_fd_, SOL_SOCKET, SO_REUSEADDR, &reuse, sizeof(reuse));

    sockaddr_in address{};
    address.sin_family = AF_INET;
    address.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    address.sin_port = 0;
    ::bind(listen_fd_, reinterpret_cast<sockaddr*>(&address), sizeof(address));
    ::listen(listen_fd_, 8);

    socklen_t length = sizeof(address);
    ::getsockname(listen_fd_, reinterpret_cast<sockaddr*>(&address), &length);
    url_ = "http://127.0.0.1:" + std::to_string(ntohs(address.sin_port));

    worker_ = std::thread([this] { serve(); });
  }

  ~FakeApi() {
    stopped_ = true;
    ::shutdown(listen_fd_, SHUT_RDWR);
    ::close(listen_fd_);
    if (worker_.joinable()) worker_.join();
  }

  const std::string& url() const { return url_; }

  std::vector<Call> calls() {
    std::lock_guard<std::mutex> guard(mutex_);
    return calls_;
  }

  static Responder json_ok(std::string body) {
    return [body](int) { return std::make_pair(200, body); };
  }

 private:
  int listen_fd_ = -1;
  std::string url_;
  Responder respond_;
  std::thread worker_;
  std::atomic<bool> stopped_{false};
  std::mutex mutex_;
  std::vector<Call> calls_;

  void serve() {
    while (!stopped_) {
      int client = ::accept(listen_fd_, nullptr, nullptr);
      if (client < 0) {
        if (stopped_) break;
        continue;
      }
      handle(client);
      ::close(client);
    }
  }

  void handle(int client) {
    std::string request;
    std::size_t header_end = std::string::npos;
    char buffer[4096];

    // headers first, so Content-Length says how much body to expect
    while ((header_end = request.find("\r\n\r\n")) == std::string::npos) {
      ssize_t n = ::recv(client, buffer, sizeof(buffer), 0);
      if (n <= 0) return;
      request.append(buffer, static_cast<std::size_t>(n));
    }

    std::string head = request.substr(0, header_end);
    std::string body = request.substr(header_end + 4);

    std::string lowered;
    lowered.reserve(head.size());
    for (char c : head) lowered.push_back(static_cast<char>(std::tolower(c)));

    std::size_t at = lowered.find("content-length:");
    std::size_t expected = 0;
    if (at != std::string::npos) expected = std::strtoul(lowered.c_str() + at + 15, nullptr, 10);
    while (body.size() < expected) {
      ssize_t n = ::recv(client, buffer, sizeof(buffer), 0);
      if (n <= 0) break;
      body.append(buffer, static_cast<std::size_t>(n));
    }

    Call call;
    std::size_t first_space = head.find(' ');
    std::size_t second_space = head.find(' ', first_space + 1);
    call.method = head.substr(0, first_space);
    call.path = head.substr(first_space + 1, second_space - first_space - 1);
    call.headers = lowered;
    call.body = body;

    int nth;
    {
      std::lock_guard<std::mutex> guard(mutex_);
      calls_.push_back(call);
      nth = static_cast<int>(calls_.size());
    }

    auto [status, payload] = respond_(nth);
    std::string response = "HTTP/1.1 " + std::to_string(status) +
                           " X\r\nContent-Type: application/json\r\nContent-Length: " +
                           std::to_string(payload.size()) + "\r\nConnection: close\r\n\r\n" +
                           payload;
    ::send(client, response.data(), response.size(), 0);
  }
};

static std::string trimmed(const std::string& raw) {
  std::size_t begin = raw.find_first_not_of(" \t\n\r");
  if (begin == std::string::npos) return "";
  std::size_t end = raw.find_last_not_of(" \t\n\r");
  return raw.substr(begin, end - begin + 1);
}

TEST(the_cli_reports_a_version_the_sdk_accepts) {
  if (!have_cli()) return;
  Output out = sdk().run({"version"});
  CHECK_MSG(out.exit_code == 0, out.err);
  CHECK_MSG(!trimmed(out.out).empty(), "version printed nothing");
}

TEST(the_cli_signs_and_posts_what_the_sdk_built) {
  if (!have_cli()) return;
  FakeApi api(FakeApi::json_ok(R"({"failures":[]})"));
  Rysk rysk = sdk();

  Output out = rysk.run(rysk.premium_quote_args("req-1", quote(), api.url()));
  CHECK_MSG(out.exit_code == 0, out.err);
  CHECK_EQ(trimmed(out.out), std::string(R"({"failures":[]})"));

  auto calls = api.calls();
  CHECK_EQ(calls.size(), std::size_t(1));
  if (calls.empty()) return;
  CHECK_EQ(calls[0].method, std::string("POST"));
  CHECK_EQ(calls[0].path, std::string("/api/quotes"));

  auto posted = Json::parse(calls[0].body);
  CHECK(posted.has_value());
  if (!posted) return;
  CHECK_EQ(posted->size(), std::size_t(1));

  // the terms are not posted: the server rebuilds them from the stored request
  const Json& entry = (*posted)[std::size_t(0)];
  const Json::Object* fields = entry.as_object();
  CHECK(fields != nullptr);
  if (!fields) return;
  std::vector<std::string> keys;
  for (const auto& [key, unused] : *fields) {
    (void)unused;
    keys.push_back(key);
  }
  CHECK_EQ(keys, (std::vector<std::string>{"maker", "nonce", "price", "requestId", "signature",
                                           "validUntil"}));

  std::string signature = entry["signature"].as_string().value_or("");
  CHECK_MSG(signature.size() == 132 && signature.rfind("0x", 0) == 0,
            "not a signature: " + signature);
}

TEST(a_batch_is_one_call) {
  if (!have_cli()) return;
  FakeApi api(FakeApi::json_ok(R"({"failures":[]})"));
  Rysk rysk = sdk();

  std::vector<std::pair<std::string, Quote>> entries;
  std::int64_t now = millis();
  for (int i = 0; i < 5; ++i) {
    Quote q = quote();
    q.nonce = std::to_string(now + i);
    entries.emplace_back("req-" + std::to_string(i), q);
  }

  std::string path = "/tmp/rysk-cpp-batch-" + std::to_string(::getpid()) + ".json";
  {
    std::ofstream file(path);
    file << rysk.premium_quote_batch(entries);
  }

  Output out = rysk.run(rysk.premium_quote_batch_args(path, api.url()));
  CHECK_MSG(out.exit_code == 0, out.err);

  auto calls = api.calls();
  CHECK_MSG(calls.size() == 1, "a batch has to be one request");
  if (!calls.empty()) {
    auto posted = Json::parse(calls[0].body);
    CHECK(posted.has_value());
    if (posted) CHECK_EQ(posted->size(), std::size_t(5));
  }
  ::unlink(path.c_str());
}

TEST(a_rejected_quote_fails_the_command) {
  if (!have_cli()) return;
  FakeApi api(FakeApi::json_ok(R"({"failures":[{"error":"request expired","quote":{}}]})"));
  Rysk rysk = sdk();

  Output out = rysk.run(rysk.premium_quote_args("req-1", quote(), api.url()));
  CHECK_MSG(out.exit_code != 0, "the api answers 200, the exit code is the result");
  CHECK_MSG(out.out.find("request expired") != std::string::npos, out.out);
  CHECK_MSG(out.err.find("1 of 1 quotes rejected") != std::string::npos, out.err);
}

TEST(rate_limit_and_missing_routes_are_told_apart) {
  if (!have_cli()) return;
  FakeApi api([](int nth) {
    return nth == 1 ? std::make_pair(429, std::string("cc"))
                    : std::make_pair(404, std::string("404 page not found"));
  });
  Rysk rysk = sdk();
  auto args = rysk.premium_requests_args(MAKER, api.url());

  Output limited = rysk.run(args);
  CHECK(limited.exit_code != 0);
  CHECK_MSG(limited.err.find("rate limited") != std::string::npos, limited.err);

  Output missing = rysk.run(args);
  CHECK(missing.exit_code != 0);
  CHECK_MSG(missing.err.find("no rfq routes") != std::string::npos, missing.err);
}

TEST(the_key_travels_in_the_environment_not_argv) {
  if (!have_cli()) return;
  FakeApi api([](int) { return std::make_pair(204, std::string()); });
  Rysk rysk = sdk();

  auto args = rysk.premium_cancel_args("24361", 84532, "43", api.url());
  for (const std::string& arg : args) {
    CHECK_MSG(arg != "--private_key", "the key flag is in argv");
    CHECK_MSG(arg.find(PK) == std::string::npos, "the key is in argv");
  }

  // and the cli still authenticates, so it did get the key
  Output out = rysk.run(args);
  CHECK_MSG(out.exit_code == 0, out.err);

  auto calls = api.calls();
  CHECK_EQ(calls.size(), std::size_t(1));
  if (calls.empty()) return;
  bool authenticated = calls[0].headers.find("signature") != std::string::npos ||
                       calls[0].headers.find("auth") != std::string::npos ||
                       calls[0].headers.find("maker") != std::string::npos;
  CHECK_MSG(authenticated, "no auth header in:\n" + calls[0].headers);
}

TEST(run_lines_hands_over_each_line_as_it_arrives) {
  if (!have_cli()) return;
  Rysk rysk = sdk();
  std::vector<std::string> lines;
  int code = rysk.run_lines({"version"}, [&](const std::string& line) { lines.push_back(line); });

  CHECK_EQ(code, 0);
  CHECK_MSG(!lines.empty(), "no lines delivered");
  if (!lines.empty()) CHECK_MSG(!lines[0].empty(), "first line was empty");
}

HARNESS_MAIN()
