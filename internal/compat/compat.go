// Package compat maps the supported 1.0 core contract (ADR 0086) onto
// executable positive and incompatible-input checks.
package compat

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/ops"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

const (
	PolicyVersion = 2
	// Profile is the unattended 1.0 topology diagnose reports.
	Profile = ops.Topology
)

// Class is the ADR 0086 support class for one surface.
const (
	ClassSupported       = "supported"
	ClassAuthorValidated = "author-validated"
	ClassExperimental    = "experimental"
	ClassExcluded        = "excluded"
)

// Surface is one versioned API, runner, policy, export, or artifact in the
// 1.0 contract. Tests named in Positive and Negative must exist.
type Surface struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Class    string `json:"class"`
	Positive string `json:"positive,omitempty"`
	Negative string `json:"negative,omitempty"`
}

// Surfaces is the C1 inventory. Unselected optional tracks stay excluded
// so they cannot become mandatory by accident.
func Surfaces() []Surface {
	schema := strconv.Itoa(store.CurrentSchema)
	return []Surface{
		{Name: "sqlite-schema", Version: schema, Class: ClassSupported, Positive: "TestV1DatabaseUpgradesInPlace", Negative: "TestNewerSchemaIsRejected"},
		{Name: "schema-upgrade", Version: "forward-only", Class: ClassSupported, Positive: "TestCompletedUpgradeClearsStaleLock", Negative: "TestInterruptedUpgradeFailsClosed"},
		{Name: "policy", Version: "v2", Class: ClassSupported, Positive: "TestParseExampleAndFixture", Negative: "TestPolicyV1AndUnknownFieldsFailClosed"},
		{Name: "operator-api", Version: "http+sse", Class: ClassSupported, Positive: "TestCreateAndFollowUpOmitOptionalRequestFields", Negative: ""},
		{Name: "independent-client", Version: "http+sse", Class: ClassSupported, Positive: "TestIndependentClientJourney", Negative: "TestIndependentClientAuthRefused"},
		{Name: "runner-protocol", Version: "hello/claim/heartbeat/complete/fail", Class: ClassSupported, Positive: "TestRunnerHelloAcceptsMatchingAndUnknownCommits", Negative: "TestRunnerHelloRejectsMismatchedCommit"},
		{Name: "restore", Version: "rusui restore -db", Class: ClassSupported, Positive: "TestRestorePreservesIdentitiesRetentionAndReceipts", Negative: "TestRestoreMissingAndCorruptArtifacts"},
		{Name: "diagnostics-export", Version: "redacted-bundle", Class: ClassSupported, Positive: "TestWriteBundleRedactsSecretsAndRecordsIdentity", Negative: "TestWriteBundleRedactsOperatorTokenAndModelUpstreamUserinfo"},
		{Name: "review-receipt-export", Version: "json-md-v1", Class: ClassSupported, Positive: "TestExportReceiptFromStoredReview", Negative: "TestExportReceiptIsNotRecoveryInput"},
		{Name: "signed-portable-session-bundle", Version: "", Class: ClassExcluded, Positive: "", Negative: ""},
		{Name: "postgres", Version: "", Class: ClassExcluded, Positive: "", Negative: ""},
		{Name: "ha-plane", Version: "", Class: ClassExcluded, Positive: "", Negative: ""},
		{Name: "mixed-binary-commits", Version: "", Class: ClassExcluded, Positive: "", Negative: "TestRunnerHelloRejectsMismatchedCommit"},
	}
}

// Report is the operator-facing conformance result for one candidate
// commit and schema.
type Report struct {
	Commit   string        `json:"commit"`
	Schema   int           `json:"schema"`
	Policy   int           `json:"policy"`
	Profile  string        `json:"profile"`
	Surfaces []Surface     `json:"surfaces"`
	Checks   []CheckResult `json:"checks"`
}

// CheckResult is one executable check from SelfCheck.
type CheckResult struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Err  string `json:"err,omitempty"`
}

// SelfCheck runs the in-process 1.0 compatibility checks against a
// throwaway database in dir. It does not start a plane.
func SelfCheck(dir string) (Report, error) {
	rep := Report{
		Schema:   store.CurrentSchema,
		Policy:   PolicyVersion,
		Profile:  Profile,
		Surfaces: Surfaces(),
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return rep, err
	}
	run := func(name string, fn func() error) {
		cr := CheckResult{Name: name, OK: true}
		if err := fn(); err != nil {
			cr.OK = false
			cr.Err = err.Error()
		}
		rep.Checks = append(rep.Checks, cr)
	}
	run("schema-current", func() error {
		st, err := store.Open(filepath.Join(dir, "rusui.db"))
		if err != nil {
			return err
		}
		defer func() { _ = st.Close() }()
		ver, err := store.SchemaVersion(st)
		if err != nil {
			return err
		}
		if ver != store.CurrentSchema {
			return fmt.Errorf("schema %d, want %d", ver, store.CurrentSchema)
		}
		return nil
	})
	run("policy-v1-rejected", func() error {
		_, err := policy.Parse([]byte("version: 1\nprojects: {}\n"))
		if err == nil {
			return fmt.Errorf("policy v1 accepted")
		}
		if !strings.Contains(err.Error(), "unsupported version") {
			return err
		}
		return nil
	})
	run("policy-unknown-field-rejected", func() error {
		_, err := policy.Parse([]byte("version: 2\nprojects:\n  test:\n    extra: true\n    repos: {}\n"))
		if err == nil {
			return fmt.Errorf("unknown field accepted")
		}
		return nil
	})
	run("schema-too-new-rejected", func() error {
		path := filepath.Join(dir, "future.db")
		st, err := store.Open(path)
		if err != nil {
			return err
		}
		if _, err := st.DB.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
			store.CurrentSchema+1, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			_ = st.Close()
			return err
		}
		if err := st.Close(); err != nil {
			return err
		}
		_, err = store.Open(path)
		if err == nil {
			return fmt.Errorf("newer schema accepted")
		}
		if !strings.Contains(err.Error(), "newer than this binary") {
			return err
		}
		return nil
	})
	run("restore-preserves-schema", func() error {
		src := filepath.Join(dir, "copy.db")
		st, err := store.Open(src)
		if err != nil {
			return err
		}
		if err := st.Close(); err != nil {
			return err
		}
		dst := filepath.Join(dir, "restored.db")
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			return err
		}
		st2, _, err := store.Restore(dst)
		if err != nil {
			return err
		}
		defer func() { _ = st2.Close() }()
		ver, err := store.SchemaVersion(st2)
		if err != nil {
			return err
		}
		if ver != store.CurrentSchema {
			return fmt.Errorf("restored schema %d", ver)
		}
		return nil
	})
	for _, c := range rep.Checks {
		if !c.OK {
			return rep, fmt.Errorf("compat: %s: %s", c.Name, c.Err)
		}
	}
	return rep, nil
}
