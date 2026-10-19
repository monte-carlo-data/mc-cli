#!/usr/bin/env bash
# Prints the version tag on HEAD, or nothing when HEAD carries none. The release reads its tag from
# here, not from next-tag.sh, which prints nothing once HEAD is tagged, so a rerun still releases.
# Reads tags from the local clone.
set -euo pipefail

git tag --points-at HEAD | { grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' || true; } | sort -V | tail -1
