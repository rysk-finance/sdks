// Mirrors the typescript, python and rust arg-builder suites: the sdks have to
// spell the cli's flags the same way, so these assert the same things.

#include <unistd.h>

#include <algorithm>
#include <array>

#include "harness.hpp"
#include "ryskv12/client.hpp"
#include "ryskv12/nonce.hpp"

using namespace ryskv12;

static const char* const MAKER = "0x1000000000000000000000000000000000000001";
static const char* const HANDLER = "0x2000000000000000000000000000000000000002";
static const char* const ASSET = "0x98d56648c9b7f3cb49531f4135115b5000ab1733";
static const char* const KEY = "test-key";

/// No binary is spawned: these builders are pure, so the version check has
/// nothing to say and is skipped.
static Rysk sdk() {
  Rysk::Options options;
  options.cli_path = "./does-not-exist";
  options.skip_version_check = true;
  return Rysk(Env::Testnet, KEY, options);
}

static Quote quote() {
  Quote q;
  q.asset_address = ASSET;
  q.chain_id = 84532;
  q.expiry = 1767225600;
  q.is_put = true;
  q.is_taker_buy = false;
  q.maker = MAKER;
  q.nonce = "42";
  q.price = "1250000000000000000";
  q.quantity = "1000000000000000000";
  q.strike = "300000000000";
  q.valid_until = 1767139500;
  q.usd = ASSET;
  q.collateral_asset = ASSET;
  return q;
}

static TypedDataDomain premium_domain() {
  TypedDataDomain domain;
  domain.verifying_contract = HANDLER;
  return domain;
}

static bool has(const std::vector<std::string>& args, const std::string& flag) {
  return std::find(args.begin(), args.end(), flag) != args.end();
}

static std::string flag_value(const std::vector<std::string>& args, const std::string& flag) {
  auto it = std::find(args.begin(), args.end(), flag);
  if (it == args.end() || it + 1 == args.end()) return "<missing " + flag + ">";
  return *(it + 1);
}

// --------------------------------------------------------------------- basics

TEST(connect_and_disconnect) {
  Rysk rysk = sdk();
  CHECK_EQ(rysk.connect_args("chan", "maker"),
           (std::vector<std::string>{"connect", "--channel_id", "chan", "--url",
                                     "wss://rip-testnet.rysk.finance/maker"}));
  CHECK_EQ(rysk.disconnect_args("chan"),
           (std::vector<std::string>{"disconnect", "--channel_id", "chan"}));
}

TEST(every_env_has_its_own_url) {
  CHECK_EQ(std::string(base_url(Env::Local)), std::string("ws://localhost:8000/"));
  CHECK_EQ(std::string(base_url(Env::Testnet)), std::string("wss://rip-testnet.rysk.finance/"));
  CHECK_EQ(std::string(base_url(Env::Mainnet)), std::string("wss://v12.rysk.finance/"));
}

TEST(approve_leaves_the_asset_to_the_cli_unless_one_is_given) {
  Rysk rysk = sdk();
  CHECK(!has(rysk.approve_args(84532, "1", "https://rpc"), "--asset"));

  auto explicit_asset = rysk.approve_args(84532, "1", "https://rpc", ASSET);
  CHECK_EQ(flag_value(explicit_asset, "--asset"), std::string(ASSET));
  CHECK_EQ(flag_value(explicit_asset, "--amount"), std::string("1"));

  // an empty string is not an address; it must not stop the cli falling back
  CHECK(!has(rysk.approve_args(84532, "1", "https://rpc", std::string("")), "--asset"));
}

TEST(transfer_flags_a_deposit_but_not_a_withdrawal) {
  Rysk rysk = sdk();
  Transfer transfer;
  transfer.user = MAKER;
  transfer.amount = "1";
  transfer.asset = ASSET;
  transfer.chain_id = 84532;
  transfer.is_deposit = true;
  transfer.nonce = "1";

  auto deposit = rysk.transfer_args("chan", transfer);
  CHECK(has(deposit, "--is_deposit"));
  CHECK_EQ(flag_value(deposit, "--chain_id"), std::string("84532"));
  CHECK_EQ(flag_value(deposit, "--amount"), std::string("1"));

  transfer.is_deposit = false;
  CHECK(!has(rysk.transfer_args("chan", transfer), "--is_deposit"));
}

// ---------------------------------------------------------------------- quote

TEST(quote_passes_terms_and_flags) {
  Rysk rysk = sdk();
  Quote q = quote();
  q.is_taker_buy = true;
  auto args = rysk.quote_args("chan", "rfq-1", q);

  CHECK_EQ(flag_value(args, "--rfq_id"), std::string("rfq-1"));
  CHECK_EQ(flag_value(args, "--asset"), std::string(ASSET));
  CHECK_EQ(flag_value(args, "--collateral"), std::string(ASSET));
  CHECK_EQ(flag_value(args, "--valid_until"), std::string("1767139500"));
  CHECK(has(args, "--is_put"));
  CHECK(has(args, "--is_taker_buy"));

  q.is_put = false;
  q.is_taker_buy = false;
  auto neither = rysk.quote_args("chan", "rfq-1", q);
  CHECK(!has(neither, "--is_put"));
  CHECK(!has(neither, "--is_taker_buy"));
}

TEST(quote_sends_premium_asset_only_when_set) {
  Rysk rysk = sdk();
  Quote q = quote();
  q.premium_asset = HANDLER;
  CHECK_EQ(flag_value(rysk.quote_args("chan", "rfq", q), "--premium_asset"), std::string(HANDLER));
  CHECK(!has(rysk.quote_args("chan", "rfq", quote()), "--premium_asset"));
}

TEST(websocket_quote_needs_a_whole_domain) {
  Rysk rysk = sdk();
  Quote q = quote();
  TypedDataDomain domain;
  domain.name = "rysk";
  q.domain = domain;
  CHECK_THROWS(rysk.quote_args("chan", "rfq", q), "missing version, verifyingContract");
}

TEST(websocket_quote_without_a_domain_sends_no_domain_flags) {
  auto args = sdk().quote_args("chan", "rfq", quote());
  CHECK(!has(args, "--domain_name"));
  CHECK(!has(args, "--domain_verifying_contract"));
}

// -------------------------------------------------------------- premium quote

TEST(premium_quote_carries_domain_and_request_id) {
  Rysk rysk = sdk();
  Quote q = quote();
  TypedDataDomain domain;
  domain.name = "PremiumOptionHandler";
  domain.version = "1";
  domain.verifying_contract = HANDLER;
  q.domain = domain;

  auto args = rysk.premium_quote_args("req-1", q);
  CHECK_EQ(args[0], std::string("premium"));
  CHECK_EQ(args[1], std::string("quote"));
  CHECK_EQ(flag_value(args, "--request_id"), std::string("req-1"));
  CHECK_EQ(flag_value(args, "--domain_name"), std::string("PremiumOptionHandler"));
  CHECK_EQ(flag_value(args, "--domain_version"), std::string("1"));
  CHECK_EQ(flag_value(args, "--domain_verifying_contract"), std::string(HANDLER));
}

TEST(premium_defaults_the_domain_name) {
  Quote q = quote();
  q.domain = premium_domain();
  auto args = sdk().premium_quote_args("req", q);
  CHECK(!has(args, "--domain_name"));
  CHECK(!has(args, "--domain_version"));
  CHECK_EQ(flag_value(args, "--domain_verifying_contract"), std::string(HANDLER));
}

TEST(premium_url_only_passed_when_given) {
  Rysk rysk = sdk();
  CHECK(!has(rysk.premium_requests_args(MAKER), "--url"));
  CHECK_EQ(flag_value(rysk.premium_requests_args(MAKER, std::string("http://localhost:8080")),
                      "--url"),
           std::string("http://localhost:8080"));

  const std::string url = "http://x";
  CHECK(has(rysk.premium_quotes_args(MAKER, url), "--url"));
  CHECK(has(rysk.premium_quote_status_args("id", url), "--url"));
  CHECK(has(rysk.premium_cancel_args("id", 1, "1", url), "--url"));
  CHECK(has(rysk.premium_quote_batch_args("f.json", url), "--url"));
}

TEST(premium_rejects_unsignable_domains) {
  Rysk rysk = sdk();

  CHECK_THROWS(rysk.premium_quote_args("req", quote()), "missing verifyingContract");

  Quote salted = quote();
  TypedDataDomain with_salt = premium_domain();
  with_salt.salt = "0x01";
  salted.domain = with_salt;
  CHECK_THROWS(rysk.premium_quote_args("req", salted), "salt is not supported");

  Quote wrong_chain = quote();
  TypedDataDomain other = premium_domain();
  other.chain_id = "8453";
  wrong_chain.domain = other;
  CHECK_THROWS(rysk.premium_quote_args("req", wrong_chain),
               "does not match the quote's chain 84532");

  Quote unparseable = quote();
  TypedDataDomain text = premium_domain();
  text.chain_id = "mainnet";
  unparseable.domain = text;
  CHECK_THROWS(rysk.premium_quote_args("req", unparseable), "does not match");
}

TEST(premium_accepts_a_matching_hex_chain_id) {
  // the server marshals chain ids as hex strings, so 0x14a34 is 84532
  Quote q = quote();
  TypedDataDomain domain = premium_domain();
  domain.chain_id = "0x14a34";
  q.domain = domain;
  CHECK_EQ(flag_value(sdk().premium_quote_args("req", q), "--domain_verifying_contract"),
           std::string(HANDLER));
}

// ---------------------------------------------------------------------- batch

TEST(premium_quote_batch_serialises_entries) {
  Rysk rysk = sdk();
  Quote first = quote();
  first.domain = premium_domain();
  Quote second = first;
  second.nonce = "43";

  auto parsed = Json::parse(rysk.premium_quote_batch({{"req-0", first}, {"req-1", second}}));
  CHECK(parsed.has_value());
  if (!parsed) return;

  CHECK_EQ(parsed->size(), std::size_t(2));
  CHECK_EQ((*parsed)[std::size_t(0)]["requestId"].as_string(),
           std::optional<std::string>("req-0"));
  CHECK_EQ((*parsed)[std::size_t(1)]["nonce"].as_string(), std::optional<std::string>("43"));
  CHECK_EQ((*parsed)[std::size_t(0)]["domain"]["verifyingContract"].as_string(),
           std::optional<std::string>(HANDLER));
  // the cli reads camelCase, whatever the sdk calls the fields
  CHECK_EQ((*parsed)[std::size_t(0)]["assetAddress"].as_string(),
           std::optional<std::string>(ASSET));
  CHECK_EQ((*parsed)[std::size_t(0)]["collateralAsset"].as_string(),
           std::optional<std::string>(ASSET));
  CHECK_EQ((*parsed)[std::size_t(0)]["validUntil"].as_int(),
           std::optional<std::int64_t>(1767139500));
  // an unset optional is left out rather than sent as null
  CHECK(!(*parsed)[std::size_t(0)].contains("premiumAsset"));
}

TEST(premium_quote_batch_refuses_unsignable_entries) {
  // nothing is written when one entry could not be signed
  CHECK_THROWS(sdk().premium_quote_batch({{"req", quote()}}), "missing verifyingContract");
}

// ------------------------------------------------------------------- the key

TEST(no_arg_builder_carries_the_private_key) {
  Rysk rysk = sdk();
  Quote signed_quote = quote();
  TypedDataDomain domain;
  domain.name = "rysk";
  domain.version = "1";
  domain.verifying_contract = HANDLER;
  signed_quote.domain = domain;

  Transfer transfer;
  transfer.user = MAKER;
  transfer.amount = "1";
  transfer.asset = ASSET;
  transfer.chain_id = 84532;
  transfer.is_deposit = true;
  transfer.nonce = "1";

  std::vector<std::vector<std::string>> builders{
      rysk.connect_args("chan", "maker"),
      rysk.disconnect_args("chan"),
      rysk.approve_args(84532, "1", "https://rpc", ASSET),
      rysk.balances_args("chan", MAKER),
      rysk.positions_args("chan", MAKER),
      rysk.transfer_args("chan", transfer),
      rysk.quote_args("chan", "rfq", signed_quote),
      rysk.premium_requests_args(MAKER),
      rysk.premium_quotes_args(MAKER),
      rysk.premium_quote_status_args("id"),
      rysk.premium_quote_args("rfq", signed_quote),
      rysk.premium_quote_batch_args("batch.json"),
      rysk.premium_cancel_args("id", 84532, "43"),
  };

  for (const auto& args : builders) {
    CHECK_MSG(!has(args, "--private_key"), args[0] + " " + args[1] + " leaks the key");
    bool leaked = false;
    for (const std::string& arg : args) {
      if (arg.find(KEY) != std::string::npos) leaked = true;
    }
    CHECK_MSG(!leaked, args[0] + " " + args[1] + " leaks the key");
  }
}

// ---------------------------------------------------------------- versions

TEST(parses_the_versions_a_cli_reports) {
  using V = std::array<int, 3>;
  CHECK_EQ(parse_version("4.0.0").value(), (V{4, 0, 0}));
  CHECK_EQ(parse_version("v4.0.0").value(), (V{4, 0, 0}));
  CHECK_EQ(parse_version(" 4.0.0\n").value(), (V{4, 0, 0}));
  CHECK_EQ(parse_version("4.0.0-rc1").value(), (V{4, 0, 0}));
  CHECK_EQ(parse_version("10.2.3").value(), (V{10, 2, 3}));
  // a locally built cli, which must not be compared at all
  CHECK(!parse_version("dev").has_value());
  CHECK(!parse_version("").has_value());
  CHECK(!parse_version("4.0").has_value());
}

TEST(version_ordering_is_numeric_not_lexical) {
  CHECK(parse_version("4.10.0").value() > parse_version("4.9.0").value());
  CHECK(parse_version("3.2.0").value() < parse_version("4.0.0").value());
  CHECK(parse_version("4.0.1").value() > parse_version("4.0.0").value());
}

TEST(chain_ids_parse_both_ways_round) {
  CHECK_EQ(chain_id_value("84532"), std::optional<std::int64_t>(84532));
  CHECK_EQ(chain_id_value("0x14a34"), std::optional<std::int64_t>(84532));
  CHECK_EQ(chain_id_value("0X14A34"), std::optional<std::int64_t>(84532));
  CHECK_EQ(chain_id_value(" 84532 "), std::optional<std::int64_t>(84532));
  CHECK(!chain_id_value("mainnet").has_value());
  CHECK(!chain_id_value("").has_value());
  CHECK(!chain_id_value("84532x").has_value());
}

// ------------------------------------------------------------------- nonce

TEST(nonce_counter_never_repeats_or_rewinds) {
  std::string path = std::string("/tmp/rysk-cpp-nonce-") + std::to_string(::getpid());
  ::unlink(path.c_str());

  NonceCounter first(path);
  std::vector<std::string> issued{first.next(), first.next(), first.next()};

  std::vector<std::string> unique = issued;
  std::sort(unique.begin(), unique.end());
  unique.erase(std::unique(unique.begin(), unique.end()), unique.end());
  CHECK_MSG(unique.size() == issued.size(), "handed out a nonce twice");

  for (std::size_t i = 1; i < issued.size(); ++i) {
    CHECK_MSG(std::stoull(issued[i]) > std::stoull(issued[i - 1]), "went backwards");
  }

  // a restart must not rewind: the file is the floor
  NonceCounter restarted(path);
  CHECK_MSG(std::stoull(restarted.next()) > std::stoull(issued.back()), "rewound on restart");

  // a counter with no file at all still starts from the clock, never 0
  NonceCounter fresh(path + "-absent");
  CHECK_EQ(fresh.last(), std::uint64_t(0));

  ::unlink(path.c_str());
  ::unlink((path + "-absent").c_str());
}

HARNESS_MAIN()
