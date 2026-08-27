#pragma once

// A test harness small enough to not be a dependency.
//
// gtest or catch2 would each be larger than this sdk. TEST registers a case,
// CHECK records a failure and keeps going so one run reports everything, and
// HARNESS_MAIN makes the file an executable ctest can run.

#include <cstdint>
#include <functional>
#include <iostream>
#include <map>
#include <optional>
#include <sstream>
#include <string>
#include <vector>

namespace harness {

struct Case {
  std::string name;
  std::function<void()> body;
};

inline std::vector<Case>& registry() {
  static std::vector<Case> cases;
  return cases;
}

inline int& failures() {
  static int count = 0;
  return count;
}

struct Register {
  Register(std::string name, std::function<void()> body) {
    registry().push_back({std::move(name), std::move(body)});
  }
};

// ---- describing values, so a failure says what it actually got -------------

// A type with no describe() of its own still compares; it just cannot print
// itself. Anything exposing dump() (this sdk's Json) prints through that.
template <typename T>
std::string describe(const T&) {
  return "<value>";
}

template <typename T>
auto describe(const T& v) -> decltype(v.dump()) {
  return v.dump();
}

inline std::string describe(const std::string& v) { return "\"" + v + "\""; }
inline std::string describe(const char* v) { return describe(std::string(v)); }
inline std::string describe(bool v) { return v ? "true" : "false"; }
inline std::string describe(std::int64_t v) { return std::to_string(v); }
inline std::string describe(int v) { return std::to_string(v); }
inline std::string describe(std::size_t v) { return std::to_string(v); }

template <typename T>
std::string describe(const std::optional<T>& v) {
  return v ? "some(" + describe(*v) + ")" : "none";
}

template <typename T>
std::string describe(const std::vector<T>& v) {
  std::string out = "[";
  for (std::size_t i = 0; i < v.size(); ++i) {
    if (i) out += ", ";
    out += describe(v[i]);
  }
  return out + "]";
}

inline void record(bool ok, const std::string& expression, const std::string& detail,
                   const char* file, int line) {
  if (ok) return;
  ++failures();
  std::cerr << "  FAIL " << file << ":" << line << "  " << expression;
  if (!detail.empty()) std::cerr << "\n       " << detail;
  std::cerr << "\n";
}

inline int run_all() {
  int failed_cases = 0;
  for (const Case& c : registry()) {
    int before = failures();
    try {
      c.body();
    } catch (const std::exception& e) {
      ++failures();
      std::cerr << "  FAIL " << c.name << " threw: " << e.what() << "\n";
    }
    bool ok = failures() == before;
    if (!ok) ++failed_cases;
    std::cout << (ok ? "ok   " : "FAIL ") << c.name << "\n";
  }
  std::cout << registry().size() - static_cast<std::size_t>(failed_cases) << "/"
            << registry().size() << " passed\n";
  return failed_cases == 0 ? 0 : 1;
}

}  // namespace harness

#define TEST(name)                                                  \
  static void name();                                               \
  static harness::Register harness_register_##name(#name, name);    \
  static void name()

#define CHECK(cond) harness::record((cond), #cond, "", __FILE__, __LINE__)

#define CHECK_MSG(cond, detail) harness::record((cond), #cond, (detail), __FILE__, __LINE__)

#define CHECK_EQ(actual, expected)                                            \
  do {                                                                        \
    auto harness_a = (actual);  /* by value: .value() on a temporary would dangle */ \
    auto harness_b = (expected);                                              \
    harness::record(harness_a == harness_b, #actual " == " #expected,         \
                    "got " + harness::describe(harness_a) + ", wanted " +     \
                        harness::describe(harness_b),                         \
                    __FILE__, __LINE__);                                      \
  } while (0)

/// Records a failure when `body` does not throw an Error whose message contains
/// `needle`. The other sdks assert on these messages too.
#define CHECK_THROWS(body, needle)                                                        \
  do {                                                                                    \
    bool harness_threw = false;                                                           \
    std::string harness_what;                                                             \
    try {                                                                                 \
      body;                                                                               \
    } catch (const std::exception& e) {                                                   \
      harness_threw = true;                                                               \
      harness_what = e.what();                                                            \
    }                                                                                     \
    harness::record(harness_threw && harness_what.find(needle) != std::string::npos,       \
                    #body " throws " needle,                                              \
                    harness_threw ? "threw \"" + harness_what + "\"" : "did not throw",   \
                    __FILE__, __LINE__);                                                  \
  } while (0)

#define HARNESS_MAIN() \
  int main() { return harness::run_all(); }
