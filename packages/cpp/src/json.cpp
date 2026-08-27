#include "ryskv12/json.hpp"

#include <cctype>
#include <cerrno>
#include <cmath>
#include <cstring>
#include <cstdio>
#include <cstdlib>
#include <sstream>

namespace ryskv12 {
namespace {

const Json& null_json() {
  static const Json instance;
  return instance;
}

void append_utf8(std::string& out, std::uint32_t code) {
  if (code <= 0x7F) {
    out.push_back(static_cast<char>(code));
  } else if (code <= 0x7FF) {
    out.push_back(static_cast<char>(0xC0 | (code >> 6)));
    out.push_back(static_cast<char>(0x80 | (code & 0x3F)));
  } else if (code <= 0xFFFF) {
    out.push_back(static_cast<char>(0xE0 | (code >> 12)));
    out.push_back(static_cast<char>(0x80 | ((code >> 6) & 0x3F)));
    out.push_back(static_cast<char>(0x80 | (code & 0x3F)));
  } else {
    out.push_back(static_cast<char>(0xF0 | (code >> 18)));
    out.push_back(static_cast<char>(0x80 | ((code >> 12) & 0x3F)));
    out.push_back(static_cast<char>(0x80 | ((code >> 6) & 0x3F)));
    out.push_back(static_cast<char>(0x80 | (code & 0x3F)));
  }
}

class Parser {
 public:
  Parser(std::string_view text) : text_(text) {}

  std::optional<Json> run(std::string* error) {
    skip_space();
    auto value = parse_value();
    if (!value) {
      if (error) *error = error_;
      return std::nullopt;
    }
    skip_space();
    if (pos_ != text_.size()) {
      if (error) *error = "trailing content at byte " + std::to_string(pos_);
      return std::nullopt;
    }
    return value;
  }

 private:
  std::string_view text_;
  std::size_t pos_ = 0;
  std::string error_;

  bool fail(const std::string& what) {
    if (error_.empty()) error_ = what + " at byte " + std::to_string(pos_);
    return false;
  }

  void skip_space() {
    while (pos_ < text_.size()) {
      char c = text_[pos_];
      if (c == ' ' || c == '\t' || c == '\n' || c == '\r') {
        ++pos_;
      } else {
        break;
      }
    }
  }

  bool literal(std::string_view word) {
    if (text_.substr(pos_, word.size()) != word) return false;
    pos_ += word.size();
    return true;
  }

  std::optional<Json> parse_value() {
    if (pos_ >= text_.size()) {
      fail("unexpected end of input");
      return std::nullopt;
    }
    switch (text_[pos_]) {
      case '{': return parse_object();
      case '[': return parse_array();
      case '"': {
        auto s = parse_string();
        if (!s) return std::nullopt;
        return Json(*s);
      }
      case 't':
        if (literal("true")) return Json(true);
        fail("invalid literal");
        return std::nullopt;
      case 'f':
        if (literal("false")) return Json(false);
        fail("invalid literal");
        return std::nullopt;
      case 'n':
        if (literal("null")) return Json(nullptr);
        fail("invalid literal");
        return std::nullopt;
      default: return parse_number();
    }
  }

  std::optional<Json> parse_object() {
    ++pos_;  // {
    Json::Object object;
    skip_space();
    if (pos_ < text_.size() && text_[pos_] == '}') {
      ++pos_;
      return Json(std::move(object));
    }
    while (true) {
      skip_space();
      if (pos_ >= text_.size() || text_[pos_] != '"') {
        fail("expected a key");
        return std::nullopt;
      }
      auto key = parse_string();
      if (!key) return std::nullopt;
      skip_space();
      if (pos_ >= text_.size() || text_[pos_] != ':') {
        fail("expected ':'");
        return std::nullopt;
      }
      ++pos_;
      skip_space();
      auto value = parse_value();
      if (!value) return std::nullopt;
      object[*key] = std::move(*value);
      skip_space();
      if (pos_ < text_.size() && text_[pos_] == ',') {
        ++pos_;
        continue;
      }
      if (pos_ < text_.size() && text_[pos_] == '}') {
        ++pos_;
        return Json(std::move(object));
      }
      fail("expected ',' or '}'");
      return std::nullopt;
    }
  }

  std::optional<Json> parse_array() {
    ++pos_;  // [
    Json::Array array;
    skip_space();
    if (pos_ < text_.size() && text_[pos_] == ']') {
      ++pos_;
      return Json(std::move(array));
    }
    while (true) {
      skip_space();
      auto value = parse_value();
      if (!value) return std::nullopt;
      array.push_back(std::move(*value));
      skip_space();
      if (pos_ < text_.size() && text_[pos_] == ',') {
        ++pos_;
        continue;
      }
      if (pos_ < text_.size() && text_[pos_] == ']') {
        ++pos_;
        return Json(std::move(array));
      }
      fail("expected ',' or ']'");
      return std::nullopt;
    }
  }

  std::optional<std::uint32_t> parse_hex4() {
    if (pos_ + 4 > text_.size()) {
      fail("truncated \\u escape");
      return std::nullopt;
    }
    std::uint32_t value = 0;
    for (int i = 0; i < 4; ++i) {
      char c = text_[pos_++];
      value <<= 4;
      if (c >= '0' && c <= '9') {
        value |= static_cast<std::uint32_t>(c - '0');
      } else if (c >= 'a' && c <= 'f') {
        value |= static_cast<std::uint32_t>(c - 'a' + 10);
      } else if (c >= 'A' && c <= 'F') {
        value |= static_cast<std::uint32_t>(c - 'A' + 10);
      } else {
        fail("bad hex digit in \\u escape");
        return std::nullopt;
      }
    }
    return value;
  }

  std::optional<std::string> parse_string() {
    ++pos_;  // opening quote
    std::string out;
    while (true) {
      if (pos_ >= text_.size()) {
        fail("unterminated string");
        return std::nullopt;
      }
      char c = text_[pos_];
      if (c == '"') {
        ++pos_;
        return out;
      }
      if (static_cast<unsigned char>(c) < 0x20) {
        fail("raw control character in string");
        return std::nullopt;
      }
      if (c != '\\') {
        out.push_back(c);
        ++pos_;
        continue;
      }
      ++pos_;  // backslash
      if (pos_ >= text_.size()) {
        fail("unterminated escape");
        return std::nullopt;
      }
      char esc = text_[pos_++];
      switch (esc) {
        case '"': out.push_back('"'); break;
        case '\\': out.push_back('\\'); break;
        case '/': out.push_back('/'); break;
        case 'b': out.push_back('\b'); break;
        case 'f': out.push_back('\f'); break;
        case 'n': out.push_back('\n'); break;
        case 'r': out.push_back('\r'); break;
        case 't': out.push_back('\t'); break;
        case 'u': {
          auto code = parse_hex4();
          if (!code) return std::nullopt;
          std::uint32_t value = *code;
          // a high surrogate has to be followed by its low half
          if (value >= 0xD800 && value <= 0xDBFF) {
            if (pos_ + 1 < text_.size() && text_[pos_] == '\\' && text_[pos_ + 1] == 'u') {
              pos_ += 2;
              auto low = parse_hex4();
              if (!low) return std::nullopt;
              if (*low >= 0xDC00 && *low <= 0xDFFF) {
                value = 0x10000 + ((value - 0xD800) << 10) + (*low - 0xDC00);
              } else {
                fail("lone high surrogate");
                return std::nullopt;
              }
            } else {
              fail("lone high surrogate");
              return std::nullopt;
            }
          } else if (value >= 0xDC00 && value <= 0xDFFF) {
            fail("lone low surrogate");
            return std::nullopt;
          }
          append_utf8(out, value);
          break;
        }
        default:
          fail("unknown escape");
          return std::nullopt;
      }
    }
  }

  std::optional<Json> parse_number() {
    std::size_t start = pos_;
    if (pos_ < text_.size() && text_[pos_] == '-') ++pos_;
    if (pos_ >= text_.size() || !std::isdigit(static_cast<unsigned char>(text_[pos_]))) {
      fail("expected a number");
      return std::nullopt;
    }
    // json forbids leading zeros
    if (text_[pos_] == '0') {
      ++pos_;
    } else {
      while (pos_ < text_.size() && std::isdigit(static_cast<unsigned char>(text_[pos_]))) ++pos_;
    }

    bool integral = true;
    if (pos_ < text_.size() && text_[pos_] == '.') {
      integral = false;
      ++pos_;
      if (pos_ >= text_.size() || !std::isdigit(static_cast<unsigned char>(text_[pos_]))) {
        fail("expected a digit after '.'");
        return std::nullopt;
      }
      while (pos_ < text_.size() && std::isdigit(static_cast<unsigned char>(text_[pos_]))) ++pos_;
    }
    if (pos_ < text_.size() && (text_[pos_] == 'e' || text_[pos_] == 'E')) {
      integral = false;
      ++pos_;
      if (pos_ < text_.size() && (text_[pos_] == '+' || text_[pos_] == '-')) ++pos_;
      if (pos_ >= text_.size() || !std::isdigit(static_cast<unsigned char>(text_[pos_]))) {
        fail("expected a digit in the exponent");
        return std::nullopt;
      }
      while (pos_ < text_.size() && std::isdigit(static_cast<unsigned char>(text_[pos_]))) ++pos_;
    }

    std::string raw(text_.substr(start, pos_ - start));
    if (integral) {
      errno = 0;
      char* end = nullptr;
      long long value = std::strtoll(raw.c_str(), &end, 10);
      // an integer too big for int64 keeps its value as a double rather than
      // silently wrapping
      if (errno == 0 && end == raw.c_str() + raw.size()) {
        return Json(static_cast<std::int64_t>(value));
      }
    }
    return Json(std::strtod(raw.c_str(), nullptr));
  }
};

}  // namespace

std::optional<bool> Json::as_bool() const {
  if (type_ != Type::Bool) return std::nullopt;
  return bool_;
}

std::optional<std::int64_t> Json::as_int() const {
  if (type_ != Type::Int) return std::nullopt;
  return int_;
}

std::optional<double> Json::as_double() const {
  if (type_ == Type::Double) return double_;
  if (type_ == Type::Int) return static_cast<double>(int_);
  return std::nullopt;
}

std::optional<std::string> Json::as_string() const {
  if (type_ != Type::String) return std::nullopt;
  return string_;
}

const Json& Json::operator[](const std::string& key) const {
  if (type_ != Type::Object) return null_json();
  auto it = object_.find(key);
  return it == object_.end() ? null_json() : it->second;
}

const Json& Json::operator[](std::size_t index) const {
  if (type_ != Type::Array || index >= array_.size()) return null_json();
  return array_[index];
}

bool Json::contains(const std::string& key) const {
  return type_ == Type::Object && object_.count(key) > 0;
}

std::size_t Json::size() const {
  if (type_ == Type::Array) return array_.size();
  if (type_ == Type::Object) return object_.size();
  return 0;
}

bool Json::operator==(const Json& other) const {
  if (type_ != other.type_) return false;
  switch (type_) {
    case Type::Null: return true;
    case Type::Bool: return bool_ == other.bool_;
    case Type::Int: return int_ == other.int_;
    case Type::Double: return double_ == other.double_;
    case Type::String: return string_ == other.string_;
    case Type::Array: return array_ == other.array_;
    case Type::Object: return object_ == other.object_;
  }
  return false;
}

std::optional<Json> Json::parse(std::string_view text, std::string* error) {
  return Parser(text).run(error);
}

std::string json_quote(std::string_view raw) {
  std::string out = "\"";
  for (char c : raw) {
    switch (c) {
      case '"': out += "\\\""; break;
      case '\\': out += "\\\\"; break;
      case '\b': out += "\\b"; break;
      case '\f': out += "\\f"; break;
      case '\n': out += "\\n"; break;
      case '\r': out += "\\r"; break;
      case '\t': out += "\\t"; break;
      default:
        if (static_cast<unsigned char>(c) < 0x20) {
          char buf[7];
          std::snprintf(buf, sizeof(buf), "\\u%04x", static_cast<unsigned char>(c));
          out += buf;
        } else {
          out.push_back(c);
        }
    }
  }
  out += "\"";
  return out;
}

std::string Json::dump() const {
  switch (type_) {
    case Type::Null: return "null";
    case Type::Bool: return bool_ ? "true" : "false";
    case Type::Int: return std::to_string(int_);
    case Type::Double: {
      if (std::isfinite(double_)) {
        // shortest round-tripping form, without a trailing .0 for integral values
        char buf[40];
        std::snprintf(buf, sizeof(buf), "%.17g", double_);
        double back = std::strtod(buf, nullptr);
        if (back != double_) return buf;
        for (int precision = 1; precision < 17; ++precision) {
          std::snprintf(buf, sizeof(buf), "%.*g", precision, double_);
          if (std::strtod(buf, nullptr) == double_) break;
        }
        return buf;
      }
      return "null";  // json has no infinity or nan
    }
    case Type::String: return json_quote(string_);
    case Type::Array: {
      std::string out = "[";
      for (std::size_t i = 0; i < array_.size(); ++i) {
        if (i) out += ",";
        out += array_[i].dump();
      }
      return out + "]";
    }
    case Type::Object: {
      std::string out = "{";
      bool first = true;
      for (const auto& [key, value] : object_) {
        if (!first) out += ",";
        first = false;
        out += json_quote(key) + ":" + value.dump();
      }
      return out + "}";
    }
  }
  return "null";
}

}  // namespace ryskv12
