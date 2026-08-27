#include "ryskv12/fetch_script.hpp"

namespace ryskv12 {

const char* const kFetchScript = R"SCRIPT(#!/bin/bash

GITHUB_REPO="${RYSK_CLI_REPO:-rysk-finance/sdks}"
CLI_TAG_PREFIX="${RYSK_CLI_TAG_PREFIX:-cli-v}"

# Function to fetch the latest CLI release from GitHub API
# The monorepo publishes releases for several packages, so /releases/latest is
# not necessarily the CLI. Pick the newest release tagged "${CLI_TAG_PREFIX}*".
get_latest_release() {
  response=$(curl -s -H "User-Agent: curl" "https://api.github.com/repos/${GITHUB_REPO}/releases?per_page=50")

  # a missing repo or a rate limit answers with an object, not a list of releases
  if ! echo "$response" | jq -e 'type == "array"' >/dev/null 2>&1; then
    echo "Error: unexpected response for ${GITHUB_REPO}: $(echo "$response" | jq -r '.message // .' 2>/dev/null | head -1)" >&2
    echo "Error: set RYSK_CLI_REPO to the repo that publishes the cli releases." >&2
    return 1
  fi

  echo "$response" | jq -c "[.[] | select(.tag_name | startswith(\"${CLI_TAG_PREFIX}\"))] | first // empty"
}

# Function to get the architecture-specific asset download URL
get_arch_specific_asset() {
  os="$(uname -s)"
  arch="$(uname -m)"

  if [[ "$os" == "Linux" ]]; then
    os_name="linux"
  elif [[ "$os" == "Darwin" ]]; then
    os_name="darwin"
  else
    echo "Error: Unsupported OS: $os"
    return 1
  fi

  if [[ "$arch" == "x86_64" || "$arch" == "amd64" ]]; then
    arch_name="amd64"
  elif [[ "$arch" == "aarch64" || "$arch" == "arm64" ]]; then
    arch_name="arm64"
  else
    echo "Error: Unsupported architecture: $arch"
    return 1
  fi

  asset_name_pattern="ryskV[0-9]+-${os_name}-${arch_name}"

  release_json=$(get_latest_release) || return 1
  if [[ -z "$release_json" ]]; then
    echo "Error: ${GITHUB_REPO} has no release tagged ${CLI_TAG_PREFIX}*." >&2
    return 1
  fi

  download_url=$(echo "$release_json" | jq -r ".assets[] | select(.name | test(\"$asset_name_pattern\")) | .browser_download_url")

  if [[ -z "$download_url" ]]; then
    echo "Error: No matching asset found for $os_name/$arch_name (expected '$asset_name_pattern')."
    return 1
  fi

  echo "$download_url"
}

# Main script
main() {

  download_url=$(get_arch_specific_asset)

  if [[ -n "$download_url" ]]; then
    echo "Download URL: $download_url"
    echo "Downloading to: ryskV12"
    curl -L -o "ryskV12" "$download_url"
    if [[ $? -eq 0 ]]; then
      echo "Download complete!"
    else
      echo "Download failed!"
      return 1
    fi
  else
    return 1 #error already printed in get_arch_specific_asset
  fi
}

main)SCRIPT";

}  // namespace ryskv12
