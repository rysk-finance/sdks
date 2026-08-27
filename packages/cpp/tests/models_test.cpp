// The other sdks hand-write is_request / is_transfer predicates to check a
// payload's shape. Here that job is from_json, so these assert the same
// acceptances and rejections against the wire shapes those predicates target.

#include <string>

#include "harness.hpp"
#include "ryskv12/models.hpp"

using namespace ryskv12;

static const char* const MAKER = "0x1000000000000000000000000000000000000001";
static const char* const HANDLER = "0x2000000000000000000000000000000000000002";
static const char* const ASSET = "0x98d56648c9b7f3cb49531f4135115b5000ab1733";

static std::string websocket_request() {
  return std::string(R"({
    "asset": ")") + ASSET + R"(",
    "assetName": "ETH",
    "chainId": 84532,
    "expiry": 1767225600,
    "isPut": false,
    "quantity": "1",
    "strike": "1",
    "taker": ")" + MAKER + R"(",
    "usd": ")" + ASSET + R"(",
    "collateralAsset": ")" + ASSET + R"("
  })";
}

static std::string premium_request() {
  return std::string(R"({
    "id": "b7c2",
    "asset": ")") + ASSET + R"(",
    "chainId": 84532,
    "expiry": 1767225600,
    "isPut": false,
    "isTakerBuy": false,
    "quantity": "1",
    "strike": "1",
    "taker": ")" + MAKER + R"(",
    "usd": ")" + ASSET + R"(",
    "collateralAsset": ")" + ASSET + R"(",
    "validUntil": 1787740878814,
    "makers": [],
    "premiumAsset": ")" + ASSET + R"(",
    "isPremium": true,
    "isEIP1271": false,
    "createdAt": 1787740279,
    "typeDataDomain": {
      "name": "PremiumOptionHandler",
      "version": "1",
      "chainId": "0x14a34",
      "verifyingContract": ")" + HANDLER + R"("
    }
  })";
}

static std::string without(const std::string& text, const std::string& fragment) {
  std::string out = text;
  std::size_t at = out.find(fragment);
  if (at != std::string::npos) out.erase(at, fragment.size());
  return out;
}

static std::string replaced(const std::string& text, const std::string& from,
                            const std::string& to) {
  std::string out = text;
  std::size_t at = out.find(from);
  if (at != std::string::npos) out.replace(at, from.size(), to);
  return out;
}

TEST(both_request_shapes_parse) {
  auto premium = Request::parse(premium_request());
  CHECK(premium.has_value());
  if (!premium) return;

  CHECK_EQ(premium->id, std::optional<std::string>("b7c2"));
  CHECK(premium->makers.has_value());
  CHECK_EQ(premium->makers->size(), std::size_t(0));
  CHECK_EQ(premium->is_eip1271, std::optional<bool>(false));
  CHECK_EQ(premium->valid_until, std::optional<std::int64_t>(1787740878814LL));
  CHECK(premium->type_data_domain.has_value());
  CHECK_EQ(premium->type_data_domain->verifying_contract,
           std::optional<std::string>(HANDLER));
  // absent on premium requests, and absent is not empty
  CHECK(!premium->asset_name.has_value());

  // an older server's payload still parses, with the premium fields unset
  auto older = Request::parse(websocket_request());
  CHECK(older.has_value());
  if (!older) return;
  CHECK(!older->id.has_value());
  CHECK(!older->type_data_domain.has_value());
  CHECK_EQ(older->asset_name, std::optional<std::string>("ETH"));
}

TEST(a_hex_chain_id_on_the_domain_survives_the_trip) {
  auto request = Request::parse(premium_request());
  CHECK(request.has_value());
  if (!request || !request->type_data_domain) return;

  auto chain = request->type_data_domain->chain_id;
  CHECK_EQ(chain, std::optional<std::string>("0x14a34"));
  CHECK_EQ(chain_id_value(*chain), std::optional<std::int64_t>(84532));
}

TEST(a_numeric_domain_chain_id_is_kept_too) {
  // the websocket flow sends a number where the premium api sends hex text
  auto request = Request::parse(
      replaced(premium_request(), R"("chainId": "0x14a34")", R"("chainId": 84532)"));
  CHECK(request.has_value());
  if (!request || !request->type_data_domain) return;
  CHECK_EQ(request->type_data_domain->chain_id, std::optional<std::string>("84532"));
}

TEST(a_wrongly_typed_field_is_rejected_rather_than_coerced) {
  // the predicates in the other sdks exist to catch exactly these
  CHECK(!Request::parse(replaced(premium_request(), R"("chainId": 84532)",
                                 R"("chainId": "84532")"))
             .has_value());
  CHECK(!Request::parse(replaced(premium_request(), R"("isPremium": true)",
                                 R"("isPremium": "yes")"))
             .has_value());
  // a required field missing is not a Request either
  CHECK(!Request::parse(without(premium_request(),
                                std::string(R"("taker": ")") + MAKER + R"(",)"))
             .has_value());
  // nor is a list of makers holding something that is not a maker
  CHECK(!Request::parse(replaced(premium_request(), R"("makers": [])", R"("makers": [1])"))
             .has_value());
  // and neither is a non-object
  CHECK(!Request::parse("[]").has_value());
  CHECK(!Request::parse("null").has_value());
  CHECK(!Request::parse("not json at all").has_value());
}

TEST(an_optional_field_may_be_absent_but_not_wrong) {
  // absent is fine
  CHECK(Request::parse(without(premium_request(), R"("isPremium": true,)")).has_value());
  // explicit null is fine too: the server omits and nulls interchangeably
  CHECK(Request::parse(replaced(premium_request(), R"("isPremium": true)",
                                R"("isPremium": null)"))
            .has_value());
  // present and wrongly typed is not
  CHECK(!Request::parse(replaced(premium_request(), R"("createdAt": 1787740279)",
                                 R"("createdAt": "yesterday")"))
             .has_value());
}

TEST(transfer_round_trips_as_the_camel_case_the_cli_marshals) {
  Transfer transfer;
  transfer.user = MAKER;
  transfer.amount = "1";
  transfer.asset = ASSET;
  transfer.chain_id = 84532;
  transfer.is_deposit = true;
  transfer.nonce = "1";

  Json value = transfer.to_json();
  // the cli's struct tags say chainId and isDeposit, not chain_id
  CHECK_EQ(value["chainId"].as_int(), std::optional<std::int64_t>(84532));
  CHECK_EQ(value["isDeposit"].as_bool(), std::optional<bool>(true));
  CHECK(!value.contains("chain_id"));
  CHECK(!value.contains("is_deposit"));

  auto back = Transfer::from_json(value);
  CHECK(back.has_value());
  if (!back) return;
  CHECK_EQ(back->chain_id, std::int64_t(84532));
  CHECK_EQ(back->is_deposit, true);
  CHECK_EQ(back->user, std::string(MAKER));
}

TEST(a_successful_response_carries_a_result_and_no_error) {
  auto res = JsonRpcResponse::parse(R"({"jsonrpc":"2.0","id":"7","result":{"ok":true}})");
  CHECK(res.has_value());
  if (!res) return;
  CHECK(!res->error.has_value());
  CHECK(res->result.has_value());
  CHECK_EQ((*res->result)["ok"].as_bool(), std::optional<bool>(true));
}

TEST(a_failed_call_is_still_a_response) {
  // the cli sends error and omits result entirely
  auto res = JsonRpcResponse::parse(
      R"({"jsonrpc":"2.0","id":"1","error":{"code":-32000,"message":"nope","data":{"why":"late"}}})");
  CHECK(res.has_value());
  if (!res) return;

  CHECK(!res->result.has_value());
  CHECK(res->error.has_value());
  if (!res->error) return;
  CHECK_EQ(res->error->code, std::int64_t(-32000));
  CHECK_EQ(res->error->message, std::string("nope"));
  CHECK(res->error->data.has_value());
  CHECK_EQ((*res->error->data)["why"].as_string(), std::optional<std::string>("late"));
}

TEST(a_response_needs_its_envelope) {
  // no id is not a response, however good the result looks
  CHECK(!JsonRpcResponse::parse(R"({"jsonrpc":"2.0","result":{}})").has_value());
  // nor is a numeric id
  CHECK(!JsonRpcResponse::parse(R"({"jsonrpc":"2.0","id":1,"result":{}})").has_value());
  // nor is one carrying neither half
  CHECK(!JsonRpcResponse::parse(R"({"jsonrpc":"2.0","id":"1"})").has_value());
  // a malformed error cannot stand in for a result
  CHECK(!JsonRpcResponse::parse(R"({"jsonrpc":"2.0","id":"1","error":{"message":"no code"}})")
             .has_value());
}

TEST(a_result_narrows_to_the_payload_or_gives_up) {
  auto res = JsonRpcResponse::parse(std::string(R"({"jsonrpc":"2.0","id":"1","result":{
        "rfqId":"1","assetAddress":")") + ASSET + R"(","chainId":84532,
        "newBest":"1","yours":"1"}})");
  CHECK(res.has_value());
  if (!res || !res->result) return;

  auto note = QuoteNotification::from_json(*res->result);
  CHECK(note.has_value());
  if (!note) return;
  CHECK_EQ(note->rfq_id, std::string("1"));
  CHECK_EQ(note->new_best, std::string("1"));

  // the same payload is not a Request, and asking says so rather than throwing
  CHECK(!Request::from_json(*res->result).has_value());
}

HARNESS_MAIN()
