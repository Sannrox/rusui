#!/usr/bin/env bash

# shellcheck disable=SC2034
set -o errexit
set -o nounset
set -o pipefail



ROOT_PATH=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
cd "${ROOT_PATH}"

source "${ROOT_PATH}/scripts/lib/init.sh"

golang::setup_env

# GOLANG CI LINT
GOLANGCI_LINT=golangci-lint
GOLANGCI_LINT_OPTS=${GOLANGCI_LINT_OPTS:-}
GOLANGCI_LINT_VERSION="v2.12.2"


function check_if_golangci_lint_is_in_path(){
    if ! command -v "${GOLANGCI_LINT}" > /dev/null 2>&1; then
        install_golangci_lint
    fi

}

function check_golangci_lint_version(){
    if [ "$("${GOLANGCI_LINT}" version | grep -o "${GOLANGCI_LINT_VERSION/v/}")" != "${GOLANGCI_LINT_VERSION/v/}" ]; then
        echo "Install new version ${GOLANGCI_LINT_VERSION/v/} of golangci-lint "
        install_golangci_lint
    fi
}

function resolve_lint_base(){
    if [[ -n "${GOLANGCI_LINT_BASE_REV:-}" ]]; then
        git rev-parse --verify "${GOLANGCI_LINT_BASE_REV}^{commit}"
        return
    fi

    local target_ref
    for target_ref in origin/main main; do
        if git rev-parse --verify --quiet "${target_ref}^{commit}" > /dev/null; then
            git merge-base HEAD "${target_ref}"
            return
        fi
    done

    echo "Unable to determine lint base. Fetch origin/main or set GOLANGCI_LINT_BASE_REV." >&2
    return 1
}

function run_golangci_lint(){
    if [[ "${GOLANGCI_LINT_FULL:-0}" == "1" ]]; then
        echo "Running full-repository golangci-lint audit"
        # shellcheck disable=SC2086 # options are intentionally passed as separate arguments
        "${GOLANGCI_LINT}" run -v -c .golangci.yaml ${GOLANGCI_LINT_OPTS} ./...
        return
    fi

    # PR validation is a ratchet: reject new findings without hiding existing
    # debt from explicit full audits (GOLANGCI_LINT_FULL=1).
    local base_rev
    base_rev=$(resolve_lint_base)

    echo "Running golangci-lint for changes since ${base_rev}"
    # shellcheck disable=SC2086 # options are intentionally passed as separate arguments
    "${GOLANGCI_LINT}" run -v -c .golangci.yaml --new-from-rev "${base_rev}" ${GOLANGCI_LINT_OPTS} ./...
}

function install_golangci_lint(){
	mkdir -p "${GOPATH}/bin"
	curl -sfL "https://raw.githubusercontent.com/golangci/golangci-lint/${GOLANGCI_LINT_VERSION}/install.sh" \
		| sed -e '/install -d/d' \
		| sh -s -- -b "${GOPATH}/bin" "${GOLANGCI_LINT_VERSION}"
}


check_if_golangci_lint_is_in_path
check_golangci_lint_version
run_golangci_lint
