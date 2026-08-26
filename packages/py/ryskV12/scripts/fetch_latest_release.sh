#!/bin/bash

GITHUB_REPO="${RYSK_CLI_REPO:-rysk-finance/ryskV12}"
CLI_TAG_PREFIX="${RYSK_CLI_TAG_PREFIX:-cli-v}"

# Function to fetch the latest CLI release from GitHub API
# The monorepo publishes releases for several packages, so /releases/latest is
# not necessarily the CLI. Pick the newest release tagged "${CLI_TAG_PREFIX}*".
get_latest_release() {
  curl -s -H "User-Agent: curl" "https://api.github.com/repos/${GITHUB_REPO}/releases?per_page=50" |
    jq -c "[.[] | select(.tag_name | startswith(\"${CLI_TAG_PREFIX}\"))] | first // empty"
}

# Function to get the architecture-specific asset download URL
get_arch_specific_asset() {
  arch="$(uname -m)"

  release_json=$(get_latest_release)
  if [[ -z "$release_json" ]]; then
    echo "Error: Failed to fetch latest release."
    return 1
  fi

  if [[ "$arch" == "x86_64" || "$arch" == "amd64" ]]; then
    asset_name_pattern="ryskV[0-9]+-linux-amd64"
    elif [[ "$arch" == "aarch64" || "$arch" == "arm64" ]]; then
    asset_name_pattern="ryskV[0-9]+-linux-arm64"
  else
    echo "Error: Unsupported architecture: $arch"
    return 1
  fi

  download_url=$(echo "$release_json" | jq -r ".assets[] | select(.name | test(\"$asset_name_pattern\")) | .browser_download_url")

  if [[ -z "$download_url" ]]; then
    echo "Error: No matching asset found for your architecture."
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

main