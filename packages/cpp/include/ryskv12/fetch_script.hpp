#pragma once

namespace ryskv12 {

/// The download script `setup()` runs, embedded so the installed library does
/// not depend on a repo checkout. Generated from scripts/fetch_latest_release.sh;
/// a test asserts the two have not drifted apart.
extern const char* const kFetchScript;

}  // namespace ryskv12
