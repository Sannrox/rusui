#!/usr/bin/env bash
#
# Build the reference guest image (ADR 0018 D4). The Dockerfile hash is the
# build cache tag; the printed tag names the built image by its image ID,
# the same tag rusui setup apply records.
#

set -o errexit
set -o nounset
set -o pipefail

ROOT_PATH="$(dirname "${BASH_SOURCE[0]}")/../.."
DOCKERFILE="${ROOT_PATH}/build/guest-image/Dockerfile"
RUNTIME="${CONTAINER_RUNTIME:-docker}"
CACHE="rusui-guest:build-$(shasum -a 256 "${DOCKERFILE}" | cut -c1-16)"

"${RUNTIME}" build -t "${CACHE}" - < "${DOCKERFILE}" >&2
ID="$("${RUNTIME}" image inspect --format '{{.Id}}' "${CACHE}")"
ID="${ID#sha256:}"
TAG="rusui-guest:${ID:0:16}"
"${RUNTIME}" tag "${CACHE}" "${TAG}"
echo "${TAG}"
