#!/bin/bash
set -e

VERSION="${VERSION:-dev}"

# Define the target platforms
platforms=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
)

# Build for each platform
for platform in "${platforms[@]}"; do
  os=$(echo "$platform" | cut -d'/' -f1)
  arch=$(echo "$platform" | cut -d'/' -f2)
  output="ryskV12-$os-$arch"

  echo "Building for $os/$arch..."
  # CGO_ENABLED=0 for every target, not just the cross compiled ones: a native
  # build defaults to cgo, which links the host's libc through net's resolver and
  # produces a binary that will not run on a different distro. Cross compilation
  # turns cgo off on its own, which is why only the host target came out dynamic.
  # -trimpath keeps build paths out of the binary.
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
    -trimpath \
    -ldflags="-X 'main.Version=${VERSION}'" \
    -o "$output"
  echo "Build for $os/$arch successful: $output"
done

echo "All builds completed."
