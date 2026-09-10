#!/usr/bin/env bash
#
#

set -o errexit
set -o pipefail
set -o nounset

ROOT_PATH=$(dirname "${BASH_SOURCE[0]}")/..

source "${ROOT_PATH}/build/general.sh"
source "${ROOT_PATH}/build/lib/release.sh"

build::verify_prerequisites
release::publish_server_image  "${GIT_VERSION}"
