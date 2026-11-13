#!/bin/sh
# Prepare reproducible local release artifacts. Publishing/tagging is explicit.
set -eu
version=${1:?usage: scripts/release.sh vMAJOR.MINOR.PATCH}
case "$version" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) echo 'expected vMAJOR.MINOR.PATCH' >&2; exit 1 ;;
esac
if ! printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then exit 1; fi
make check
make lint
release_dir="dist/$version"
mkdir -p "$release_dir"
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -o "$release_dir/nudge-server-linux-$arch" ./cmd/server
done
cp LICENSE CHANGELOG.md "$release_dir/"
(cd "$release_dir" && shasum -a 256 nudge-server-* > SHA256SUMS)
printf 'Prepared %s; review before tagging or publishing.\n' "$release_dir"
