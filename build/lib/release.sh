#!/usr/bin/env bash

#
set -o errexit
set -o nounset
set -o pipefail

readonly RELEASE_STAGE_PATH="${LOCAL_OUTPUT_ROOT}/release-stage"
readonly RELEASE_IMAGES_PATH="${LOCAL_OUTPUT_ROOT}/release-images"

BUILD_PULL_LATEST_IMAGES=${BUILD_PULL_LATEST_IMAGES:-0}


function release::docker_registry_prefix() {
    if [[ -n "${DOCKER_REGISTRY:-}" ]]; then
        echo "${DOCKER_REGISTRY}/"
    else
        echo ""
    fi
}

function release::build_server_image(){
    rm -rf "${RELEASE_IMAGES_PATH}"

    local platform
    for platform in "${SERVER_PLATFORMS[@]}"; do
        local platform_tag arch
        platform_tag=${platform/\//-}
        arch=$(basename "${platform}")

        echo "Building release images for ${platform_tag} (${arch})"

        local release_stage
        release_stage="${RELEASE_STAGE_PATH}/servers/${platform_tag}/"
        rm -rf "${release_stage}"
        mkdir -p "${release_stage}/server/bin"

        local bin
        for bin in "${SERVER_BINARIES[@]}"; do
            cp "${LOCAL_OUTPUT_BINPATH}/${platform}/${bin}" "${release_stage}/server/bin/"
        done

        release::create_server_image "${release_stage}/server/bin" "${arch}"

    done

}

function release::create_server_image(){
    local binary_dir
    local arch
    local binaries
    binary_dir="$1"
    arch="$2"
    binaries=$(build::get_docker_wrapped_binaries)

    mkdir -p "${RELEASE_IMAGES_PATH}/${arch}"

    local docker_registry
    docker_registry="$(release::docker_registry_prefix)"

     local docker_tag="${GIT_VERSION/+/_}"
     if [ -z "${docker_tag}" ]; then
        echo "GIT_VERSION is not set; cannot create docker images"
        return 1
     fi

     local -a docker_build_opts=()
     if [ "${BUILD_PULL_LATEST_IMAGES}" -eq 1 ]; then
        docker_build_opts+=(--pull)
     fi

     local wrappable
     for wrappable in $binaries; do
       local binary_name="${wrappable%%,*}"
       local base_image="${wrappable##*,}"

       local binary_file_path="${binary_dir}/${binary_name}"
       local docker_build_path="${binary_file_path}.dockerbuild"
       local docker_image_tag="${docker_registry}${binary_name}-${arch}:${docker_tag}"
       local docker_file_path="${ROOT_PATH}/build/server-image/Dockerfile"

       if [ -f "${ROOT_PATH}/build/image/${binary_name}/Dockerfile" ]; then
           docker_file_path="${ROOT_PATH}/build/image/${binary_name}/Dockerfile"
       fi

       echo "Building docker image ${docker_image_tag} from ${docker_file_path} with context ${docker_build_path}"
       rm -rf "${docker_build_path}"
       mkdir -p "${docker_build_path}"

       cp "${binary_file_path}" "${docker_build_path}/${binary_name}"

       local build_log="${docker_build_path}/build.log"

       if ! DOCKER_CLI_EXPERIMENTAL=enabled "${DOCKER[@]}" buildx build  \
           --load "${docker_build_opts[@]}" \
           -t "${docker_image_tag}" \
           -f "${docker_file_path}" \
           --build-arg BINARY="${binary_name}" \
           --build-arg BASE_IMAGE="${base_image}" \
           "${docker_build_path}" > "${build_log}" 2>&1; then
           cat "${build_log}"
           exit 1
       fi
       rm "${build_log}"

       echo "Created docker image ${docker_image_tag}"
    done
}

function release::publish_server_image() {
    local docker_tag="$1"

    if [[ -z "${DOCKER_REGISTRY:-}" ]]; then
        echo "DOCKER_REGISTRY is unset; skipping image push"
        return 0
    fi

    local platform
    for platform in "${SERVER_PLATFORMS[@]}"; do
        release::push_server_images "${docker_tag}" "${platform}"
    done
}

function release::push_server_images() {
    local docker_tag="$1"
    local platform="$2"
    local binaries
    binaries=$(build::get_docker_wrapped_binaries)

    local wrappable
    for wrappable in $binaries; do
        local binary_name="${wrappable%%,*}"
        release::push_server_image "${binary_name}" "${docker_tag}" "${platform}"
    done
}

function release::push_server_image() {
    local docker_registry
    docker_registry="$(release::docker_registry_prefix)"
    local binary_name="$1"
    local docker_tag="${2/+/_}"
    local platform="$3"

    local platform_tag arch
    platform_tag=${platform/\//-}
    arch=$(basename "${platform}")
    local docker_image_tag="${docker_registry}${binary_name}-${arch}:${docker_tag}"

    echo "Push images for ${platform_tag} (${arch}): ${binary_name}"
    docker push "${docker_image_tag}"
}
