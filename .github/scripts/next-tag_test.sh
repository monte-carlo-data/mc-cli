#!/usr/bin/env bash
# Tests next-tag.sh against throwaway repositories.
set -euo pipefail
# Ignore the developer's git config (e.g. commit signing) so cases never prompt or fail on it.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

script=$(cd "$(dirname "$0")" && pwd)/next-tag.sh
failures=0
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT

# repo <version> <tag>... : a fresh repository whose one commit carries every tag given.
repo() {
  local dir
  dir=$(mktemp -d "$root/XXXXXX")
  cd "$dir"
  git init -q
  printf '%s\n' "$1" > VERSION
  git add VERSION
  git -c user.name=t -c user.email=t@example.com commit -qm init
  shift
  for t in "$@"; do git tag "$t"; done
}

# untagged_head : add a commit after the tags, as a merge to main would.
untagged_head() {
  git -c user.name=t -c user.email=t@example.com commit -q --allow-empty -m next
}

expect() {
  local name=$1 want=$2 got
  got=$("$script" 2>/dev/null) || got="<failed>"
  if [[ $got == "$want" ]]; then
    echo "ok   $name"
  else
    echo "FAIL $name: want '$want', got '$got'"
    failures=$((failures + 1))
  fi
}

repo 0.1
expect "the first release on a base is .0" v0.1.0

repo 0.1 v0.1.0 v0.1.1 v0.1.9 v0.1.10; untagged_head
expect "patches compare as numbers, not strings" v0.1.11

repo 0.2 v0.1.7; untagged_head
expect "a new base starts again at .0" v0.2.0

repo 0.1 v0.2.0 v0.1.3 v0.10.4; untagged_head
expect "tags on another base are ignored" v0.1.4

repo 0.1 v0.1.2 v0.1.3-rc1 v0.1.4.1; untagged_head
expect "only plain v<major>.<minor>.<patch> tags count" v0.1.3

repo 1.0 v0.9.4; untagged_head
expect "a 1.x base is allowed" v1.0.0

repo 0.1 v0.1.4
expect "a commit already tagged is not tagged again" ""

repo 0.1; untagged_head; git tag v0.1.4; git checkout -q HEAD~1
expect "a commit older than a released one is not tagged" ""

for bad in 2.0 0.1.0 v0.1 01.1 abc ""; do
  repo "$bad"
  expect "VERSION '$bad' is rejected" "<failed>"
done

exit $((failures > 0))
