#!/usr/bin/env bash

## Run a command in the docker build container. Typically this will be one of
# the commands in `scripts/` or `make`. When running in the build container the
# user is sure to have a consistent reproducible build environment.

set -o errexit
set -o nounset
set -o pipefail

ROOT_PATH=$(dirname "${BASH_SOURCE[0]}")/..
source "$ROOT_PATH/build/general.sh"


build::verify_prerequisites
build::build_image

build::run_build_command "$@"
