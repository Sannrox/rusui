#!/usr/bin/env bash
#

readonly CA_GOPATH="${CA_GOPATH:-"${OUTPUT}/go"}"
export CA_GOPATH


# Docker-wrapped binaries. Host `make all` still builds the machine's GOOS/GOARCH.
readonly SUPPORTED_SERVER_PLATFORMS=(
    linux/amd64
    linux/arm64
)

function golang::server_targets() {
  local -r targets=(
    "cmd/rusui"
    "cmd/rusui-runner"
  )
  echo "${targets[@]}"
}

IFS=" " read -ra SERVER_TARGETS <<< "$(golang::server_targets)"
readonly SERVER_TARGETS
readonly SERVER_BINARIES=("${SERVER_TARGETS[@]##*/}"); export SERVER_BINARIES


function golang::verify_go_version() {
  # default GO_VERSION to content of .go-version
  GO_VERSION="${GO_VERSION:-"$(cat "${ROOT_PATH}/.go-version")"}"

  if [ "${GOTOOLCHAIN:-auto}" != 'auto' ]; then
    # no-op, just respect GOTOOLCHAIN
    :
  elif [ -n "${FORCE_HOST_GO:-}" ]; then
    # ensure existing host version is used, like before GOTOOLCHAIN existed
    export GOTOOLCHAIN='local'
  else
    # otherwise, we want to ensure the go version matches GO_VERSION
    GOTOOLCHAIN="go${GO_VERSION}"
    export GOTOOLCHAIN
  fi

  if [[ -z "$(command -v go)" ]]; then
    cat <<EOF
Can't find 'go' in PATH, please fix and retry.
See http://golang.org/doc/install for installation instructions.
EOF
    return 2
  fi

  local go_version
  IFS=" " read -ra go_version <<< "$(GOFLAGS='' go version)"

  # Get minimum go version from go.mod
  local minimum_go_version
  if [[ -f "${ROOT_PATH}/go.mod" ]]; then
    minimum_go_version="go$(awk '/^go [0-9]/ {print $2}' "${ROOT_PATH}/go.mod")"
  else
    minimum_go_version="go${GO_VERSION}"
  fi

  if [[ "${minimum_go_version}" != $(echo -e "${minimum_go_version}\n${go_version[2]}" | sort -s -t. -k 1,1 -k 2,2n -k 3,3n | head -n1) && "${go_version[2]}" != "devel" ]]; then
    cat <<EOF
Detected go version: ${go_version[*]}.
Requires ${minimum_go_version} or greater.
Please install ${minimum_go_version} or later.
EOF
    return 2
  fi
}

function golang::build_binaries(){
    # shellcheck disable=SC2030 # goflags and targets are intentionally local to subshell
    (
        golang::setup_env || return 1
        local host_platform
        host_platform="$(go env GOOS)/$(go env GOARCH)"

        local -a platform
        IFS=" " read -r -a platform <<< "${BUILD_PLATFORMS:-}"
        if [[ ${#platform[@]} -eq 0 ]]; then
            platform=("${host_platform}")

        fi
        goflags=()

        local -a targets=()
        for arg; do
            if [[ "${arg}" == -* ]]; then
                # Assume arguments starting with a dash are flags to pass to go.
                goflags+=("${arg}")
            else
                targets+=("${arg}")
            fi
        done


        if [[ ${#targets[@]} -eq 0 ]]; then
            targets=("${SERVER_TARGETS[@]}")
        fi


        for platform in "${platform[@]}"; do (
            golang::set_platform_envs "${platform}"
            golang::build_binaries_for_plattform "${platform}"
            )
        done
    )

}


# Returns a sorted newline-separated list containing only duplicated items.
function golang::dups() {
  # We use printf to insert newlines, which are required by sort.
  printf "%s\n" "$@" | sort | uniq -d
}

# Returns a sorted newline-separated list with duplicated items removed.
function golang::dedup() {
  # We use printf to insert newlines, which are required by sort.
  printf "%s\n" "$@" | sort -u
}

# golang::read-array reads stdin line-by-line into the named array.
# Safe with empty input (unlike `IFS read -ra` which returns exit 1 on EOF).
function golang::read-array() {
  local __i=0
  while IFS= read -r "$1[__i++]"; do :; done
  if ! eval "[[ \${$1[--__i]} ]]"; then
    unset "$1[__i]"
  fi
}

declare -a SERVER_PLATFORMS
function golang::setup_platforms() {
    if [[ -n "${BUILD_PLATFORMS:-}" ]]; then
        local -a platforms
        IFS=" " read -ra platforms <<< "${BUILD_PLATFORMS}"

        golang::read-array platforms < <(golang::dedup "${platforms[@]}")

        golang::read-array SERVER_PLATFORMS < <(golang::dups \
            "${platforms[@]}" \
            "${SUPPORTED_SERVER_PLATFORMS[@]}"
        )
        readonly SERVER_PLATFORMS; export SERVER_PLATFORMS
    else
        SERVER_PLATFORMS=("${SUPPORTED_SERVER_PLATFORMS[@]}")
        readonly SERVER_PLATFORMS; export SERVER_PLATFORMS
    fi
}

golang::setup_platforms


function golang::host_platform() {
  echo "$(go env GOHOSTOS)/$(go env GOHOSTARCH)"
}

# shellcheck disable=SC2178,SC2128 # platform here is a string, different from array in build_binaries
function golang::set_platform_envs() {
  local -r platform="$1"
  if [[ -z "$platform" ]]; then
      echo "No platform specified"
      return 1
  fi

  export GOOS=${platform%/*}
  export GOARCH=${platform##*/}
  # modernc.org/sqlite is pure Go; keep builds CGO-free on every platform.
  export CGO_ENABLED=0
}

function golang::setup_env() {
  # Set up Go environment following Kubernetes pattern
  # GOPATH is used for go install output, binaries will be copied to final location
  export GOPATH="${CA_GOPATH}"

  # Configure build and module caches
  export GOCACHE="${GOCACHE:-"${CA_GOPATH}/cache/build"}"
  export GOMODCACHE="${GOMODCACHE:-"${CA_GOPATH}/cache/mod"}"

  # Make sure our own Go binaries are in PATH
  export PATH="${CA_GOPATH}/bin:${PATH}"

  # Unset GOBIN to ensure cross-compilation works properly
  # go install will use GOPATH/bin with platform subdirs for cross-compilation
  unset GOBIN

  # Use default Go module and workspace behavior
  unset GO111MODULE
  unset GOWORK

  # Ensure we're in the correct module directory
  if [[ ! -f "go.mod" ]]; then
    echo "Error: go.mod not found. Please run from the module root directory."
    return 1
  fi

  golang::verify_go_version
}


# Place binary from GOPATH/bin to final output location (Kubernetes pattern)
function golang::place_binary() {
    local -r target_name="$1"
    local source_binary
    local target_output

    # Determine where go install placed the binary
    if [[ "${GOOS}" == "$(go env GOHOSTOS)" && "${GOARCH}" == "$(go env GOHOSTARCH)" ]]; then
        # Host platform: binary is in GOPATH/bin
        source_binary="${CA_GOPATH}/bin/${target_name}"
    else
        # Cross-compilation: binary is in GOPATH/bin/GOOS_GOARCH
        source_binary="${CA_GOPATH}/bin/${GOOS}_${GOARCH}/${target_name}"
    fi

    # Windows binaries have .exe extension
    if [[ "${GOOS}" == "windows" ]]; then
        source_binary="${source_binary}.exe"
        target_name="${target_name}.exe"
    fi

    # Final output location
    target_output="${OUTPUT_BINPATH}/${platform}/${target_name}"
    mkdir -p "$(dirname "${target_output}")"

    # Copy binary to final location
    if [[ -f "${source_binary}" ]]; then
        cp "${source_binary}" "${target_output}"
        echo "Placed binary: ${target_output}"
    else
        echo "Error: Binary not found at ${source_binary}"
        return 1
    fi
}

function golang::build_binaries_for_plattform() {
    local platform_ldflags
    local -r platform="$1"
    local arch="${platform##*/}"
    local os="${platform%%/*}"

    # Environment is already set up in main build_binaries function

    local platform_ldflags
    platform_ldflags="-X main.PlatformName=${platform}"
    local ldflags_value
    ldflags_value="-w ${platform_ldflags} -X main.BuildArch=${arch} -X main.BuildOs=${os} ${LD_FLAGS:-} $(version::ldflags)"
    export ldflags="${ldflags_value}"

    local -a binaries=()

    # shellcheck disable=SC2031 # targets is set in parent function
    for target in "${targets[@]}"; do
        binaries+=("${target}")
    done

    # Build regular binaries
    for target in "${binaries[@]}"; do
        golang::build_binary "${target}" || {
            echo "Failed to build ${target} for platform ${platform}"
            return 1
        }
    done
}


function golang::build_binary() {
    local -r target_path="$1"
    local -r target_name="${target_path##*/}"

    # For external modules (like github.com/...), don't add ./
    # For local packages (like cmd/...), add ./
    local source
    if [[ "${target_path}" == github.com/* ]] || [[ "${target_path}" == golang.org/* ]] || [[ "${target_path}" == gopkg.in/* ]]; then
        source="${target_path}"
    else
        source="./${target_path}"
    fi

    : "${CGO_ENABLED=0}"
    : "${GO_LINKMODE=static}"
    : "${GO_BUILDMODE=}"
    : "${GO_BUILDTAGS=}"
    : "${GO_STRIP=}"

    echo "Building $GO_LINKMODE ${target_name}"

    # Build command
    local build_cmd
    # shellcheck disable=SC2031,SC2206 # goflags is set in parent function, GO_BUILDMODE may be empty
    if [[ "${GO_LINKMODE}" == "static" ]]; then
        build_cmd=(env CGO_ENABLED="${CGO_ENABLED}" go install \
            ${goflags:+"${goflags[@]}"} \
            -installsuffix=static \
            -tags="${GO_BUILDTAGS}" \
            -ldflags="${ldflags}" \
            ${GO_BUILDMODE} \
            "${source}")
    else
        # shellcheck disable=SC2031,SC2206
        build_cmd=(go install \
            ${goflags:+"${goflags[@]}"} \
            -tags="${GO_BUILDTAGS}" \
            -ldflags="${ldflags}" \
            ${GO_BUILDMODE} \
            "${source}")
    fi

    local build_cmd_output
    build_cmd_output=$("${build_cmd[@]}" 2>&1) || {
        cat <<EOF >&2
Error building ${target_name}:
${build_cmd_output}
EOF
        return 1
    }

    # Following Kubernetes pattern: go install places binaries in GOPATH/bin,
    # then we copy them to the desired output structure
    golang::place_binary "${target_name}" || {
        echo "Failed to place binary ${target_name}"
        return 1
    }

    echo "Built ${target_name}"
}
