#!/usr/bin/env bash

# Apply go fix modernizations to the codebase.
# Usage: scripts/update-go-fix.sh

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

find_packages | xargs modernize -fix
