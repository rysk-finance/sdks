// C++ has no json, so this sdk carries its own. It is the one piece here the
// other sdks get from their standard library, so it is tested on its own terms.

#include "harness.hpp"
#include "ryskv12/json.hpp"

using ryskv12::Json;

static Json parsed(const std::string& text) {
  auto value = Json::parse(text);
  CHECK_MSG(value.has_value(), "did not parse: " + text);
  return value ? *value : Json();
}

TEST(parses_the_scalars) {
  CHECK(parsed("null").is_null());
  CHECK_EQ(parsed("true").as_bool(), std::optional<bool>(true));
  CHECK_EQ(parsed("false").as_bool(), std::optional<bool>(false));
  CHECK_EQ(parsed("\"hi\"").as_string(), std::optional<std::string>("hi"));
  CHECK_EQ(parsed("42").as_int(), std::optional<std::int64_t>(42));
  CHECK_EQ(parsed("-42").as_int(), std::optional<std::int64_t>(-42));
  CHECK_EQ(parsed("0").as_int(), std::optional<std::int64_t>(0));
}

TEST(keeps_integers_integral) {
  // a nonce is a millisecond timestamp; coming back as 1.7e12 would be useless
  CHECK_EQ(parsed("1787740878814").as_int(), std::optional<std::int64_t>(1787740878814LL));
  CHECK_EQ(parsed("1787740878814").dump(), std::string("1787740878814"));
  CHECK(parsed("1.5").is_int() == false);
  CHECK(parsed("1e3").is_int() == false);
}

TEST(rejects_what_is_not_json) {
  CHECK(!Json::parse("").has_value());
  CHECK(!Json::parse("{").has_value());
  CHECK(!Json::parse("[1,]").has_value());
  CHECK(!Json::parse("{\"a\":1,}").has_value());
  CHECK(!Json::parse("{a:1}").has_value());
  CHECK(!Json::parse("'single'").has_value());
  CHECK(!Json::parse("01").has_value());  // json forbids leading zeros
  CHECK(!Json::parse("1.").has_value());
  CHECK(!Json::parse(".1").has_value());
  CHECK(!Json::parse("1e").has_value());
  CHECK(!Json::parse("nul").has_value());
  CHECK(!Json::parse("{} trailing").has_value());
  CHECK(!Json::parse("\"unterminated").has_value());
}

TEST(reports_where_it_gave_up) {
  std::string error;
  CHECK(!Json::parse("{\"a\" 1}", &error).has_value());
  CHECK_MSG(!error.empty(), "no error text");
  CHECK_MSG(error.find("byte") != std::string::npos, "no position in: " + error);
}

TEST(handles_string_escapes) {
  CHECK_EQ(parsed(R"("a\"b")").as_string(), std::optional<std::string>("a\"b"));
  CHECK_EQ(parsed(R"("a\\b")").as_string(), std::optional<std::string>("a\\b"));
  CHECK_EQ(parsed(R"("a\nb")").as_string(), std::optional<std::string>("a\nb"));
  CHECK_EQ(parsed(R"("a\/b")").as_string(), std::optional<std::string>("a/b"));
  CHECK_EQ(parsed(R"("A")").as_string(), std::optional<std::string>("A"));
  // a raw control character is not allowed inside a string
  CHECK(!Json::parse("\"a\nb\"").has_value());
  CHECK(!Json::parse(R"("\q")").has_value());
  CHECK(!Json::parse(R"("\u00")").has_value());
  CHECK(!Json::parse(R"("\uZZZZ")").has_value());
}

TEST(handles_surrogate_pairs) {
  // U+1F600, which only fits in utf-16 as a pair
  auto emoji = parsed(R"("😀")").as_string();
  CHECK(emoji.has_value());
  CHECK_EQ(*emoji, std::string("\xF0\x9F\x98\x80"));

  // a lone half is malformed, not silently replaced
  CHECK(!Json::parse(R"("\ud83d")").has_value());
  CHECK(!Json::parse(R"("\ude00")").has_value());
  CHECK(!Json::parse(R"("\ud83dA")").has_value());
}

TEST(walks_objects_and_arrays) {
  Json value = parsed(R"({"a":{"b":[1,2,{"c":"d"}]}})");
  CHECK_EQ(value["a"]["b"][2]["c"].as_string(), std::optional<std::string>("d"));
  CHECK_EQ(value["a"]["b"].size(), std::size_t(3));
  CHECK(value.contains("a"));
  CHECK(!value.contains("z"));

  // a missing key is null rather than a throw, so chains need no guarding
  CHECK(value["nope"]["deeper"][7].is_null());
  // and indexing the wrong type is null too
  CHECK(value["a"]["b"]["not-an-index"].is_null());
}

TEST(accessors_do_not_coerce) {
  Json value = parsed(R"({"n":1,"s":"1","b":true})");
  // the whole point of the other sdks' predicates: 1 is not "1"
  CHECK(!value["n"].as_string().has_value());
  CHECK(!value["s"].as_int().has_value());
  CHECK(!value["b"].as_int().has_value());
  CHECK(!value["n"].as_bool().has_value());
  CHECK_EQ(value["n"].as_int(), std::optional<std::int64_t>(1));
}

TEST(round_trips_through_dump) {
  for (const std::string& text : {
           R"({"a":1,"b":[true,false,null],"c":"x"})",
           R"([])",
           R"({})",
           R"([{"nested":{"deep":[1,2,3]}}])",
       }) {
    CHECK_EQ(parsed(text).dump(), text);
  }
}

TEST(dump_escapes_what_it_must) {
  Json::Object object;
  object["quote\""] = Json(std::string("tab\there"));
  object["ctrl"] = Json(std::string("\x01"));
  std::string dumped = Json(object).dump();

  CHECK_MSG(dumped.find(R"("quote\"")") != std::string::npos, dumped);
  CHECK_MSG(dumped.find(R"(tab\there)") != std::string::npos, dumped);
  // a control byte has to leave as the six character escape, not raw
  CHECK_MSG(dumped.find("\\u0001") != std::string::npos, dumped);
  CHECK_MSG(dumped.find('\x01') == std::string::npos, dumped);
  // and it parses back to what went in
  CHECK(Json::parse(dumped).has_value());
  CHECK_EQ(Json::parse(dumped)->dump(), Json(object).dump());
}

TEST(dumps_object_keys_in_a_stable_order) {
  Json::Object object;
  object["z"] = Json(1);
  object["a"] = Json(2);
  object["m"] = Json(3);
  CHECK_EQ(Json(object).dump(), std::string(R"({"a":2,"m":3,"z":1})"));
}

TEST(ignores_surrounding_whitespace) {
  CHECK_EQ(parsed("  \n\t {\"a\" : 1}  \r\n").dump(), std::string(R"({"a":1})"));
}

HARNESS_MAIN()
