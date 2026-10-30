#!/usr/bin/env bash
# List the changes merged to main since the last release tag, newest first,
# as a starting point for the release owner's Keep a Changelog section.
# Pull requests do not edit CHANGELOG.md; the version-bump step does.
set -euo pipefail

ROOT_PATH="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${ROOT_PATH}"

version="$(tr -d '[:space:]' < VERSION)"
[[ -n "$version" ]] || { echo "changelog-draft: VERSION is empty" >&2; exit 1; }

git fetch --quiet --tags origin main
since="${1:-$(git describe --tags --abbrev=0 origin/main 2>/dev/null || git rev-list --max-parents=0 origin/main)}"
echo "## [${version}]"
echo
git log --first-parent --format='- %s' "${since}..origin/main"
