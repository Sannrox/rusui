#!/usr/bin/env bash
#

set -o errexit
set -o nounset
set -o pipefail


ROOT_PATH=$(dirname "${BASH_SOURCE[0]}")/..

source "${ROOT_PATH}/build/general.sh"


build::verify_prerequisites
build::build_image
