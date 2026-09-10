#!/usr/bin/env bash

# This script checks whether go fix modernizations need to be applied.
# Run `scripts/update-go-fix.sh` to actually apply fixes.
#
# Usage: scripts/validate-go-fix.sh

set -o errexit
set -o nounset
set -o pipefail

ROOT_PATH="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

source "${ROOT_PATH}/scripts/lib/init.sh"

golang::setup_env

cd "${ROOT_PATH}"

# Modernize tool (runs all analyzers by default)
MODERNIZE_VERSION="latest"
go install "golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize@${MODERNIZE_VERSION}"

find_packages() {
    find . -not \( \
        \( \
            -wholename './.git' \
            -o -wholename './_output' \
            -o -wholename './vendor' \
            -o -wholename '*/vendor/*' \
        \) -prune \
    \) -name '*.go' -exec dirname {} \; | sort -u | sed 's|^\./|./|'
}

# modernize exits with non-zero exit code if it finds issues.
# Without "|| true" this would fail before printing useful output.
diff=$(find_packages | xargs modernize 2>&1) || true
if [[ -n "${diff}" ]]; then
    echo "${diff}" >&2
    echo >&2
    echo "Run ./scripts/update-go-fix.sh" >&2
    exit 1
fi
