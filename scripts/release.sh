#!/bin/sh
# Builds every release asset into OUTDIR: one archive per platform, the browser
# extension, and checksums.txt. Run from anywhere; the release workflow calls it.
#
#   scripts/release.sh 1.2.3 dist
#
# Needs go, zip, tar and sha256sum. Nothing is signed.
set -eu

[ $# -eq 2 ] || { echo "usage: $0 VERSION OUTDIR" >&2; exit 2; }
version=${1#v} # accept v1.2.3 too
case $version in
  "" | *[!A-Za-z0-9._+-]*) echo "invalid version: $1" >&2; exit 2 ;;
esac

root=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$2"
out=$(cd "$2" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

cd "$root"
for goos_arch in windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
  goos=${goos_arch%/*}
  goarch=${goos_arch#*/}
  name=cortex_${version}_${goos}_${goarch}
  stage=$work/$name
  mkdir -p "$stage"

  ldflags="-s -w -X main.version=$version"
  bin=cortex
  if [ "$goos" = windows ]; then
    bin=cortex.exe
    ldflags="$ldflags -H windowsgui" # no console window at login or double click
  fi
  echo "building $name"
  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -ldflags "$ldflags" -o "$stage/$bin" ./cmd/cortex
  cp README.md LICENSE "$stage/"

  if [ "$goos" = windows ]; then
    (cd "$stage" && zip -q -X "$out/$name.zip" "$bin" README.md LICENSE)
  else
    tar -C "$stage" -czf "$out/$name.tar.gz" "$bin" README.md LICENSE
  fi
done

# The extension is loaded unpacked, so it ships as the folder, without its tests.
echo "packaging cortex-extension_$version.zip"
(cd "$root" && zip -q -r -X "$out/cortex-extension_$version.zip" extension -x 'extension/test/*')

(cd "$out" && sha256sum cortex_"$version"_* cortex-extension_"$version".zip > checksums.txt)
echo "done: $out"
