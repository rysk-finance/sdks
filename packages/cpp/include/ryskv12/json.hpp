#pragma once

// A small json value, parser and writer.
//
// C++ ships no json, and the other sdks lean on one (serde, JSON.parse,
// json.loads) for the quote batch and for reading the cli's stdout. Pulling in
// a library would make this the only sdk here with a third party dependency, so
// this is the subset those two jobs need: no comments, no trailing commas, no
// duplicate key merging, and numbers kept as integers when they are integral so
// a nonce does not come back in scientific notation.

#include <cstdint>
#include <map>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace ryskv12 {

class Json {
 public:
  enum class Type { Null, Bool, Int, Double, String, Array, Object };

  using Array = std::vector<Json>;
  using Object = std::map<std::string, Json>;

  Json() = default;
  Json(std::nullptr_t) {}
  Json(bool v) : type_(Type::Bool), bool_(v) {}
  Json(int v) : type_(Type::Int), int_(v) {}
  Json(std::int64_t v) : type_(Type::Int), int_(v) {}
  Json(double v) : type_(Type::Double), double_(v) {}
  Json(const char* v) : type_(Type::String), string_(v) {}
  Json(std::string v) : type_(Type::String), string_(std::move(v)) {}
  Json(Array v) : type_(Type::Array), array_(std::move(v)) {}
  Json(Object v) : type_(Type::Object), object_(std::move(v)) {}

  Type type() const { return type_; }
  bool is_null() const { return type_ == Type::Null; }
  bool is_bool() const { return type_ == Type::Bool; }
  bool is_int() const { return type_ == Type::Int; }
  bool is_number() const { return type_ == Type::Int || type_ == Type::Double; }
  bool is_string() const { return type_ == Type::String; }
  bool is_array() const { return type_ == Type::Array; }
  bool is_object() const { return type_ == Type::Object; }

  // Accessors return nullopt rather than throwing or coercing: a field of the
  // wrong type is what the other sdks' predicates exist to catch.
  std::optional<bool> as_bool() const;
  std::optional<std::int64_t> as_int() const;
  std::optional<double> as_double() const;
  std::optional<std::string> as_string() const;
  const Array* as_array() const { return type_ == Type::Array ? &array_ : nullptr; }
  const Object* as_object() const { return type_ == Type::Object ? &object_ : nullptr; }

  // Missing keys give a null Json, so chains do not need guarding at each step.
  const Json& operator[](const std::string& key) const;
  const Json& operator[](std::size_t index) const;
  bool contains(const std::string& key) const;
  std::size_t size() const;

  /// Parses one json document. `error`, when given, is filled with what went
  /// wrong and where.
  static std::optional<Json> parse(std::string_view text, std::string* error = nullptr);

  /// Serialises. Object keys come out sorted, so output is reproducible.
  std::string dump() const;

  bool operator==(const Json& other) const;
  bool operator!=(const Json& other) const { return !(*this == other); }

 private:
  Type type_ = Type::Null;
  bool bool_ = false;
  std::int64_t int_ = 0;
  double double_ = 0.0;
  std::string string_;
  Array array_;
  Object object_;
};

/// Escapes a string as a json string literal, quotes included.
std::string json_quote(std::string_view raw);

}  // namespace ryskv12
