#!/usr/bin/env bash
#
# Build a static binary for the host OS/ARCH
#


set -o errexit
set -o nounset
set -o pipefail


ROOT_PATH="$(dirname "${BASH_SOURCE[0]}")/../.."

source "${ROOT_PATH}/scripts/lib/init.sh"


version::get_version

golang::build_binaries "$@"
