#pragma once

namespace ryskv12 {

/// The download script `setup()` runs, embedded so the installed library does
/// not depend on a repo checkout. CMakeLists.txt generates it from
/// scripts/fetch_latest_release.sh at configure time.
extern const char* const kFetchScript;

}  // namespace ryskv12
