#!/usr/bin/env bash
# Reproduce the 1.0 compatibility checks for this checkout (issue #142).
# Run from the repository root. Prints the binary report, then the Go tests
# that map each supported surface to a positive and incompatible-input check.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
go test ./internal/compat ./internal/store ./internal/policy ./internal/ops ./internal/receipt ./internal/server ./cmd/rusui ./examples/independent-client \
  -count=1 -timeout 120s \
  -run 'TestSurfacesCover|TestSelfCheck|TestPolicyV1|TestV1DatabaseUpgradesInPlace|TestNewerSchemaIsRejected|TestInterruptedUpgradeFailsClosed|TestCompletedUpgradeClearsStaleLock|TestRestorePreservesIdentities|TestRestoreMissingAndCorrupt|TestWriteBundleRedacts|TestExportReceiptFromStoredReview|TestExportReceiptIsNotRecoveryInput|TestParseExampleAndFixture|TestRunnerHello|TestCreateAndFollowUpOmit|TestCompatConformanceMain|TestIndependentClient'
go run ./cmd/rusui compat-conformance
