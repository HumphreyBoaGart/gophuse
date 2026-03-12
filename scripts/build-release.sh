#!/bin/sh
set -eu

# Build statically linked release binaries into bin/ using only repository-local
# Go caches. Keeping GOPATH/GOCACHE/GOMODCACHE inside the repo makes builds
# reproducible across clean environments and avoids polluting a maintainer's
# global Go workspace.
ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
BIN_DIR="$ROOT_DIR/bin"
CACHE_DIR="$ROOT_DIR/.cache/go-build"
MODCACHE_DIR="$ROOT_DIR/.cache/go-mod"
GOPATH_DIR="$ROOT_DIR/.gopath"
PKG="./cmd/gophuse"
CHECKSUM_FILE="$BIN_DIR/SHA256SUMS"

# The default release set intentionally targets the three Linux architectures
# we currently plan to publish on GitHub releases.
TARGETS="${TARGETS:-linux/amd64 linux/arm64 linux/riscv64}"

mkdir -p "$BIN_DIR" "$CACHE_DIR" "$MODCACHE_DIR" "$GOPATH_DIR"
# Regenerate checksums from scratch on every release build so the file reflects
# only the artifacts produced by this invocation.
rm -f "$CHECKSUM_FILE"

for target in $TARGETS; do
  goos="${target%/*}"
  goarch="${target#*/}"
  output="$BIN_DIR/gophuse-${goarch}"

  echo "building $output"
  # CGO is disabled so the outputs remain self-contained static binaries.
  GOOS="$goos" \
  GOARCH="$goarch" \
  CGO_ENABLED=0 \
  GOPATH="$GOPATH_DIR" \
  GOCACHE="$CACHE_DIR" \
  GOMODCACHE="$MODCACHE_DIR" \
  go build -trimpath -ldflags='-s -w' -o "$output" "$PKG"
  # Publish checksums alongside the release assets so end users can verify the
  # downloaded binaries without rebuilding locally.
  sha256sum "$output" >> "$CHECKSUM_FILE"
done
