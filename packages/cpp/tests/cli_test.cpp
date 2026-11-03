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
#include <optional>
#include <regex>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

#include "harness.hpp"
#include "ryskv12/client.hpp"
#include "ryskv12/json.hpp"

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

/// Writes a batch of the one quote to `path` and returns the args that post it.
static std::vector<std::string> post_one(const Rysk& rysk, const std::string& path,
                                         const std::string& request_id, const Quote& q,
                                         const std::string& url) {
  std::ofstream(path) << rysk.premium_quote_batch({{request_id, q}});
  return rysk.premium_quote_batch_args(path, url);
}

static std::string scratch_path(const char* name) {
  return "/tmp/rysk-cpp-" + std::string(name) + "-" + std::to_string(::getpid()) + ".json";
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

  std::string path = scratch_path("quote");
  Output out = rysk.run(post_one(rysk, path, "req-1", quote(), api.url()));
  ::unlink(path.c_str());
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

  std::string path = scratch_path("batch");
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

  std::string path = scratch_path("rejected");
  Output out = rysk.run(post_one(rysk, path, "req-1", quote(), api.url()));
  ::unlink(path.c_str());
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

// cat stands in for connect: what send writes is exactly what comes back, so
// this pins the wire format - one json array of args per line.
TEST(send_writes_each_command_as_one_json_line) {
  Rysk::Options options;
  options.cli_path = "/bin/cat";
  options.skip_version_check = true;
  Rysk rysk(Env::Testnet, PK, options);

  std::vector<std::string> args{"quote", "--rfq_id", "a \"quoted\"\nid", "--is_put"};
  std::optional<Json> echoed;
  {
    Process process = rysk.spawn({});  // no args: cat echoes stdin
    process.send(args);
    std::thread reader([&] {
      process.read_lines([&](const std::string& line) {
        echoed = Json::parse(line);
        process.kill();
      });
    });
    reader.join();
  }  // the destructor reaps without hanging

  CHECK_MSG(echoed.has_value(), "nothing came back");
  if (echoed) CHECK_MSG(*echoed == Json(Json::Array(args.begin(), args.end())), echoed->dump());
}

static std::string env_or(const char* name, std::string fallback) {
  const char* value = std::getenv(name);
  return value && *value ? value : fallback;
}

static std::string lower(std::string value) {
  for (char& c : value) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
  return value;
}

// Opt in against the real testnet api (`make e2e` reads the keys from the repo's
// .env): a taker opens an rfq only the maker may quote, the maker quotes,
// cancels and quotes again, and the taker takes the trade on chain for 1 wei.
TEST(testnet_open_an_rfq_quote_cancel_requote_and_take_the_trade) {
  std::string maker_key = env_or("RYSK_E2E_PRIVATE_KEY", "");
  std::string maker = lower(env_or("RYSK_E2E_MAKER", ""));
  std::string taker_key = env_or("RYSK_E2E_TAKER_PRIVATE_KEY", "");
  std::string pool_address = lower(env_or("RYSK_E2E_POOL", ""));
  if (maker_key.empty() || maker.empty() || taker_key.empty() || pool_address.empty()) {
    std::cerr << "set RYSK_E2E_PRIVATE_KEY, RYSK_E2E_MAKER, RYSK_E2E_TAKER_PRIVATE_KEY and "
                 "RYSK_E2E_POOL to run against testnet\n";
    return;
  }
  if (!have_cli()) return;
  std::string url = env_or("RYSK_E2E_PREMIUM_URL", "https://insti-testnet.rysk.finance");
  // base sepolia's strike asset, which a request's usd has to be
  const std::string usd = "0x98d56648c9b7f3cb49531f4135115b5000ab1733";

  Rysk::Options options;
  options.cli_path = cli_path();
  Rysk maker_sdk(Env::Testnet, maker_key, options);
  Rysk taker_sdk(Env::Testnet, taker_key, options);
  auto ok = [](const Rysk& rysk, const std::vector<std::string>& args) {
    Output out = rysk.run(args);
    if (out.exit_code != 0) throw std::runtime_error(args[1] + ": " + out.err + "\n" + out.out);
    std::string text = trimmed(out.out);
    auto parsed = Json::parse(text.empty() ? "[]" : text);
    return parsed ? *parsed : Json(text);
  };
  auto text = [](const Json& v) { return v.as_string().value_or(v.dump()); };
  // unix nanoseconds: spent once per address, fresh across runs
  std::int64_t last = 0;
  auto nonce = [&] {
    std::int64_t now = std::chrono::duration_cast<std::chrono::nanoseconds>(
                           std::chrono::system_clock::now().time_since_epoch())
                           .count();
    last = now > last ? now : last + 1;
    return std::to_string(last);
  };

  // 1. the taker opens a request on the pool, only this maker may quote
  Json pools = ok(taker_sdk, taker_sdk.premium_pools_args(url));
  std::optional<Json> pool;
  if (const Json::Array* listed = pools.as_array()) {
    for (const Json& p : *listed) {
      if (lower(text(p["address"])) == pool_address) pool = p;
    }
  }
  if (!pool) throw std::runtime_error("pool " + pool_address + " is not listed");
  std::int64_t chain_id = (*pool)["chainId"].as_int().value_or(0);

  // next friday 08:00 utc at least a week out; 1970-01-01 was a thursday
  std::int64_t day = millis() / 1000 / 86400 + 7;
  while ((day + 4) % 7 != 5) ++day;
  Json::Object entry{
      {"asset", (*pool)["underlying"]},
      {"chainId", Json(chain_id)},
      {"expiry", Json(day * 86400 + 8 * 3600)},
      {"isPut", Json(text((*pool)["type"]) == "Put")},
      {"isTakerBuy", Json(false)},
      {"quantity", Json("1000000000000000000")},
      {"strike", Json("100000000000000")},
      {"taker", (*pool)["address"]},
      {"usd", Json(usd)},
      {"collateralAsset", (*pool)["asset"]},
      {"premiumAsset", (*pool)["asset"]},
      {"isPremium", Json(true)},
      {"makers", Json(Json::Array{Json(maker)})},
      {"validUntil", Json(millis() + 5 * 60000)},
  };
  char dir[] = "/tmp/rysk-e2e-XXXXXX";
  if (!::mkdtemp(dir)) throw std::runtime_error("mkdtemp failed");
  std::string file = std::string(dir) + "/requests.json";
  std::ofstream(file) << Json(Json::Array{Json(std::move(entry))}).dump();
  ok(taker_sdk, taker_sdk.premium_request_args(file, chain_id, nonce(), url));

  // 2. the maker polls its requests until that one shows up
  std::optional<Request> request;
  for (int i = 0; i < 30 && !request; i++) {
    Json requests = ok(maker_sdk, maker_sdk.premium_requests_args(maker, url));
    if (const Json::Array* listed = requests.as_array()) {
      for (const Json& r : *listed) {
        std::optional<Request> parsed = Request::from_json(r);
        if (parsed && lower(parsed->taker) == pool_address) request = parsed;
      }
    }
    if (!request) std::this_thread::sleep_for(std::chrono::seconds(1));
  }
  if (!request) throw std::runtime_error("no request from " + pool_address + " listed for " + maker);
  std::string request_id = request->id.value_or("");

  auto quote = [&] {
    Quote q;
    q.asset_address = request->asset;
    q.chain_id = request->chain_id;
    q.expiry = request->expiry;
    q.is_put = request->is_put;
    q.is_taker_buy = request->is_taker_buy.value_or(false);
    q.quantity = request->quantity;
    q.strike = request->strike;
    q.usd = request->usd;
    q.collateral_asset = request->collateral_asset;
    q.maker = maker;
    q.nonce = nonce();
    q.price = "1";
    q.valid_until = millis() / 1000 + 300;
    q.domain = request->type_data_domain;
    ok(maker_sdk, post_one(maker_sdk, std::string(dir) + "/quote.json", request_id, q, url));
    return q.nonce;
  };
  // the id of the maker's live quote with this nonce, or "" when there is none
  auto on_book = [&](const std::string& n) -> std::string {
    Json book = ok(maker_sdk, maker_sdk.premium_quotes_args(maker, url));
    if (const Json::Array* quotes = book.as_array()) {
      for (const Json& listed : *quotes) {
        if (text(listed["nonce"]) == n) return text(listed["id"]);
      }
    }
    return "";
  };

  // 3-4. quote, then cancel it off the book
  std::string first = quote();
  std::string id = on_book(first);
  CHECK_MSG(!id.empty(), "quote with nonce " + first + " is not in the maker's live quotes");
  if (id.empty()) return;
  ok(maker_sdk, maker_sdk.premium_cancel_args(id, request->chain_id, nonce(), url));
  CHECK_MSG(on_book(first).empty(), "quote " + id + " is still live after cancel");

  // 5-6. quote again, and the taker takes it from the request's book
  std::string second = quote();
  Json offered = ok(taker_sdk, taker_sdk.premium_request_quotes_args(request_id, url));
  std::string picked;
  if (const Json::Array* quotes = offered.as_array()) {
    for (const Json& listed : *quotes) {
      if (text(listed["nonce"]) == second) picked = text(listed["id"]);
    }
  }
  CHECK_MSG(!picked.empty(),
            "quote with nonce " + second + " is not on request " + request_id + "'s book");
  if (picked.empty()) return;
  Json tx = ok(taker_sdk, taker_sdk.premium_execute_args(picked, nonce(), url));
  CHECK_MSG(std::regex_match(text(tx), std::regex("0x[0-9a-fA-F]{64}")), text(tx));
  CHECK_MSG(on_book(second).empty(), "quote " + picked + " is still live after the trade");
}

HARNESS_MAIN()
