#pragma once

// Internal: not installed with the public headers.

#include <string>

namespace ryskv12 {

/// `raw` without leading or trailing whitespace.
inline std::string trim(const std::string& raw) {
  std::size_t begin = raw.find_first_not_of(" \t\n\r");
  if (begin == std::string::npos) return "";
  std::size_t end = raw.find_last_not_of(" \t\n\r");
  return raw.substr(begin, end - begin + 1);
}

}  // namespace ryskv12
