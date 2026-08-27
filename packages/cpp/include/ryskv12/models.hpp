#pragma once

#include <cstdint>
#include <optional>
#include <string>
#include <vector>

#include "ryskv12/json.hpp"

namespace ryskv12 {

/// A chain id as the server sends it, kept as text because go marshals it as a
/// hex string and the websocket flow sends a number. Use `chain_id_value` to
/// compare it.
std::optional<std::int64_t> chain_id_value(const std::string& raw);

/// EIP712 domain a quote has to be signed against. The chain is always the
/// request's, so the cli takes it from the quote itself.
struct TypedDataDomain {
  std::optional<std::string> name;
  std::optional<std::string> version;
  std::optional<std::string> chain_id;
  std::optional<std::string> verifying_contract;
  std::optional<std::string> salt;

  Json to_json() const;
  static std::optional<TypedDataDomain> from_json(const Json& value);
};

/// An rfq to quote on.
struct Request {
  std::string asset;
  std::int64_t chain_id = 0;
  std::int64_t expiry = 0;
  bool is_put = false;
  std::string quantity;
  std::string strike;
  std::string taker;
  std::string usd;
  std::string collateral_asset;

  std::optional<std::string> id;
  std::optional<std::string> asset_name;
  std::optional<bool> is_taker_buy;
  std::optional<std::int64_t> valid_until;
  std::optional<std::vector<std::string>> makers;
  std::optional<std::int64_t> created_at;
  std::optional<std::int64_t> auction_deadline;
  std::optional<bool> is_premium;
  std::optional<std::string> premium_asset;
  std::optional<bool> is_eip1271;
  std::optional<TypedDataDomain> type_data_domain;

  /// nullopt when `value` is not a request: a missing required field, or one of
  /// the wrong type. This is what the other sdks' `is_request` predicate does.
  static std::optional<Request> from_json(const Json& value);
  static std::optional<Request> parse(std::string_view text);
};

/// A price on a request. The cli signs it; nothing here carries a signature.
struct Quote {
  std::string asset_address;
  std::int64_t chain_id = 0;
  std::int64_t expiry = 0;
  bool is_put = false;
  bool is_taker_buy = false;
  std::string maker;
  std::string nonce;
  std::string price;
  std::string quantity;
  std::string strike;
  std::int64_t valid_until = 0;
  std::string usd;
  std::string collateral_asset;
  /// asset the premium is paid in; sent with the quote but not signed
  std::optional<std::string> premium_asset;
  /// domain to sign against, usually the request's `type_data_domain`
  std::optional<TypedDataDomain> domain;

  Json to_json() const;
};

/// Where your price stands, so you can re-quote rather than wait.
struct QuoteNotification {
  std::string rfq_id;
  std::string asset_address;
  std::int64_t chain_id = 0;
  std::string new_best;
  std::string yours;

  static std::optional<QuoteNotification> from_json(const Json& value);
};

/// A deposit or withdrawal.
///
/// The field names here are the sdk's; `to_json` writes the camelCase the cli
/// puts on the socket, and the arg builder spells the cli's snake_case flags.
struct Transfer {
  std::string user;
  std::string amount;
  std::string asset;
  std::int64_t chain_id = 0;
  bool is_deposit = false;
  std::string nonce;

  Json to_json() const;
  static std::optional<Transfer> from_json(const Json& value);
};

/// What the cli reports instead of a result.
struct ErrorData {
  std::int64_t code = 0;
  std::string message;
  std::optional<Json> data;

  static std::optional<ErrorData> from_json(const Json& value);
};

/// One line of the cli's stdout, once parsed.
struct JsonRpcResponse {
  std::string jsonrpc;
  std::string id;
  /// absent when the call failed - `error` carries the reason instead
  std::optional<Json> result;
  /// absent when the call succeeded
  std::optional<ErrorData> error;

  static std::optional<JsonRpcResponse> from_json(const Json& value);
  static std::optional<JsonRpcResponse> parse(std::string_view text);
};

}  // namespace ryskv12
