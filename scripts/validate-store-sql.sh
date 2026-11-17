#!/usr/bin/env bash

# This script checks that internal/store is the only SQL boundary
# (ADR 0089 D2), and that store queries avoid SQLite-only forms. Non-test Go code outside internal/store must not import
# database/sql, reach through Store.DB, or call Exec/Query/QueryRow/Prepare
# on a transaction or database handle. Tests may keep SQL.
#
# Usage: scripts/validate-store-sql.sh

set -o errexit
set -o nounset
set -o pipefail

ROOT_PATH="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

cd "${ROOT_PATH}"

# go_files lists non-test Go files outside internal/store, NUL-separated.
go_files() {
    find . \
        \( -path './.git' -o -path './_output' -o -path '*/vendor' -o -path './internal/store' \) -prune \
        -o -type f -name '*.go' ! -name '*_test.go' -print0
}

# check prints matches of an extended regexp and records a failure.
failed=0
check() {
    local label="$1" pattern="$2" hits
    hits="$(go_files | xargs -0 grep -nE "${pattern}" || true)"
    if [[ -n "${hits}" ]]; then
        echo "${label}:" >&2
        echo "${hits}" >&2
        echo >&2
        failed=1
    fi
}

check 'database/sql imported outside internal/store' '^[[:space:]]*(import[[:space:]]+)?([[:alnum:]_.]+[[:space:]]+)?"database/sql"'
check 'Store.DB used outside internal/store' '\.DB\.[[:upper:]]'
check 'SQL run on a transaction or database handle outside internal/store' \
    '\b[[:alnum:]_]*([Tt]x|[Dd][Bb]|[Cc]onn|[Ss]tmt)\.(Exec|Query|QueryRow|Prepare)(Context)?\('
check 'SQL literal passed to Exec/Query/QueryRow/Prepare outside internal/store' \
    '\.(Exec|Query|QueryRow|Prepare)(Context)?\([[:space:]]*([[:alnum:]_]+,[[:space:]]*)?[`"]'

if [[ "${failed}" -ne 0 ]]; then
    echo "Move the statement into a named function in internal/store." >&2
    exit 1
fi

# Store queries use SQL both store backends accept (#734). Migrations are
# per dialect and exempt. The remaining SQLite-only points are rowid
# ordering on actions and receipts, group_concat in FollowToken, and the
# store clock in plane_lease.go; the second backend handles them (#727).
store_hits="$(find internal/store -type f -name '*.go' ! -name '*_test.go' ! -name 'migrate.go' -print0 |
    xargs -0 grep -nE 'INSERT[[:space:]]+OR[[:space:]]+(IGNORE|REPLACE)|LastInsertId\(|datetime\(|julianday\(' || true)"
if [[ -n "${store_hits}" ]]; then
    echo "SQLite-only SQL in internal/store:" >&2
    echo "${store_hits}" >&2
    echo "Use ON CONFLICT ... DO NOTHING / DO UPDATE, INSERT ... RETURNING id, and times passed in from Go." >&2
    exit 1
fi
