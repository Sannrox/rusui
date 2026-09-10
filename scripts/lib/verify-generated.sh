#!/usr/bin/env bash

# verify-generated provides a reusable function for verifying generated code
# Usage: verify::generate <diffroot> <update-script>
#   diffroot: The directory containing generated files to verify
#   update-script: The script that regenerates the files

function verify::generate() {
    ( # subshell prevents environment changes from leaking and allows EXIT trap to work
        local diffroot="${1}"
        local update_script="${2}"

        if [[ ! -d "${diffroot}" ]]; then
            echo "Error: Directory ${diffroot} does not exist" >&2
            return 1
        fi

        if [[ ! -x "${update_script}" ]]; then
            echo "Error: Update script ${update_script} does not exist or is not executable" >&2
            return 1
        fi

        local tmp_diffroot
        tmp_diffroot="$(mktemp -d -t "verify-codegen.XXXXXX")/$(basename "${diffroot}")"
        local tmp_root
        tmp_root="$(dirname "${tmp_diffroot}")"

        # Setup cleanup trap - will fire when subshell exits
        trap 'rm -rf ${tmp_root}' EXIT SIGINT

        # Backup current generated files
        mkdir -p "${tmp_diffroot}"
        if [[ -n "$(ls -A "${diffroot}" 2>/dev/null)" ]]; then
            cp -a "${diffroot}"/* "${tmp_diffroot}/"
        fi

        # Run the update script to regenerate
        echo "Running ${update_script}..."
        "${update_script}"

        # Compare old vs new
        echo "Diffing ${diffroot} against freshly generated code..."
        local ret=0
        diff -Naupr "${diffroot}" "${tmp_diffroot}" || ret=$?

        if [[ $ret -eq 0 ]]; then
            echo "${diffroot} is up to date."
        else
            echo "" >&2
            echo "ERROR: ${diffroot} is out of date." >&2
            echo "Please run: ${update_script}" >&2
            echo "" >&2
        fi

        return $ret
    )
}
