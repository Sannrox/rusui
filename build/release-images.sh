#!/usr/bin/env bash
#
set -o errexit
set -o pipefail
set -o nounset


ROOT_PATH=$(dirname "${BASH_SOURCE[0]}")/..

# Default to the Docker engine's linux arch so runtime image RUN steps
# do not need qemu. Override with BUILD_PLATFORMS='linux/amd64 linux/arm64'.
if [[ -z "${BUILD_PLATFORMS:-}" ]]; then
  arch="$(docker version -f '{{.Server.Arch}}' 2>/dev/null || echo amd64)"
  export BUILD_PLATFORMS="linux/${arch}"
fi

source "${ROOT_PATH}/build/general.sh"
source "${ROOT_PATH}/build/lib/release.sh"


CMD_TARGETS="${SERVER_TARGETS[*]}"

build::verify_prerequisites
build::build_image
build::run_build_command make all WHAT="${CMD_TARGETS}" BUILD_PLATFORMS="${SERVER_PLATFORMS[*]}" GOFLAGS="${GOFLAGS:--buildvcs=false}"

release::build_server_image
