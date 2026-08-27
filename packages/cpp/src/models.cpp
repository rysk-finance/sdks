#include "ryskv12/models.hpp"

#include <cerrno>
#include <cstdlib>

namespace ryskv12 {
namespace {

// A required field: present and of the right type, or the whole parse fails.
bool need_string(const Json& obj, const char* key, std::string& out) {
  auto v = obj[key].as_string();
  if (!v) return false;
  out = *v;
  return true;
}

bool need_int(const Json& obj, const char* key, std::int64_t& out) {
  auto v = obj[key].as_int();
  if (!v) return false;
  out = *v;
  return true;
}

bool need_bool(const Json& obj, const char* key, bool& out) {
  auto v = obj[key].as_bool();
  if (!v) return false;
  out = *v;
  return true;
}

// An optional field: absent or null is fine, present and wrongly typed is not.
template <typename T, typename Read>
bool maybe(const Json& obj, const char* key, std::optional<T>& out, Read read) {
  const Json& field = obj[key];
  if (field.is_null()) return true;
  auto v = read(field);
  if (!v) return false;
  out = *v;
  return true;
}

bool maybe_string(const Json& obj, const char* key, std::optional<std::string>& out) {
  return maybe<std::string>(obj, key, out, [](const Json& j) { return j.as_string(); });
}

bool maybe_int(const Json& obj, const char* key, std::optional<std::int64_t>& out) {
  return maybe<std::int64_t>(obj, key, out, [](const Json& j) { return j.as_int(); });
}

bool maybe_bool(const Json& obj, const char* key, std::optional<bool>& out) {
  return maybe<bool>(obj, key, out, [](const Json& j) { return j.as_bool(); });
}

void put(Json::Object& object, const char* key, const std::optional<std::string>& value) {
  if (value) object[key] = Json(*value);
}

}  // namespace

std::optional<std::int64_t> chain_id_value(const std::string& raw) {
  std::string text = raw;
  std::size_t begin = text.find_first_not_of(" \t\n\r");
  if (begin == std::string::npos) return std::nullopt;
  std::size_t end = text.find_last_not_of(" \t\n\r");
  text = text.substr(begin, end - begin + 1);
  if (text.empty()) return std::nullopt;

  int base = 10;
  const char* start = text.c_str();
  if (text.size() > 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'X')) {
    base = 16;
    start += 2;
  }
  errno = 0;
  char* stop = nullptr;
  long long value = std::strtoll(start, &stop, base);
  if (errno != 0 || stop == start || *stop != '\0') return std::nullopt;
  return static_cast<std::int64_t>(value);
}

// ------------------------------------------------------------ TypedDataDomain

Json TypedDataDomain::to_json() const {
  Json::Object object;
  put(object, "name", name);
  put(object, "version", version);
  put(object, "chainId", chain_id);
  put(object, "verifyingContract", verifying_contract);
  put(object, "salt", salt);
  return Json(std::move(object));
}

std::optional<TypedDataDomain> TypedDataDomain::from_json(const Json& value) {
  if (!value.is_object()) return std::nullopt;
  TypedDataDomain domain;
  if (!maybe_string(value, "name", domain.name)) return std::nullopt;
  if (!maybe_string(value, "version", domain.version)) return std::nullopt;
  if (!maybe_string(value, "verifyingContract", domain.verifying_contract)) return std::nullopt;
  if (!maybe_string(value, "salt", domain.salt)) return std::nullopt;

  // the server sends the chain either way round; both are kept as text
  const Json& chain = value["chainId"];
  if (chain.is_string()) {
    domain.chain_id = *chain.as_string();
  } else if (chain.is_int()) {
    domain.chain_id = std::to_string(*chain.as_int());
  } else if (!chain.is_null()) {
    return std::nullopt;
  }
  return domain;
}

// -------------------------------------------------------------------- Request

std::optional<Request> Request::from_json(const Json& value) {
  if (!value.is_object()) return std::nullopt;
  Request request;
  if (!need_string(value, "asset", request.asset)) return std::nullopt;
  if (!need_int(value, "chainId", request.chain_id)) return std::nullopt;
  if (!need_int(value, "expiry", request.expiry)) return std::nullopt;
  if (!need_bool(value, "isPut", request.is_put)) return std::nullopt;
  if (!need_string(value, "quantity", request.quantity)) return std::nullopt;
  if (!need_string(value, "strike", request.strike)) return std::nullopt;
  if (!need_string(value, "taker", request.taker)) return std::nullopt;
  if (!need_string(value, "usd", request.usd)) return std::nullopt;
  if (!need_string(value, "collateralAsset", request.collateral_asset)) return std::nullopt;

  if (!maybe_string(value, "id", request.id)) return std::nullopt;
  if (!maybe_string(value, "assetName", request.asset_name)) return std::nullopt;
  if (!maybe_bool(value, "isTakerBuy", request.is_taker_buy)) return std::nullopt;
  if (!maybe_int(value, "validUntil", request.valid_until)) return std::nullopt;
  if (!maybe_int(value, "createdAt", request.created_at)) return std::nullopt;
  if (!maybe_int(value, "auctionDeadline", request.auction_deadline)) return std::nullopt;
  if (!maybe_bool(value, "isPremium", request.is_premium)) return std::nullopt;
  if (!maybe_string(value, "premiumAsset", request.premium_asset)) return std::nullopt;
  if (!maybe_bool(value, "isEIP1271", request.is_eip1271)) return std::nullopt;

  const Json& makers = value["makers"];
  if (makers.is_array()) {
    std::vector<std::string> names;
    for (const Json& entry : *makers.as_array()) {
      auto name = entry.as_string();
      if (!name) return std::nullopt;
      names.push_back(*name);
    }
    request.makers = std::move(names);
  } else if (!makers.is_null()) {
    return std::nullopt;
  }

  const Json& domain = value["typeDataDomain"];
  if (!domain.is_null()) {
    auto parsed = TypedDataDomain::from_json(domain);
    if (!parsed) return std::nullopt;
    request.type_data_domain = *parsed;
  }
  return request;
}

std::optional<Request> Request::parse(std::string_view text) {
  auto value = Json::parse(text);
  if (!value) return std::nullopt;
  return from_json(*value);
}

// ---------------------------------------------------------------------- Quote

Json Quote::to_json() const {
  Json::Object object;
  object["assetAddress"] = Json(asset_address);
  object["chainId"] = Json(chain_id);
  object["expiry"] = Json(expiry);
  object["isPut"] = Json(is_put);
  object["isTakerBuy"] = Json(is_taker_buy);
  object["maker"] = Json(maker);
  object["nonce"] = Json(nonce);
  object["price"] = Json(price);
  object["quantity"] = Json(quantity);
  object["strike"] = Json(strike);
  object["validUntil"] = Json(valid_until);
  object["usd"] = Json(usd);
  object["collateralAsset"] = Json(collateral_asset);
  // an unset optional is left out rather than sent as null
  put(object, "premiumAsset", premium_asset);
  if (domain) object["domain"] = domain->to_json();
  return Json(std::move(object));
}

// ----------------------------------------------------------- QuoteNotification

std::optional<QuoteNotification> QuoteNotification::from_json(const Json& value) {
  if (!value.is_object()) return std::nullopt;
  QuoteNotification note;
  if (!need_string(value, "rfqId", note.rfq_id)) return std::nullopt;
  if (!need_string(value, "assetAddress", note.asset_address)) return std::nullopt;
  if (!need_int(value, "chainId", note.chain_id)) return std::nullopt;
  if (!need_string(value, "newBest", note.new_best)) return std::nullopt;
  if (!need_string(value, "yours", note.yours)) return std::nullopt;
  return note;
}

// ------------------------------------------------------------------- Transfer

Json Transfer::to_json() const {
  Json::Object object;
  object["user"] = Json(user);
  object["amount"] = Json(amount);
  object["asset"] = Json(asset);
  object["chainId"] = Json(chain_id);
  object["isDeposit"] = Json(is_deposit);
  object["nonce"] = Json(nonce);
  return Json(std::move(object));
}

std::optional<Transfer> Transfer::from_json(const Json& value) {
  if (!value.is_object()) return std::nullopt;
  Transfer transfer;
  if (!need_string(value, "user", transfer.user)) return std::nullopt;
  if (!need_string(value, "amount", transfer.amount)) return std::nullopt;
  if (!need_string(value, "asset", transfer.asset)) return std::nullopt;
  if (!need_int(value, "chainId", transfer.chain_id)) return std::nullopt;
  if (!need_bool(value, "isDeposit", transfer.is_deposit)) return std::nullopt;
  if (!need_string(value, "nonce", transfer.nonce)) return std::nullopt;
  return transfer;
}

// ------------------------------------------------------------------ responses

std::optional<ErrorData> ErrorData::from_json(const Json& value) {
  if (!value.is_object()) return std::nullopt;
  ErrorData error;
  if (!need_int(value, "code", error.code)) return std::nullopt;
  if (!need_string(value, "message", error.message)) return std::nullopt;
  if (value.contains("data")) error.data = value["data"];
  return error;
}

std::optional<JsonRpcResponse> JsonRpcResponse::from_json(const Json& value) {
  if (!value.is_object()) return std::nullopt;
  JsonRpcResponse response;
  if (!need_string(value, "jsonrpc", response.jsonrpc)) return std::nullopt;
  if (!need_string(value, "id", response.id)) return std::nullopt;

  const Json& error = value["error"];
  if (!error.is_null()) {
    auto parsed = ErrorData::from_json(error);
    if (!parsed) return std::nullopt;
    response.error = *parsed;
  }
  if (value.contains("result") && !value["result"].is_null()) {
    response.result = value["result"];
  }

  // a failed call carries error and no result, so requiring a well formed
  // result would drop every error the cli reports
  if (!response.result && !response.error) return std::nullopt;
  return response;
}

std::optional<JsonRpcResponse> JsonRpcResponse::parse(std::string_view text) {
  auto value = Json::parse(text);
  if (!value) return std::nullopt;
  return from_json(*value);
}

}  // namespace ryskv12
