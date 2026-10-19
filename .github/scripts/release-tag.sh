#!/usr/bin/env bash
# Prints the highest plain vX.Y.Z tag on HEAD, or nothing when HEAD carries none. Reads tags from
# the local clone.
set -euo pipefail

git tag --points-at HEAD | { grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' || true; } | sort -V | tail -1
