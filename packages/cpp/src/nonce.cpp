#include "ryskv12/nonce.hpp"

#include <chrono>
#include <fstream>
#include <stdexcept>

namespace ryskv12 {

NonceCounter::NonceCounter(std::string path) : path_(std::move(path)) {
  std::ifstream file(path_);
  if (!file) return;  // no counter yet, the clock takes over
  std::string raw;
  file >> raw;
  try {
    last_ = std::stoull(raw);
  } catch (const std::exception&) {
    last_ = 0;  // an unreadable counter is the same as none
  }
}

std::string NonceCounter::next() {
  const auto now = std::chrono::duration_cast<std::chrono::milliseconds>(
                       std::chrono::system_clock::now().time_since_epoch())
                       .count();
  std::uint64_t nonce = static_cast<std::uint64_t>(now);
  if (nonce <= last_) nonce = last_ + 1;

  std::ofstream file(path_, std::ios::trunc);
  if (!file) throw std::runtime_error("could not open the nonce counter " + path_);
  file << nonce;
  file.flush();
  if (!file) throw std::runtime_error("could not write the nonce counter " + path_);

  last_ = nonce;
  return std::to_string(nonce);
}

}  // namespace ryskv12
