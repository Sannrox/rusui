#!/usr/bin/env bash

ROOT_PATH="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"

_OUTPUT_SUBPATH="${OUTPUT_SUBPATH:-_output/local}"
export OUTPUT="${ROOT_PATH}/${_OUTPUT_SUBPATH}"
export OUTPUT_BINPATH="${OUTPUT}/bin"
export BIN="${ROOT_PATH}/_output/bin"

BUILDTIME=${BUILDTIME:-$(date -u +"%Y-%m-%dT%H:%M:%SZ")}
GITCOMMIT=${GITCOMMIT:-$(git rev-parse --short HEAD 2> /dev/null || true)}

source "${ROOT_PATH}/scripts/lib/golang.sh"
source "${ROOT_PATH}/scripts/lib/version.sh"
