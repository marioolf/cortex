#!/bin/sh
# Builds every release asset into OUTDIR: one archive per platform, the browser
# extension, the two installers, and checksums.txt. Run from anywhere; the release workflow calls it.
#
#   scripts/release.sh 1.2.3 dist
#
# Needs go, git, zip, GNU tar, gzip and sha256sum. Nothing is signed. The archives
# are reproducible (see SOURCE_DATE_EPOCH below).
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
# The Windows resources (icon, version information) live in cmd/cortex/*.syso;
# the committed ones are for version 0.0.0, so the release version is written
# over them below and the committed files are put back on exit.
syso="rsrc_windows_amd64.syso rsrc_windows_arm64.syso"
mkdir "$work/syso"
for f in $syso; do cp "$root/cmd/cortex/$f" "$work/syso/$f"; done
trap 'for f in $syso; do cp "$work/syso/$f" "$root/cmd/cortex/$f"; done; rm -rf "$work"' EXIT

# Reproducible archives: every timestamp is SOURCE_DATE_EPOCH (default: the
# commit time of HEAD), entries are sorted, owners are zero and the time zone is
# fixed, so the same commit gives byte-identical files.
export LC_ALL=C TZ=UTC
if [ -z "${SOURCE_DATE_EPOCH:-}" ]; then
  SOURCE_DATE_EPOCH=$(git -C "$root" log -1 --format=%ct HEAD) || { echo "cannot read the commit time; set SOURCE_DATE_EPOCH" >&2; exit 1; }
fi
export SOURCE_DATE_EPOCH

cd "$root"

go run ./tools/winres -ico winres/cortex.ico -version "$version" -out-dir cmd/cortex

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
  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$stage/$bin" ./cmd/cortex
  cp README.md LICENSE THIRD_PARTY_LICENSES.md "$stage/"
  chmod 755 "$stage/$bin"
  chmod 644 "$stage/README.md" "$stage/LICENSE" "$stage/THIRD_PARTY_LICENSES.md"
  touch -d "@$SOURCE_DATE_EPOCH" "$stage/$bin" "$stage/README.md" "$stage/LICENSE" "$stage/THIRD_PARTY_LICENSES.md"

  rm -f "$out/$name.zip" "$out/$name.tar.gz"
  if [ "$goos" = windows ]; then
    (cd "$stage" && zip -q -X "$out/$name.zip" "$bin" README.md LICENSE THIRD_PARTY_LICENSES.md)
  else
    tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$SOURCE_DATE_EPOCH" -C "$stage" -cf - \
      "$bin" LICENSE README.md THIRD_PARTY_LICENSES.md | gzip -n -9 >"$out/$name.tar.gz"
  fi
done

# The extension is loaded unpacked, so it ships as the folder, without its tests.
# Chrome wants 1 to 4 dot-separated integers as the manifest version, so the
# release version loses any -suffix; the copy in the repo is not touched.
echo "packaging cortex-extension_$version.zip"
manifest_version=${version%%[-+]*}
case $manifest_version in
  "" | *[!0-9.]* | .* | *. | *..*) echo "version $version has no usable extension version ($manifest_version)" >&2; exit 2 ;;
esac
case $(printf '%s' "$manifest_version" | tr -cd .) in
  "" | . | .. | ...) ;;
  *) echo "extension version $manifest_version has more than 4 parts" >&2; exit 2 ;;
esac
ext=$work/ext
mkdir -p "$ext"
cp -R extension "$ext/extension"
rm -rf "$ext/extension/test"
sed -E 's/^([[:space:]]*"version"[[:space:]]*:[[:space:]]*")[^"]*(")/\1'"$manifest_version"'\2/' extension/manifest.json >"$ext/extension/manifest.json"
grep -q "\"version\": \"$manifest_version\"" "$ext/extension/manifest.json" || { echo "could not set the extension version" >&2; exit 1; }
find "$ext/extension" -exec touch -d "@$SOURCE_DATE_EPOCH" {} +
rm -f "$out/cortex-extension_$version.zip"
(cd "$ext" && find extension -type f | sort | zip -q -X -D "$out/cortex-extension_$version.zip" -@)

# The installers are attached as they are, so that
# .../releases/latest/download/install.sh works too.
cp install.sh install.ps1 "$out/"
chmod 755 "$out/install.sh"
chmod 644 "$out/install.ps1"

(cd "$out" && sha256sum cortex_"$version"_* cortex-extension_"$version".zip install.sh install.ps1 > checksums.txt)
echo "done: $out"
