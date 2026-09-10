#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail


ROOT_PATH=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
cd "${ROOT_PATH}"

source "${ROOT_PATH}/scripts/lib/init.sh"

golang::setup_env

# shellcheck disable=SC2034 # GOVULNCHECK used in function calls
GOVULNCHECK=govulncheck
GOVULNCHECK_VERSION="v1.1.4"


function install_govulncheck(){
    go install golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}
  }

function run_govulncheck(){
    govulncheck -scan package ./...
}

install_govulncheck
run_govulncheck
