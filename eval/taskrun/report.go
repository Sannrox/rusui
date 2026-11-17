package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// scorerVersion changes whenever scoring rules change, so evidence from
// an older scorer is stale.
const scorerVersion = 1

// Task statuses. Only verified counts as a success.
const (
	StatusVerified    = "verified"     // checks fail on base, pass on head, gate passes
	StatusFailed      = "failed"       // a check fails on the head, or the gate fails
	StatusInvalid     = "invalid"      // a check already passes on the base
	StatusNotVerified = "not_verified" // no published head: blocked, unconfirmed, timeout
)

// historyLimit is stated in every report: guests can read public history.
const historyLimit = "guests can reach the source repository's public history, so a task's upstream fix may be visible to the agent"

// Report is one run's evidence. It applies only to identical inputs.
type Report struct {
	Version      int          `json:"version"`
	Revision     string       `json:"revision"`
	Guest        string       `json:"guest"`
	PolicyDigest string       `json:"policy_digest"`
	CorpusDigest string       `json:"corpus_digest"`
	Limit        string       `json:"limit"`
	Tasks        []TaskResult `json:"tasks"`
	Totals       Totals       `json:"totals"`
}

// TaskResult is one task's outcome and the plane's measures for it.
type TaskResult struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	Head          string `json:"head,omitempty"`
	Checks        string `json:"checks,omitempty"` // pass or fail on the head
	Regression    bool   `json:"regression"`
	Interventions int    `json:"interventions"`
	Tokens        *int64 `json:"tokens"` // nil when any turn's usage is unknown
	WallClockMS   int64  `json:"wall_clock_ms"`
}

type Totals struct {
	Tasks       int            `json:"tasks"`
	Statuses    map[string]int `json:"statuses"`
	Regressions int            `json:"regressions"`
	Tokens      int64          `json:"tokens"`
	// UnknownTokens counts tasks whose usage is missing from Tokens.
	UnknownTokens int   `json:"unknown_tokens"`
	WallClockMS   int64 `json:"wall_clock_ms"`
	// TokensPerVerified is nil without a verified task.
	TokensPerVerified *int64 `json:"tokens_per_verified"`
}

func totals(tasks []TaskResult) Totals {
	t := Totals{Tasks: len(tasks), Statuses: map[string]int{}}
	for _, r := range tasks {
		t.Statuses[r.Status]++
		if r.Regression {
			t.Regressions++
		}
		if r.Tokens == nil {
			t.UnknownTokens++
		} else {
			t.Tokens += *r.Tokens
		}
		t.WallClockMS += r.WallClockMS
	}
	if n := int64(t.Statuses[StatusVerified]); n > 0 {
		per := t.Tokens / n
		t.TokensPerVerified = &per
	}
	return t
}

// sameEvidence names the first input that differs between two reports.
func sameEvidence(prior, current Report) error {
	for _, f := range []struct{ name, a, b string }{
		{"scorer version", fmt.Sprint(prior.Version), fmt.Sprint(current.Version)},
		{"revision", prior.Revision, current.Revision},
		{"guest", prior.Guest, current.Guest},
		{"policy", prior.PolicyDigest, current.PolicyDigest},
		{"corpus", prior.CorpusDigest, current.CorpusDigest},
	} {
		if f.a != f.b {
			return fmt.Errorf("%s changed: %q, now %q", f.name, f.a, f.b)
		}
	}
	return nil
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// preserve writes the report under a name derived from its inputs. The
// write goes to a temporary file that is linked into place, so a crash
// never leaves a partial report and an existing report is never replaced.
func preserve(dir string, r Report, body []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("tasks-%s-%s-%s-%s.json", unsafeName.ReplaceAllString(r.Revision, "_"),
		digest([]byte(r.Revision + "\x00" + r.Guest))[:8], r.PolicyDigest[:12], r.CorpusDigest[:12])
	path := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".tasks-*.json")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Link(tmp.Name(), path); errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("%s exists; prior results are kept", path)
	} else if err != nil {
		return "", err
	}
	return path, nil
}
