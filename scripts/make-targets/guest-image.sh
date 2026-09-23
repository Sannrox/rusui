#!/usr/bin/env bash
#
# Build the reference guest image (ADR 0018 D4) with its content tag.
#

set -o errexit
set -o nounset
set -o pipefail

ROOT_PATH="$(dirname "${BASH_SOURCE[0]}")/../.."
DOCKERFILE="${ROOT_PATH}/build/guest-image/Dockerfile"
RUNTIME="${CONTAINER_RUNTIME:-docker}"
TAG="rusui-guest:$(shasum -a 256 "${DOCKERFILE}" | cut -c1-16)"

"${RUNTIME}" build -t "${TAG}" - < "${DOCKERFILE}"
echo "${TAG}"
