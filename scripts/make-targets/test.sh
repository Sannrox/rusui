#!/usr/bin/env bash
#
set -o errexit
set -o nounset
set -o pipefail


# shellcheck disable=SC2034 # ROOT_PATH may be used by sourced scripts
ROOT_PATH=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
cd "${ROOT_PATH}"


TIMEOUT=${TIMEOUT:--timeout=180s}
RACE=${RACE-"-race"}
JUNIT_REPORT_DIR=${JUNIT_REPORT_DIR:-}

# Activate cover mode with '1'
COVER=${COVER:-0}
COVERMODE=${COVERMODE:-atomic}

#The directory to save the generatet coverage reports. If not set, a temporary directory will be used.
COVER_REPORT_DIR="${COVER_REPORT_DIR:-}"

# How many 'go test' process run simultaneously when running tests in coverage mode
COVERPROCS="${COVERPROCS:-4}"

# test::check_for_gotestsum checks if gotestsum is installed and installs it if not.
function test::check_for_gotestsum() {
    if ! command -v gotestsum &> /dev/null; then
        go install gotest.tools/gotestsum@v1.12.3
    fi
}

# test::find_dirs finds all directories containing test files.
function test::find_dirs(){
(
    find -L . -not \( \
        \( \
          -path './_output/*' \
        \) -prune \
      \) -name '*_test.go' -print0 | xargs -0n1 dirname | LC_ALL=C sort -u
    )
}

# test::produce_junit_report produces a junit report from the test output.
function test::produce_junit_report() {
   local -r junit_filename_prefix=$1
   if [[ -z "${junit_filename_prefix}" ]]; then
       return
   fi
   local junit_xml_filename
   junit_xml_filename="${junit_filename_prefix}.xml"

   go mod download
   test::check_for_gotestsum

   gotestsum --junitfile "${junit_xml_filename}" --raw-command cat "${junit_filename_prefix}"*.stdout

   rm -f "${junit_filename_prefix}.stdout"

}

# junit_filename_prefix_create creates a filename prefix for junit report.
function junit_filename_prefix_create(){
  if [[ -z "${JUNIT_REPORT_DIR}" ]]; then
    echo ""
    return
  fi
  mkdir -p "${JUNIT_REPORT_DIR}"
  echo "${JUNIT_REPORT_DIR}/gotest_$(date +'%Y%m%d_%H%M%S_%N')"

}

function run_tests_with_codecoverage() {
    local junit_filename_prefix
    junit_filename_prefix=$(junit_filename_prefix_create)


    echo "Running tests with code coverage"

    if [[ -z "${COVER_REPORT_DIR}" ]]; then
      cover_report_dir="/tmp/coverage/$(date "+%Y%m%d-%H%M%S")"
    else
      cover_report_dir="${COVER_REPORT_DIR}"
    fi
    cover_profile="coverage.out"  # Name for each individual coverage profile
    echo "Writting coverage output in '${cover_report_dir}'"
    mkdir -p "${@+${@/#/${cover_report_dir}/}}"

    local -a cmd=(
      go test "${goflags[@]:+${goflags[@]}}"
        "${TIMEOUT}"
        -cover -covermode="${COVERMODE}"
        -coverprofile="${cover_report_dir}/${cover_profile}"
        "$@"
        "${testargs[@]:+${testargs[@]}}"
    )
    if [[ -n "${junit_filename_prefix}" ]]; then
      "${cmd[@]}" \
      | tee "${junit_filename_prefix}.stdout" \
      | grep "${go_test_grep_pattern}" \
      && test_result=$? || test_result=$?
    else
      "${cmd[@]}" \
      | grep "${go_test_grep_pattern}" \
      && test_result=$? || test_result=$?
    fi

    test::produce_junit_report "${junit_filename_prefix}"

    # shellcheck disable=SC2034 # COMBINED_COVER_PROFILE used later
    COMBINED_COVER_PROFILE="${cover_report_dir}/combined-coverage.out"


    coverage_html_file="${cover_report_dir}/coverage.html"
    go tool cover -html="${cover_report_dir}/${cover_profile}" -o="${coverage_html_file}"

    return "${test_result}"
}

# Use eval to preserve embedded quoted strings.
testargs=()
eval "testargs=(${TEST_ARGS:-})"

function run_tests() {
      local junit_filename_prefix
      junit_filename_prefix=$(junit_filename_prefix_create)

      local -a targets
      IFS=" " read -r -a targets <<< "$@"

      if [[ -n "${junit_filename_prefix}" ]]; then
        CGO_ENABLED=1 go test "${goflags[@]+${goflags[@]}}" \
            "${TIMEOUT}" "${targets[@]}" \
            "${testargs[@]:+${testargs[@]}}" \
            | tee "${junit_filename_prefix}.stdout" \
          && test_result=$? || test_result=$?
      else
        CGO_ENABLED=1 go test "${goflags[@]+${goflags[@]}}" \
            "${TIMEOUT}" "${targets[@]}" \
            "${testargs[@]:+${testargs[@]}}" \
          && test_result=$? || test_result=$?
      fi

      test::produce_junit_report "${junit_filename_prefix}"

      return "${test_result}"
}

goflags=()

# Filter go test verbose output
go_test_grep_pattern=".*"

# The junit report tool needs full test case information to produce a
# meaningful report.
if [[ -n "${JUNIT_REPORT_DIR}" ]]; then
    goflags+=("-v")
    goflags+=("-json")
fi
testcases=()

# Filter out arguments that start with "-" and move them to goflags.
for arg in "$@"; do
    if [[ $arg == -* ]]; then
        goflags+=("$arg")
    else
        testcases+=("$arg")
    fi
done

if [ ${#testcases[@]} -eq 0 ]; then
    mapfile -t testcases < <(test::find_dirs)
fi

set -- "${testcases[@]+${testcases[@]}}"

if [[ -n "${RACE}" ]] ; then
  goflags+=("${RACE}")
fi

if [[ ${COVER} != 0 ]]; then
    run_tests_with_codecoverage "$@"
else
    run_tests "$@"
fi
