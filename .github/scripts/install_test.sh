#!/usr/bin/env bash
# Copyright Monte Carlo AI, Inc.
# SPDX-License-Identifier: Apache-2.0
#
# Tests install.sh against a snapshot dist/ served by mirror.py, laid out like GitHub releases.
#   install_test.sh <dist> <tag>
# With PYTHON_CLI set to the path of the Python CLI's montecarlo, also checks that it is left
# alone.
#
# The stubs are written with a literal $1, cleanup runs from the trap, and each case list is split
# into words on purpose.
# shellcheck disable=SC2016,SC2086,SC2329

set -euo pipefail

dist=$(cd "$1" && pwd)
tag=$2
here=$(cd "$(dirname "$0")" && pwd)
installer=$here/../../install.sh
root=$(mktemp -d)
failures=0
pids=()
cleanup() {
  for pid in "${pids[@]}"; do kill "$pid" 2> /dev/null || true; done
  chmod -R u+w "$root" && rm -rf "$root"
}
trap cleanup EXIT

case $(uname -s)_$(uname -m) in
  Darwin_arm64) native=darwin_arm64 ;; Darwin_x86_64) native=darwin_amd64 ;;
  Linux_aarch64) native=linux_arm64 ;; Linux_x86_64) native=linux_amd64 ;;
esac
archive=montecarlo_${tag#v}_$native.tar.gz
sums=montecarlo_${tag#v}_checksums.txt

# mirror <dist> [--no-release]: starts a mirror and prints its base URL.
mirror() {
  local out
  out=$(mktemp "$root/mirror.XXXXXX")
  python3 "$here/mirror.py" --dist "$1" --tag "$tag" "${@:2}" > "$out" &
  pids+=($!)
  for _ in $(seq 50); do [ -s "$out" ] && break; sleep 0.1; done
  cat "$out"
}

# A gh that is installed but not logged in, unless a case says otherwise, so a developer's own
# gh never verifies a snapshot that has no attestation.
stubs=$root/stubs
mkdir -p "$stubs"
printf '#!/bin/sh\nexit 1\n' > "$stubs/gh"
chmod +x "$stubs/gh"

base=$(mirror "$dist")

# run <name> [VAR=value...]: runs install.sh in a fresh home with the variables given, leaving
# its exit code in $code and its stderr in $err.
run() {
  name=$1
  shift
  home=$root/$name
  mkdir -p "$home/bin"
  code=0
  err=$(env HOME="$home" PATH="$stubs:$home/bin:$PATH" MONTECARLO_DOWNLOAD_BASE="$base" \
    MONTECARLO_INSTALL_DIR="$home/bin" "$@" sh "$installer" 2>&1 > /dev/null) || code=$?
}

pass() { echo "ok   $1"; }
fail() {
  echo "FAIL $1"
  printf '%s\n' "$err" | sed 's/^/       /'
  failures=$((failures + 1))
}

installed_version() { "$home/bin/${1:-montecarlo}" version --output json | python3 -c 'import json,sys; print(json.load(sys.stdin)["version"])'; }

foreign() {
  printf '#!/bin/sh\necho another montecarlo\n' > "$1"
  chmod +x "$1"
}

run latest
if [ $code = 0 ] && [ "$(installed_version)" = "$tag" ]; then pass "the latest release is found through the redirect"; else fail "the latest release is found through the redirect"; fi

run pinned MONTECARLO_VERSION="$tag"
if [ $code = 0 ] && [ "$(installed_version)" = "$tag" ]; then pass "a pinned version installs"; else fail "a pinned version installs"; fi

run pinned-no-v MONTECARLO_VERSION="${tag#v}"
if [ $code = 0 ] && [ "$(installed_version)" = "$tag" ]; then pass "a pinned version without the v installs"; else fail "a pinned version without the v installs"; fi

home=$root/piped
mkdir -p "$home/bin"
code=0
err=$(HOME="$home" PATH="$stubs:$PATH" MONTECARLO_DOWNLOAD_BASE="$base" MONTECARLO_INSTALL_DIR="$home/bin" \
  sh < "$installer" 2>&1 > /dev/null) || code=$?
if [ $code = 0 ] && [ "$(installed_version)" = "$tag" ]; then pass "the script runs piped into sh"; else fail "the script runs piped into sh"; fi

for bad in 1.2 v0.1 "v0.1.0;true" v0.1.0-rc1; do
  run "bad-version-${bad//[^a-z0-9]/_}" MONTECARLO_VERSION="$bad"
  if [ $code != 0 ] && [[ $err == *"is not a release version"* ]]; then pass "version '$bad' is refused"; else fail "version '$bad' is refused"; fi
done

run bad-name MONTECARLO_BIN_NAME=../mc
if [ $code != 0 ] && [[ $err == *"plain file name"* ]]; then pass "a bin name with a path is refused"; else fail "a bin name with a path is refused"; fi

no_release=$(mirror "$dist" --no-release)
run no-release MONTECARLO_DOWNLOAD_BASE="$no_release"
if [ $code != 0 ] && [[ $err == *"found no release"* ]]; then pass "no release yet is said so"; else fail "no release yet is said so"; fi

tampered=$root/tampered-dist
cp -R "$dist" "$tampered"
printf 'x' >> "$tampered/$archive"
run tampered MONTECARLO_DOWNLOAD_BASE="$(mirror "$tampered")"
if [ $code != 0 ] && [[ $err == *"does not match its checksum"* ]] && [ ! -e "$home/bin/montecarlo" ]; then pass "a tampered archive is refused"; else fail "a tampered archive is refused"; fi

unlisted=$root/unlisted-dist
cp -R "$dist" "$unlisted"
grep -v " $archive\$" "$dist/$sums" > "$unlisted/$sums"
run unlisted MONTECARLO_DOWNLOAD_BASE="$(mirror "$unlisted")"
if [ $code != 0 ] && [[ $err == *"has no checksum"* ]]; then pass "an archive missing from the checksums is refused"; else fail "an archive missing from the checksums is refused"; fi

run upgrade
run upgrade
if [ $code = 0 ] && [ "$(installed_version)" = "$tag" ]; then pass "a second install replaces our own without force"; else fail "a second install replaces our own without force"; fi

mkdir -p "$root/foreign/bin"
foreign "$root/foreign/bin/montecarlo"
run foreign
if [ $code != 0 ] && [[ $err == *"is another program"* ]] && grep -q "another montecarlo" "$home/bin/montecarlo"; then pass "another program at the target is left alone"; else fail "another program at the target is left alone"; fi

run foreign MONTECARLO_BIN_NAME=mc
if [ $code = 0 ] && [ "$(installed_version mc)" = "$tag" ] && grep -q "another montecarlo" "$home/bin/montecarlo"; then pass "MONTECARLO_BIN_NAME installs beside it"; else fail "MONTECARLO_BIN_NAME installs beside it"; fi

run foreign MONTECARLO_FORCE=1
if [ $code = 0 ] && [ "$(installed_version)" = "$tag" ]; then pass "MONTECARLO_FORCE replaces it"; else fail "MONTECARLO_FORCE replaces it"; fi

shadow=$root/shadow
mkdir -p "$shadow"
foreign "$shadow/montecarlo"
run shadowed PATH="$stubs:$shadow:$PATH"
if [ $code = 0 ] && [[ $err == *"$shadow/montecarlo is another program"* ]] && [[ $err == *"comes before"* ]]; then pass "another montecarlo earlier on PATH is warned about"; else fail "another montecarlo earlier on PATH is warned about"; fi

printf '#!/bin/sh\n[ "$1" = auth ] && exit 0\nexit 1\n' > "$root/stub-gh-rejects"
printf '#!/bin/sh\nexit 0\n' > "$root/stub-gh-accepts"
chmod +x "$root/stub-gh-rejects" "$root/stub-gh-accepts"
cp "$root/stub-gh-rejects" "$stubs/gh"
run gh-rejects
if [ $code != 0 ] && [[ $err == *"has no provenance"* ]] && [ ! -e "$home/bin/montecarlo" ]; then pass "a provenance gh rejects is refused"; else fail "a provenance gh rejects is refused"; fi
cp "$root/stub-gh-accepts" "$stubs/gh"
run gh-accepts
if [ $code = 0 ] && [[ $err == *"verified the provenance"* ]]; then pass "a provenance gh accepts is reported"; else fail "a provenance gh accepts is reported"; fi
printf '#!/bin/sh\nexit 1\n' > "$stubs/gh"

# Each system and architecture uname can report picks its archive. The binary may not run here,
# so only the download is checked.
for case in "Darwin arm64 darwin_arm64" "Darwin x86_64 darwin_amd64" "Linux aarch64 linux_arm64" \
  "Linux x86_64 linux_amd64" "Linux amd64 linux_amd64" "Linux arm64 linux_arm64"; do
  set -- $case
  printf '#!/bin/sh\ncase $1 in -s) echo %s ;; -m) echo %s ;; esac\n' "$1" "$2" > "$stubs/uname"
  chmod +x "$stubs/uname"
  run "uname-$1-$2"
  if [[ $err == *"downloading montecarlo_${tag#v}_$3.tar.gz"* ]]; then pass "uname $1 $2 downloads the $3 archive"; else fail "uname $1 $2 downloads the $3 archive"; fi
done
for case in "FreeBSD amd64 unsupported system" "Linux i686 unsupported architecture"; do
  set -- $case
  printf '#!/bin/sh\ncase $1 in -s) echo %s ;; -m) echo %s ;; esac\n' "$1" "$2" > "$stubs/uname"
  run "uname-$1-$2"
  if [ $code != 0 ] && [[ $err == *"$3 $4"* ]]; then pass "uname $1 $2 is refused"; else fail "uname $1 $2 is refused"; fi
done
rm "$stubs/uname"

# With curl hidden, wget does the downloads and finds the redirect.
if command -v wget > /dev/null 2>&1; then
  nocurl=$root/nocurl
  mkdir -p "$nocurl"
  for tool in sh uname mktemp rm awk grep sed tail tr cut sha256sum shasum tar gzip mkdir cp chmod mv wget env; do
    p=$(command -v "$tool" || true)
    [ -n "$p" ] && ln -s "$p" "$nocurl/$tool"
  done
  run wget-only PATH="$stubs:$nocurl"
  if [ $code = 0 ] && [ "$(installed_version)" = "$tag" ]; then pass "wget alone installs the latest release"; else fail "wget alone installs the latest release"; fi
else
  echo "skip wget alone: wget is not installed"
fi

if [ -n "${PYTHON_CLI:-}" ]; then
  python_bin=$(dirname "$PYTHON_CLI")
  before=$(cksum < "$PYTHON_CLI")
  run python-cli MONTECARLO_INSTALL_DIR="$python_bin"
  if [ $code != 0 ] && [[ $err == *"is another program"* ]] && [ "$(cksum < "$PYTHON_CLI")" = "$before" ]; then pass "the Python CLI's montecarlo is left alone"; else fail "the Python CLI's montecarlo is left alone"; fi
  run python-cli-on-path PATH="$stubs:$python_bin:$PATH"
  if [ $code = 0 ] && [[ $err == *"$PYTHON_CLI is another program"* ]]; then pass "the Python CLI earlier on PATH is warned about"; else fail "the Python CLI earlier on PATH is warned about"; fi
fi

exit $((failures > 0))
