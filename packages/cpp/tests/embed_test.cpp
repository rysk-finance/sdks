// setup() runs a copy of the download script embedded in the binary, so an
// installed library needs no repo checkout. That copy is generated from
// scripts/fetch_latest_release.sh, and nothing stops the two drifting apart
// except this.

#include <fstream>
#include <sstream>
#include <string>

#include "harness.hpp"
#include "ryskv12/fetch_script.hpp"

#ifndef RYSK_SCRIPT_PATH
#error "RYSK_SCRIPT_PATH must point at scripts/fetch_latest_release.sh"
#endif

TEST(the_embedded_script_matches_the_one_on_disk) {
  std::ifstream file(RYSK_SCRIPT_PATH);
  CHECK_MSG(file.good(), "could not read " RYSK_SCRIPT_PATH);
  if (!file.good()) return;

  std::ostringstream buffer;
  buffer << file.rdbuf();
  std::string on_disk = buffer.str();
  std::string embedded = ryskv12::kFetchScript;

  CHECK_MSG(on_disk == embedded,
            "src/fetch_script.cpp has drifted from scripts/fetch_latest_release.sh; "
            "regenerate it");
}

TEST(the_embedded_script_still_looks_like_the_downloader) {
  std::string embedded = ryskv12::kFetchScript;
  CHECK(embedded.find("RYSK_CLI_REPO") != std::string::npos);
  CHECK(embedded.find("rysk-finance/sdks") != std::string::npos);
  CHECK(embedded.find("cli-v") != std::string::npos);
}

HARNESS_MAIN()
