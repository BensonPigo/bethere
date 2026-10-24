#!/usr/bin/env bash
# Build a release and upload it. Release objects are never overwritten.
set -euo pipefail
source "$(dirname "$0")/env.sh"
cd "$(dirname "$0")/../.."

make release
VERSION="$(cat build/release/LATEST)"
gc storage cp --no-clobber "build/release/$VERSION/bethere" "gs://$RELEASE_BUCKET/bethere/$VERSION/bethere"

# Mirror the pinned Caddy so recovery does not depend on GitHub.
CADDY_OBJ="gs://$RELEASE_BUCKET/caddy/$CADDY_VERSION/caddy"
if ! gc storage objects describe "$CADDY_OBJ" >/dev/null 2>&1; then
  tmp="$(mktemp -d)"
  base="https://github.com/caddyserver/caddy/releases/download/v$CADDY_VERSION"
  tarball="caddy_${CADDY_VERSION}_linux_amd64.tar.gz"
  curl -fsSL -o "$tmp/$tarball" "$base/$tarball"
  curl -fsSL -o "$tmp/checksums.txt" "$base/caddy_${CADDY_VERSION}_checksums.txt"
  (cd "$tmp" && grep " $tarball\$" checksums.txt | shasum -a 512 -c -)
  tar -xzf "$tmp/$tarball" -C "$tmp" caddy
  gc storage cp --no-clobber "$tmp/caddy" "$CADDY_OBJ"
  rm -rf "$tmp"
fi

# Mirror the pinned Litestream the same way.
LS_OBJ="gs://$RELEASE_BUCKET/litestream/$LITESTREAM_VERSION/litestream"
if ! gc storage objects describe "$LS_OBJ" >/dev/null 2>&1; then
  tmp="$(mktemp -d)"
  base="https://github.com/benbjohnson/litestream/releases/download/v$LITESTREAM_VERSION"
  tarball="litestream-${LITESTREAM_VERSION}-linux-x86_64.tar.gz"
  curl -fsSL -o "$tmp/$tarball" "$base/$tarball"
  curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"
  (cd "$tmp" && grep "  $tarball\$" checksums.txt | shasum -a 256 -c -)
  tar -xzf "$tmp/$tarball" -C "$tmp" litestream
  gc storage cp --no-clobber "$tmp/litestream" "$LS_OBJ"
  rm -rf "$tmp"
fi

echo "$VERSION"
