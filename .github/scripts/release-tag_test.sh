#!/usr/bin/env bash
# Tests release-tag.sh against throwaway repositories.
set -euo pipefail
# Ignore the developer's git config (e.g. commit signing) so cases never prompt or fail on it.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

script=$(cd "$(dirname "$0")" && pwd)/release-tag.sh
failures=0
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT

commit() { git -C "$1" -c user.name=t -c user.email=t@example.com commit -q --allow-empty -m c; }

# repo <tag>... : a fresh repository whose HEAD carries every tag given.
repo() {
  local dir
  dir=$(mktemp -d "$root/XXXXXX")
  git -C "$dir" init -q
  commit "$dir"
  for t in "$@"; do git -C "$dir" tag "$t"; done
  echo "$dir"
}

# check <name> <dir> <want> : passes when the script prints exactly <want> in <dir>.
check() {
  local got
  got=$(cd "$2" && "$script")
  if [ "$got" = "$3" ]; then
    echo "ok   $1"
  else
    echo "FAIL $1: got '$got', want '$3'"
    failures=$((failures + 1))
  fi
}

check "the version tag on HEAD is printed" "$(repo v0.1.1)" v0.1.1
check "an untagged HEAD prints nothing" "$(repo)" ""
check "a non-version tag is ignored" "$(repo foo v0.2.0-rc1)" ""
check "the highest of two version tags is printed" "$(repo v0.1.2 v0.1.1)" v0.1.2
check "versions compare as numbers, not text" "$(repo v0.1.9 v0.1.10)" v0.1.10

dir=$(repo v0.1.1)
commit "$dir"
check "a tag on an earlier commit is not HEAD's" "$dir" ""

exit $((failures > 0))
