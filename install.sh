#!/bin/sh
# Cortex installer for macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/marioolf/cortex/main/install.sh | sh
#
# Downloads the release archive for this machine, checks its SHA-256 against
# checksums.txt, puts `cortex` in ~/.local/bin and starts it once.
#
# Environment:
#   CORTEX_VERSION       a tag such as v0.1.0 (default: the latest release)
#   CORTEX_INSTALL_DIR   where to put the binary (default: $HOME/.local/bin)
#   CORTEX_NO_START=1    install only; do not start Cortex
#   CORTEX_DOWNLOAD_BASE (for tests) replaces https://github.com/marioolf/cortex/releases/download;
#                        CORTEX_VERSION must then be set
set -eu

REPO_URL=https://github.com/marioolf/cortex

say() { printf '%s\n' "$*"; }
die() { printf 'install.sh: error: %s\n' "$*" >&2; exit 1; }

# fetch URL OUTFILE: download with curl or wget.
fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  else
    wget -q -O "$2" "$1"
  fi
}

# latest_tag: the tag of the latest release, read from the redirect of
# /releases/latest (no GitHub API, so no rate limit).
latest_tag() {
  if command -v curl >/dev/null 2>&1; then
    loc=$(curl -fsS -o /dev/null -w '%{redirect_url}' "$REPO_URL/releases/latest") || return 1
  else
    loc=$(wget --max-redirect=0 -S -O /dev/null "$REPO_URL/releases/latest" 2>&1 |
      sed -n 's/^ *[Ll]ocation: *//p' | tr -d '\r' | head -n 1) || return 1
  fi
  tag=${loc##*/tag/}
  case $loc in */tag/*) ;; *) return 1 ;; esac
  printf '%s\n' "$tag"
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d ' ' -f 1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d ' ' -f 1
  else
    die "need sha256sum or shasum to verify the download"
  fi
}

main() {
  if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
    die "need curl or wget"
  fi
  command -v tar >/dev/null 2>&1 || die "need tar"

  case $(uname -s) in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) die "unsupported system '$(uname -s)': this installer supports macOS and Linux (on Windows use install.ps1)" ;;
  esac
  case $(uname -m) in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) die "unsupported processor '$(uname -m)': Cortex is built for amd64 and arm64" ;;
  esac

  base=${CORTEX_DOWNLOAD_BASE:-$REPO_URL/releases/download}
  tag=${CORTEX_VERSION:-}
  if [ -z "$tag" ]; then
    [ -z "${CORTEX_DOWNLOAD_BASE:-}" ] || die "CORTEX_VERSION must be set when CORTEX_DOWNLOAD_BASE is"
    say "Looking up the latest release..."
    tag=$(latest_tag) || die "could not find the latest release (is there one yet? see $REPO_URL/releases); set CORTEX_VERSION=v0.1.0 to pick one"
  fi
  case $tag in v*) ;; *) tag=v$tag ;; esac
  version=${tag#v}
  case $version in
    "" | *[!A-Za-z0-9._+-]*) die "invalid version '$tag'" ;;
  esac

  asset=cortex_${version}_${os}_${arch}.tar.gz
  dir=${CORTEX_INSTALL_DIR:-${HOME:?HOME is not set}/.local/bin}

  tmp=$(mktemp -d "${TMPDIR:-/tmp}/cortex-install.XXXXXX") || die "could not create a temporary folder"
  trap 'rm -rf "$tmp"' EXIT INT TERM HUP

  say "Downloading Cortex $version for $os/$arch..."
  fetch "$base/$tag/$asset" "$tmp/$asset" || die "could not download $base/$tag/$asset (does release $tag exist?)"
  fetch "$base/$tag/checksums.txt" "$tmp/checksums.txt" || die "could not download $base/$tag/checksums.txt"

  want=$(awk -v f="$asset" '$2 == f { print $1; exit }' "$tmp/checksums.txt")
  [ -n "$want" ] || die "$asset is not listed in checksums.txt"
  got=$(sha256_of "$tmp/$asset")
  if [ "$got" != "$want" ]; then
    die "checksum mismatch for $asset (expected $want, got $got): nothing was installed"
  fi
  say "Checksum OK (sha256 $got)."

  mkdir "$tmp/x"
  tar -xzf "$tmp/$asset" -C "$tmp/x" cortex || die "could not extract $asset"
  mkdir -p "$dir" || die "could not create $dir"
  # Copy beside the destination, then rename, so a running Cortex is replaced
  # atomically and a failed copy leaves the old file alone.
  cp "$tmp/x/cortex" "$dir/.cortex.new.$$" || die "could not write to $dir"
  chmod 755 "$dir/.cortex.new.$$"
  mv -f "$dir/.cortex.new.$$" "$dir/cortex"

  say ""
  say "Installed Cortex $version to $dir/cortex"

  case ":${PATH:-}:" in
    *":$dir:"*) ;;
    *)
      case ${SHELL:-} in
        */zsh) line="echo 'export PATH=\"$dir:\$PATH\"' >> ~/.zshrc" ;;
        */fish) line="fish_add_path $dir" ;;
        */bash) line="echo 'export PATH=\"$dir:\$PATH\"' >> ~/.bashrc" ;;
        *) line="echo 'export PATH=\"$dir:\$PATH\"' >> ~/.profile" ;;
      esac
      say ""
      say "$dir is not on your PATH. To run 'cortex' by name, add it (then open a new terminal):"
      say "  $line"
      ;;
  esac

  if [ -n "${CORTEX_NO_START:-}" ]; then
    say ""
    say "Not starting Cortex (CORTEX_NO_START is set). Start it with: $dir/cortex"
  else
    # The first run creates the config, registers Cortex to start at login for
    # this exact file (a launch agent on macOS, an XDG autostart entry on Linux)
    # and opens the panel in the browser. If another Cortex is already running
    # from a different file, this one asks it to quit and takes its place.
    nohup "$dir/cortex" run </dev/null >/dev/null 2>&1 &
    say ""
    say "Started Cortex. Its control panel opens in your browser."
    say "On the first start it also registers itself to start at login (turn that off in the panel)."
    say "If a Cortex was already running from another file, this new one takes over from it."
  fi

  say ""
  say "Control panel:  http://127.0.0.1:47821/"
  say "Next: open the vault folder (~/Cortex) in Obsidian and use the panel's \"Connect AI tools\" tab."
  say "Uninstall:     turn off \"Start Cortex when I log in\" in the panel, quit Cortex, then: rm $dir/cortex"
}

main "$@"
