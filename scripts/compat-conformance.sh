#!/usr/bin/env bash
# Reproduce the 1.0 compatibility checks for this checkout (issue #142).
# Run from the repository root. Prints the binary report, then the Go tests
# that map each supported surface to a positive and incompatible-input check.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
# Mapped surface tests come from the catalog; the second group is the
# catalog's own checks and unmapped tests kept next to them.
mapped="$(go run ./cmd/rusui compat-conformance -list-tests)"
go test ./internal/compat ./internal/store ./internal/policy ./internal/ops ./internal/receipt ./internal/server ./cmd/rusui ./examples/independent-client \
  -count=1 -timeout 120s \
  -run "${mapped}|^(TestSurfacesCover|TestMappedTestsExist|TestSelfCheck|TestCompatConformanceMain|TestCreateAndFollowUpOmit|TestIndependentClient)"
go run ./cmd/rusui compat-conformance
