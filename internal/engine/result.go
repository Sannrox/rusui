package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/sannrox/rusui/internal/store"
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
	// PullRequest is the pull request the agent claims it opened or
	// updated in the turn's repository (ADR 0015).
	PullRequest int `json:"pull_request,omitempty"`
	// PublishedSHA and Outcome are observed by the plane on Complete;
	// values sent by the runner or guest are discarded.
	PublishedSHA string `json:"published_sha,omitempty"`
	Outcome      string `json:"outcome,omitempty"`
}

// Turn outcomes the plane records for a run result.
const (
	OutcomePublished   = "published"   // GitHub shows the claimed PR at the candidate SHA
	OutcomeUnconfirmed = "unconfirmed" // a PR was claimed but GitHub does not show it at the candidate SHA
	OutcomeBlocked     = "blocked"     // the agent reported why it stopped
	OutcomeReported    = "reported"    // findings only
)

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
	if len(r.Findings) == 0 && r.BlockedReason == "" && r.PullRequest <= 0 {
		return fmt.Errorf("empty result")
	}
	return nil
}

// observeResult records what the plane itself sees about a run result: a
// claimed pull request counts as published only when the read-only
// GitHub client shows it in the turn's repository at the candidate SHA
// the runner observed in the workspace.
func (e *Engine) observeResult(art *Artifact) {
	r := art.Result
	if art.ItemKind != "run" || r == nil {
		return
	}
	r.PublishedSHA, r.Outcome = "", ""
	switch {
	case r.PullRequest > 0:
		r.Outcome = OutcomeUnconfirmed
		if e.GitHub == nil {
			return
		}
		pr, err := e.GitHub.GetItem(art.Repo, r.PullRequest, "pull")
		if err != nil || pr.ItemKind != "pull" {
			return
		}
		r.PublishedSHA = pr.HeadSHA
		if r.CandidateSHA != "" && pr.HeadSHA == r.CandidateSHA {
			r.Outcome = OutcomePublished
		}
	case r.BlockedReason != "":
		r.Outcome = OutcomeBlocked
	default:
		r.Outcome = OutcomeReported
	}
}

const followUpUnchangedReason = "follow-up left the pull request head unchanged"

// observeFollowUpPublication refuses to call a follow-up published when
// GitHub still shows the previous head, or when the guest names a
// different pull request. The guest pushes; the plane only records what
// it can see.
func (e *Engine) observeFollowUpPublication(jobID int64, art *Artifact) {
	r := art.Result
	if art.ItemKind != "run" || r == nil || r.PullRequest <= 0 {
		return
	}
	prevPR, prevSHA, ok := priorPublication(e.Store, jobID)
	if !ok {
		return
	}
	if r.PullRequest != prevPR {
		r.Outcome = OutcomeBlocked
		r.BlockedReason = fmt.Sprintf("follow-up must update pull request #%d", prevPR)
		return
	}
	if r.PublishedSHA == prevSHA || r.CandidateSHA == "" || r.CandidateSHA == prevSHA {
		r.Outcome = OutcomeBlocked
		r.BlockedReason = followUpUnchangedReason
	}
}

// priorPublication is the newest result this turn actually published.
// A later blocked or unconfirmed follow-up does not erase it.
func priorPublication(s *store.Store, jobID int64) (pr int, sha string, ok bool) {
	rows, err := s.DB.Query(`SELECT payload FROM review_revisions WHERE job_id=? ORDER BY id DESC`, jobID)
	if err != nil {
		return 0, "", false
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return 0, "", false
		}
		var prev Artifact
		if json.Unmarshal([]byte(raw), &prev) != nil || prev.Result == nil {
			continue
		}
		if prev.Result.Outcome == OutcomePublished && prev.Result.PullRequest > 0 && prev.Result.PublishedSHA != "" {
			return prev.Result.PullRequest, prev.Result.PublishedSHA, true
		}
	}
	if err := rows.Err(); err != nil {
		return 0, "", false
	}
	return 0, "", false
}
