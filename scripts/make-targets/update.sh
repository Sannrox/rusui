#!/usr/bin/env bash

# shellcheck disable=SC2034

set -o errexit
set -o nounset
set -o pipefail


ROOT_PATH="$(cd "$(dirname "$0")/../.." && pwd -P)"

run_cmd(){
    filname="${##*/update-}"

    "$@"
}

run_update(){
    for script in $1; do
            echo  "Updating $(basename "${script}")"
            run_cmd "${script}"
    done
}

git config --local --add safe.directory "${ROOT_PATH}" || true

run_update "${ROOT_PATH}/scripts/update-*.sh"
