package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const ResultSchema = 1

// TaskResult is the P1/P3 versioned guest result. Agent claims are not
// trusted verification (ADR 0013).
type TaskResult struct {
	SchemaVersion int       `json:"schema_version"`
	EffortKey     string    `json:"effort_key,omitempty"`
	SourceHash    string    `json:"source_hash"`
	CandidateSHA  string    `json:"candidate_sha,omitempty"`
	Findings      []Finding `json:"findings,omitempty"`
	ClaimedChecks []string  `json:"claimed_checks,omitempty"`
	BlockedReason string    `json:"blocked_reason,omitempty"`
	AgentVerified bool      `json:"agent_verified,omitempty"`
}

type Finding struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

func (r TaskResult) Hash() string {
	b, _ := json.Marshal(r)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func ValidateResult(art Artifact) error {
	if art.ItemKind != "run" {
		return nil
	}
	if art.Result == nil {
		return fmt.Errorf("result required")
	}
	r := art.Result
	if r.SchemaVersion != ResultSchema {
		return fmt.Errorf("unsupported result schema")
	}
	if r.SourceHash == "" || r.SourceHash != art.SnapshotHash {
		return fmt.Errorf("source mismatch")
	}
	if r.AgentVerified {
		return fmt.Errorf("agent cannot declare verified")
	}
	if len(r.Findings) == 0 && r.BlockedReason == "" {
		return fmt.Errorf("empty result")
	}
	return nil
}
