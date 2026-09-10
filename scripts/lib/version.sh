#!/usr/bin/env bash
#
#

set -o errexit
set -o nounset
set -o pipefail

function version::get_version() {
    if [[ -n ${GIT_VERSION_FILE-} ]]; then
      version::load_version_vars "${GIT_VERSION_FILE}"
      return
    fi
  # If the source was exported through git archive, then  # we likely don't have a git tree, but these magic values may be filled in.
  # shellcheck disable=SC2016,SC2050
  # Disabled as we're not expanding these at runtime, but rather expecting
  # that another tool may have expanded these and rewritten the source (!)
  if [[ '$Format:%%$' == "%" ]]; then
    GIT_COMMIT='$Format:%H$'
    GIT_TREE_STATE="archive"
    # When a 'git archive' is exported, the '$Format:%D$' below will look
    # something like 'HEAD -> release-1.8, tag: v1.8.3' where then 'tag: '
    # can be extracted from it.
    if [[ '$Format:%D$' =~ tag:\ (v[^ ,]+) ]]; then
     GIT_VERSION="${BASH_REMATCH[1]}"
    fi
  fi
 local git=(git --work-tree "${ROOT_PATH}")

  if [[ -n ${GIT_COMMIT-} ]] || GIT_COMMIT=$("${git[@]}" rev-parse "HEAD^{commit}" 2>/dev/null); then
    if [[ -z ${GIT_TREE_STATE-} ]]; then
      # Check if the tree is dirty.  default to dirty
      if git_status=$("${git[@]}" status --porcelain 2>/dev/null) && [[ -z ${git_status} ]]; then
        GIT_TREE_STATE="clean"
      else
        GIT_TREE_STATE="dirty"
      fi
    fi

    # Use git describe to find the version based on tags.
    if [[ -n ${GIT_VERSION-} ]] || GIT_VERSION=$("${git[@]}" describe --tags --match='v*' --abbrev=14 "${GIT_COMMIT}^{commit}" 2>/dev/null); then
      # This translates the "git describe" to an actual semver.org
      # compatible semantic version that looks something like this:
      #   v1.1.0-alpha.0.6+84c76d1142ea4d
      #
      # These regexes are painful enough in sed...
      # We don't want to do them in pure shell, so disable SC2001
      # shellcheck disable=SC2001
      DASHES_IN_VERSION=$(echo "${GIT_VERSION}" | sed "s/[^-]//g")
      if [[ "${DASHES_IN_VERSION}" == "---" ]] ; then
        # shellcheck disable=SC2001
        # We have distance to subversion (v1.1.0-subversion-1-gCommitHash)
        GIT_VERSION=$(echo "${GIT_VERSION}" | sed "s/-\([0-9]\{1,\}\)-g\([0-9a-f]\{14\}\)$/.\1\+\2/")
      elif [[ "${DASHES_IN_VERSION}" == "--" ]] ; then
        # shellcheck disable=SC2001
        # We have distance to base tag (v1.1.0-1-gCommitHash)
        GIT_VERSION=$(echo "${GIT_VERSION}" | sed "s/-g\([0-9a-f]\{14\}\)$/+\1/")
      fi
      if [[ "${GIT_TREE_STATE}" == "dirty" ]]; then
        # git describe --dirty only considers changes to existing files, but
        # that is problematic since new untracked .go files affect the build,
        # so use our idea of "dirty" from git status instead.
        if [[ ! "${GIT_VERSION}" =~ -dirty$ ]]; then
          GIT_VERSION+="-dirty"
        fi
      fi


      # Try to match the "git describe" output to a regex to try to extract
      # the "major" and "minor" versions and whether this is the exact tagged
      # version or whether the tree is between two tagged versions.
      if [[ "${GIT_VERSION}" =~ ^v([0-9]+)\.([0-9]+)(\.[0-9]+)?([-].*)?([+].*)?$ ]]; then
        GIT_MAJOR=${BASH_REMATCH[1]}
        GIT_MINOR=${BASH_REMATCH[2]}
        if [[ -n "${BASH_REMATCH[4]}" ]]; then
          GIT_MINOR+="+"
        fi
      fi

      # If GIT_VERSION is not a valid Semantic Version, then refuse to build.
      if ! [[ "${GIT_VERSION}" =~ ^v([0-9]+)\.([0-9]+)(\.[0-9]+)?(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]]; then
          echo "Version ${GIT_VERSION} is not a semantic version"
          echo "Please see more details here: https://semver.org"
          exit 1
      fi
    fi
  fi
}


function version::ldflags() {
  version::get_version

  local -a ldflags
  function add_ldflag() {
    local key=${1}
    local val=${2}
    ldflags+=(
      "-X 'main.${key}=${val}'"
    )
  }


  add_ldflag "BuildTime" "$(date ${SOURCE_DATE_EPOCH:+"--date=@${SOURCE_DATE_EPOCH}"} -u +'%Y-%m-%dT%H:%M:%SZ')"
  if [[ -n ${GIT_COMMIT-} ]]; then
    add_ldflag "GitCommit" "${GIT_COMMIT}"
  fi

  if [[ -n ${GIT_VERSION-} ]]; then
    add_ldflag "Version" "${GIT_VERSION}"
  fi

  if [[ -n ${GIT_MAJOR-} && -n ${GIT_MINOR-} ]]; then
    add_ldflag "GitMajor" "${GIT_MAJOR}"
    add_ldflag "GitMinor" "${GIT_MINOR}"
  fi
  # The -ldflags parameter takes a single string, so join the output.
  echo "${ldflags[*]-}"
}

# Saves the environment flags to $1
version::save_version_vars() {
  local version_file=${1-}
  [[ -n ${version_file} ]] || {
    echo "!!! Internal error.  No file specified in version::save_version_vars"
    return 1
  }

  cat <<EOF >"${version_file}"
GIT_COMMIT=${GIT_COMMIT-}
GIT_TREE_STATE=${GIT_TREE_STATE-}
GIT_VERSION=${GIT_VERSION-}
GIT_MAJOR=${GIT_MAJOR-}
GIT_MINOR=${GIT_MINOR-}
EOF
}

# Loads up the version variables from file $1
version::load_version_vars() {
  local version_file=${1-}
  [[ -n ${version_file} ]] || {
    echo "!!! Internal error.  No file specified for load_version_vars"
    return 1
  }

  source "${version_file}"
}
