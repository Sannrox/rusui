#!/usr/bin/env bash

# general.sh
# Sourced by other scripts in the build directory. Not executed directly.
# Local docker builds only; no registry login.

set -o errexit
set -o nounset
set -o pipefail


USER_ID=$(id -u)
GROUP_ID=$(id -g)


DOCKER_OPTS=${DOCKER_OPTS:-}
IFS=" " read -r -a DOCKER <<< "docker ${DOCKER_OPTS}"
DOCKER_HOST=${DOCKER_HOST:-}

ROOT_PATH="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

source "${ROOT_PATH}/scripts/lib/init.sh"


GO_VERSION="${GO_VERSION:-"$(cat "${ROOT_PATH}/.go-version")"}"
readonly GO_VERSION
DOCKER_IMAGE_REGISTRY="${DOCKER_IMAGE_REGISTRY:-}"
readonly BASE_IMAGE="${DOCKER_IMAGE_REGISTRY}golang:${GO_VERSION}-trixie"
readonly RUNTIME_IMAGE="${DOCKER_IMAGE_REGISTRY}debian:trixie-slim"
readonly BUILD_IMAGE_REPO="rusui"


# Increment when build/build-image or the data-container volume set changes.
BUILD_IMAGE_VERSION="$(cat "${ROOT_PATH}/build/build-image/VERSION")"
readonly BUILD_IMAGE_VERSION


readonly LOCAL_OUTPUT_ROOT="${ROOT_PATH}/_output"
readonly LOCAL_OUTPUT_SUBPATH="${LOCAL_OUTPUT_ROOT}/dockerized"
readonly LOCAL_OUTPUT_BINPATH="${LOCAL_OUTPUT_SUBPATH}/bin"; export LOCAL_OUTPUT_BINPATH
readonly LOCAL_OUTPUT_IMAGES="${LOCAL_OUTPUT_ROOT}/images"; export LOCAL_OUTPUT_IMAGES


readonly GO_PACKAGE="github.com/sannrox/rusui"
readonly REMOTE_ROOT="/go/src/${GO_PACKAGE}"
readonly REMOTE_OUTPUT_SUBPATH="${REMOTE_ROOT}/dockerized"
readonly REMOTE_OUTPUT_ROOT="${REMOTE_OUTPUT_SUBPATH}/_output"; export REMOTE_OUTPUT_ROOT
readonly REMOTE_OUTPUT_BINPATH="${REMOTE_OUTPUT_SUBPATH}/bin"; export REMOTE_OUTPUT_BINPATH

HOSTNAME="${HOSTNAME:-rusui-build}"


function build::check_docker_if_in_path(){
  if ! command -v docker  > /dev/null 2>&1; then
    echo "Docker is not available in PATH"
    return 1
  fi
}

function build::get_docker_wrapped_binaries() {
  local targets=(
    "rusui,${RUNTIME_IMAGE}"
    "rusui-runner,${RUNTIME_IMAGE}"
  )

  echo "${targets[@]}"
}

function build::verify_prerequisites(){
    build::check_docker_if_in_path

    GIT_COMMIT_HASH=$(git rev-parse --short HEAD)

    BUILD_IMAGE_BASE_TAG="build-${GIT_COMMIT_HASH}"
    BUILD_IMAGE_TAG="${BUILD_IMAGE_BASE_TAG}-${BUILD_IMAGE_VERSION}"
    BUILD_IMAGE="${BUILD_IMAGE_REPO}:${BUILD_IMAGE_TAG}"
    BUILD_CONTAINER_BASE_NAME="rusui-compile-${GIT_COMMIT_HASH}"
    BUILD_CONTAINER_NAME="${BUILD_CONTAINER_BASE_NAME}-${BUILD_IMAGE_VERSION}"
    DATA_CONTAINER_BASE_NAME="rusui-data-${GIT_COMMIT_HASH}"
    DATA_CONTAINER_NAME="${DATA_CONTAINER_BASE_NAME}-${BUILD_IMAGE_VERSION}"

    DOCKER_MOUNT_ARGS=(--volumes-from "${DATA_CONTAINER_NAME}" --volume "${ROOT_PATH}:${REMOTE_ROOT}" )
    LOCAL_OUTPUT_BUILD_CONTEXT="${LOCAL_OUTPUT_ROOT}/${BUILD_IMAGE}"


    version::get_version
    if [[ -z "${GIT_VERSION:-}" ]]; then
      GIT_VERSION="v0.0.0-dev+${GIT_COMMIT:0:14}"
    fi
    export GIT_VERSION
    version::save_version_vars "${ROOT_PATH}/.version"
}

function build::docker_image_exists(){
    [[ -n "$1" && -n "$2" ]] || {
        echo "Internal error. Image not specified"
        exit 2
    }

    [[ $("${DOCKER[@]}" images -q "${1}:${2}") ]]
}

function build::docker_delete_old_images() {
    local tag
    for tag in $("${DOCKER[@]}" images "${1}" --format '{{.Tag}}'); do
        case "${tag}" in
            "${2}"*)
                if [[ -z "${3:-}"  ||  "${tag}" != "${3}" ]]; then
                    echo "Deleting image ${1}:${tag}"
                    "${DOCKER[@]}" rmi "${1}:${tag}"  > /dev/null || true
                else
                    echo "Keeping image ${1}:${tag}"
                fi
                ;;
        esac
    done
}

function build::docker_delete_old_containers(){
    local container
    for container in $("${DOCKER[@]}" ps -a --filter "name=${1}" --format '{{.Names}}'); do
        case "${container}" in
            "${1}"*)
                if [[ -z "${2:-}"  ||  "${container}" != "${2}" ]]; then
                    echo "Deleting container ${container}"
                    build::docker_destroy_container "${container}"
                else
                    echo "Keeping container ${container}"
                fi
                ;;
        esac
    done
}

function build::docker_destroy_container(){
    "${DOCKER[@]}" kill "${1}" > /dev/null 2>&1 || true
    "${DOCKER[@]}" rm -f -v "${1}" > /dev/null 2>&1 || true
}


function build::clean(){
    build::docker_delete_old_containers "${BUILD_CONTAINER_BASE_NAME}"
    build::docker_delete_old_containers "${DATA_CONTAINER_BASE_NAME}"
    build::docker_delete_old_images "${BUILD_IMAGE_REPO}" "${BUILD_IMAGE_BASE_TAG}"

    echo "Cleaning dangling images"
    "${DOCKER[@]}" image prune -f > /dev/null 2>&1 || true


    if [[ -d "${LOCAL_OUTPUT_ROOT}" ]]; then
      echo "Cleaning ${LOCAL_OUTPUT_ROOT}"
      chmod -R +w "${LOCAL_OUTPUT_ROOT}" || true
      rm -rf "${LOCAL_OUTPUT_ROOT}"
    fi
}

function build::load_data_container(){
    local ret=0
    local code=0

    code=$(docker inspect --format='{{.State.Running}}' "${DATA_CONTAINER_NAME}" 2>/dev/null) || ret=$?

    if [[ "${ret}" -eq 0 && "${code}" != "true" ]]; then
        build::docker_destroy_container "${DATA_CONTAINER_NAME}"
        ret=1
    fi

    if [[ "${ret}" -ne 0 ]]; then
        echo "Creating data container ${DATA_CONTAINER_NAME}"


        local -ra docker_run_cmd=(
            "${DOCKER[@]}" run
            --volume "${REMOTE_ROOT}"
            --volume /usr/local/go/pkg/linux_amd64
            --volume /usr/local/go/pkg/linux_arm64
            --name "${DATA_CONTAINER_NAME}"
            --hostname "${HOSTNAME}"
            "${BUILD_IMAGE}"
            chown -R "${USER_ID}":"${GROUP_ID}" "${REMOTE_ROOT}" /usr/local/go/pkg
        )

        "${docker_run_cmd[@]}"
    fi
}

function build::build_image(){

    mkdir -p "${LOCAL_OUTPUT_BUILD_CONTEXT}"

    chown -R "${USER_ID}":"${GROUP_ID}" "${LOCAL_OUTPUT_BUILD_CONTEXT}" || true

    cp -L /etc/localtime "${LOCAL_OUTPUT_BUILD_CONTEXT}/localtime"
    chmod u+w "${LOCAL_OUTPUT_BUILD_CONTEXT}/localtime"

    cp "${ROOT_PATH}/build/build-image/Dockerfile" "${LOCAL_OUTPUT_BUILD_CONTEXT}/Dockerfile"

    build::docker_build "${BUILD_IMAGE}" "${LOCAL_OUTPUT_BUILD_CONTEXT}" "false" "--build-arg BASE_IMAGE=${BASE_IMAGE} --build-arg VERSION=${BUILD_IMAGE_VERSION}"

    build::docker_delete_old_containers "${BUILD_CONTAINER_BASE_NAME}" "${BUILD_CONTAINER_NAME}"
    build::docker_delete_old_containers "${DATA_CONTAINER_BASE_NAME}" "${DATA_CONTAINER_NAME}"
    build::docker_delete_old_images "${BUILD_IMAGE_REPO}" "${BUILD_IMAGE_BASE_TAG}" "${BUILD_IMAGE_TAG}"

    build::load_data_container

}

function build::run_build_command(){
    echo "Running build command: $*"
    build::run_build_command_in_build_image "${BUILD_CONTAINER_NAME}" -- "$@"
}

function build::run_build_command_in_build_image(){
    [[ $# != 0  ]] || { echo "Internal error. No image specified" >&2; return 4; }
    local -r container_name="${1}"
    shift

    local -a docker_run_opts=(
        "--name=${container_name}"
        "--user=${USER_ID}:${GROUP_ID}"
        "--hostname=${HOSTNAME}"
        "${DOCKER_MOUNT_ARGS[@]}"
    )

    local detach=false

    [[ $# != 0  ]] || { echo "Invalid input - docker args followed by --" >&2; return 4; }
    until [[ -z "${1-}" ]]; do
        if [[ "${1}" == "--" ]]; then
            shift
            break
        fi
        docker_run_opts+=("${1}")
        if [[ "${1}" == "-d" || "${1}" == "--detach" ]]; then
            detach=true
        fi
        shift
    done

    [[ $# != 0  ]] || { echo "Invalid input - command to run not given" >&2; return 4; }
    local -a docker_cmd=()
    until [[ -z "${1-}" ]]; do
        docker_cmd+=("${1}")
        shift
    done

    mkdir -p "${LOCAL_OUTPUT_SUBPATH}/tmp"
    docker_run_opts+=(
        --env "BUILD_PLATFORMS=${BUILD_PLATFORMS:-}"
        --env "GOFLAGS=${GOFLAGS:-}"
        --env "GIT_VERSION=${GIT_VERSION:-}"
        --env "HOME=/tmp/rusui-home"
        --env "TMPDIR=${REMOTE_ROOT}/_output/dockerized/tmp"
        --env "GOTMPDIR=${REMOTE_ROOT}/_output/dockerized/tmp"
        --env "GIT_CONFIG_COUNT=1"
        --env "GIT_CONFIG_KEY_0=safe.directory"
        --env "GIT_CONFIG_VALUE_0=*"
        --workdir "${REMOTE_ROOT}"
    )


    if [[ -t 0 ]]; then
        docker_run_opts+=(--interactive --tty)
    elif [[ "${detach}" == "false" ]]; then
        docker_run_opts+=(--attach=stdout --attach=stderr)
    fi

    local -ra docker_run_cmd=("${DOCKER[@]}" run "${docker_run_opts[@]}" "${BUILD_IMAGE}")
    build::docker_destroy_container "${container_name}"
    "${docker_run_cmd[@]}" "${docker_cmd[@]}"
    if [[ "${detach}" == "false" ]]; then
        build::docker_destroy_container "${container_name}"
    fi
}

function build::docker_build() {
    local -r image="${1}"
    local -r context_dir="${2}"
    local -r pull="${3:-true}"
    local build_args
    IFS=" " read -r -a build_args <<< "${4:-}"

    local -a build_cmd=("${DOCKER[@]}" buildx build)
    if [[ ${#build_args[@]} -gt 0 ]]; then
        build_cmd+=("${build_args[@]}")
    fi
    build_cmd+=(-t "${image}" "--pull=${pull}" --load "${context_dir}")

    echo "Building docker image ${image} with context ${context_dir}"
    DOCKER_CLI_EXPERIMENTAL=enabled "${build_cmd[@]}" || {
        cat <<EOF >&2
    Error: Docker build failed.

    to retry manually, run:

    DOCKER_CLI_EXPERIMENTAL=enabled ${build_cmd[*]}

EOF
        return  1

}
}
