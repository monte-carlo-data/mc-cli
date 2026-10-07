#!/bin/sh
# Copyright Monte Carlo AI, Inc.
# SPDX-License-Identifier: Apache-2.0
#
# Installs the montecarlo CLI on macOS or Linux from a GitHub release of
# monte-carlo-data/mc-cli: downloads the archive for this platform, checks it against the
# release's checksums, and installs the binary.
#
#   MONTECARLO_VERSION        the release to install, 0.1.3 or v0.1.3; the latest by default
#   MONTECARLO_INSTALL_DIR    where to install; /usr/local/bin if writable, else ~/.local/bin
#   MONTECARLO_BIN_NAME       the installed name, montecarlo by default
#   MONTECARLO_FORCE=1        replace a montecarlo at the target that is not this CLI
#   MONTECARLO_DOWNLOAD_BASE  where releases are served from, for a mirror; the GitHub
#                             releases page by default
#
# With gh installed and logged in, the archive's provenance is verified too.
#
# Everything runs from main, called on the last line, so a download cut short runs nothing.

set -eu

repo=monte-carlo-data/mc-cli

say() { printf 'install.sh: %s\n' "$*" >&2; }
fail() {
  say "$*"
  exit 1
}

# fetch <url> <file>, or fetch <url> - to print where <url> redirects to.
fetch() {
  if command -v curl > /dev/null 2>&1; then
    if [ "$2" = - ]; then
      # shellcheck disable=SC2086 # $curl_flags is a list of flags.
      curl $curl_flags -fsSIL -o /dev/null -w '%{url_effective}' "$1"
    else
      # shellcheck disable=SC2086
      curl $curl_flags -fsSL -o "$2" "$1"
    fi
  elif command -v wget > /dev/null 2>&1; then
    if [ "$2" = - ]; then
      # wget exits non-zero on the redirect it is told not to follow; the Location is the answer.
      # shellcheck disable=SC2086
      wget $wget_flags --max-redirect=0 --spider -S "$1" 2>&1 | sed -n 's/^ *Location: *//p' | tail -1 | tr -d '\r'
    else
      # shellcheck disable=SC2086
      wget $wget_flags -q -O "$2" "$1"
    fi
  else
    fail "needs curl or wget to download the release"
  fi
}

sha256() {
  if command -v sha256sum > /dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum > /dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  else
    fail "needs sha256sum or shasum to check the download"
  fi
}

# This CLI's binary records its module path in its build info. Reading it, not running the
# file, guards against replacing another program by accident; it is not a tamper check.
is_ours() {
  LC_ALL=C grep -aq "github.com/$repo" "$1"
}

is_release_tag() {
  printf '%s\n' "$1" | grep -Eqx 'v[0-9]+\.[0-9]+\.[0-9]+'
}

main() {
  base=${MONTECARLO_DOWNLOAD_BASE:-https://github.com/$repo/releases}
  name=${MONTECARLO_BIN_NAME:-montecarlo}
  case $name in
    '' | */* | .*) fail "MONTECARLO_BIN_NAME must be a plain file name, got '$name'" ;;
  esac

  # HTTPS on every hop, unless a mirror outside HTTPS was asked for by name.
  case $base in
    https://*)
      curl_flags="--proto =https --proto-redir =https --tlsv1.2"
      wget_flags="--https-only --secure-protocol=TLSv1_2"
      ;;
    *)
      [ -n "${MONTECARLO_DOWNLOAD_BASE:-}" ] || fail "the download base must be HTTPS"
      curl_flags=""
      wget_flags=""
      ;;
  esac

  case $(uname -s) in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) fail "unsupported system $(uname -s); on Windows, use install.ps1" ;;
  esac
  case $(uname -m) in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) fail "unsupported architecture $(uname -m)" ;;
  esac

  if [ -n "${MONTECARLO_VERSION:-}" ]; then
    tag=v${MONTECARLO_VERSION#v}
  else
    location=$(fetch "$base/latest" -) || location=""
    tag=${location##*/}
    [ -n "$location" ] && [ "$tag" != latest ] || fail "found no release at $base/latest"
  fi
  is_release_tag "$tag" || fail "'$tag' is not a release version like v0.1.3"
  version=${tag#v}

  archive=montecarlo_${version}_${os}_${arch}.tar.gz
  sums=montecarlo_${version}_checksums.txt
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  say "downloading $archive ($tag)"
  fetch "$base/download/$tag/$archive" "$tmp/$archive" || fail "could not download $base/download/$tag/$archive"
  fetch "$base/download/$tag/$sums" "$tmp/$sums" || fail "could not download $base/download/$tag/$sums"

  want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/$sums")
  printf '%s\n' "$want" | grep -Eqx '[0-9a-f]{64}' || fail "$sums has no checksum for $archive"
  got=$(sha256 "$tmp/$archive")
  [ "$got" = "$want" ] || fail "$archive does not match its checksum: got $got, want $want"

  if command -v gh > /dev/null 2>&1 && gh auth status > /dev/null 2>&1; then
    gh attestation verify "$tmp/$archive" --repo "$repo" \
      --signer-workflow "$repo/.github/workflows/ci.yml" --source-ref refs/heads/main \
      --deny-self-hosted-runners > /dev/null ||
      fail "$archive has no provenance from $repo's release workflow on main"
    say "verified the provenance of $archive"
  else
    say "checked the checksum; with gh installed and logged in, the provenance is verified too"
  fi

  tar xzf "$tmp/$archive" -C "$tmp" montecarlo

  if [ -n "${MONTECARLO_INSTALL_DIR:-}" ]; then
    dir=$MONTECARLO_INSTALL_DIR
  elif [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
    dir=/usr/local/bin
  else
    dir=$HOME/.local/bin
  fi
  mkdir -p "$dir"
  target=$dir/$name

  if [ -e "$target" ] && ! is_ours "$target" && [ "${MONTECARLO_FORCE:-}" != 1 ]; then
    fail "$target is another program, not this CLI, so it was left alone.
  Install under another name with MONTECARLO_BIN_NAME, for example MONTECARLO_BIN_NAME=mc,
  or replace it with MONTECARLO_FORCE=1."
  fi

  # Written beside the target and renamed over it, so the target is never half-written.
  cp "$tmp/montecarlo" "$dir/.$name.$$"
  chmod 755 "$dir/.$name.$$"
  mv -f "$dir/.$name.$$" "$target"
  say "installed $("$target" version --output table) at $target"

  first=""
  old_ifs=$IFS
  IFS=:
  set -f
  for d in $PATH; do
    [ -n "$d" ] && [ -x "$d/$name" ] || continue
    [ -n "$first" ] || first=$d/$name
    if [ "$d/$name" != "$target" ] && ! is_ours "$d/$name"; then
      say "warning: $d/$name is another program with the same name; whichever comes first on PATH runs."
      say "  Install under another name with MONTECARLO_BIN_NAME to keep both."
    fi
  done
  set +f
  IFS=$old_ifs
  if [ -z "$first" ]; then
    say "$dir is not on PATH; add it, or run $target"
  elif [ "$first" != "$target" ]; then
    say "warning: $first comes before $target on PATH, so \"$name\" runs that one"
  fi
}

main "$@"
