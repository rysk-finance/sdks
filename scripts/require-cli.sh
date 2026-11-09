#!/usr/bin/env bash
# usage: require-cli.sh <min cli version> <owner/repo>
#
# The sdks download the newest cli-v* release at install time. If that is older
# than the version an sdk demands -- or carries no binaries -- every fresh
# install is broken, so the sdk release workflows refuse to publish into that.
# gh reads its token from GH_TOKEN.
set -eo pipefail

min=${1:?usage: require-cli.sh <min cli version> <owner/repo>}
repo=${2:?usage: require-cli.sh <min cli version> <owner/repo>}

releases=$(gh api "repos/$repo/releases?per_page=100" \
  --jq '[.[] | select((.draft or .prerelease) == false) | select(.tag_name | startswith("cli-v"))]')

newest=$(echo "$releases" | jq -r '.[].tag_name' | sed 's/^cli-v//' | sort -V | tail -n 1)
if [ -z "$newest" ]; then
  echo "::error::no cli-v* release published; run the Release CLI workflow at $min first."
  exit 1
fi

if [ "$(printf '%s\n%s\n' "$min" "$newest" | sort -V | head -n 1)" != "$min" ]; then
  echo "::error::newest cli is $newest but this sdk needs $min or newer; release the cli first."
  exit 1
fi

missing=""
assets=$(echo "$releases" | jq -r --arg t "cli-v$newest" '.[] | select(.tag_name == $t) | .assets[].name')
for want in ryskV12-linux-amd64 ryskV12-linux-arm64 ryskV12-darwin-amd64 ryskV12-darwin-arm64; do
  echo "$assets" | grep -qx "$want" || missing="$missing $want"
done
if [ -n "$missing" ]; then
  echo "::error::cli-v$newest is missing binaries:$missing"
  exit 1
fi

echo "cli-v$newest satisfies the required $min, with all four binaries."
