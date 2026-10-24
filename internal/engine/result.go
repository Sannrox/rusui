package engine

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/sannrox/rusui/internal/store"
)

const ResultSchema = 1

// TaskResult is the P1/P3 versioned guest result. Agent claims are not
// trusted verification (ADR 0013).
type TaskResult struct {
	SchemaVersion int    `json:"schema_version"`
	EffortKey     string `json:"effort_key,omitempty"`
	SourceHash    string `json:"source_hash"`
	CandidateSHA  string `json:"candidate_sha,omitempty"`
	// BranchHeads are the local branch tips the runner observes in the
	// session workspace, including branches checked out in a nested
	// worktree (#469). A claimed pull request at one of them was produced
	// in this workspace even when the workspace HEAD is elsewhere.
	BranchHeads   []string  `json:"branch_heads,omitempty"`
	Findings      []Finding `json:"findings,omitempty"`
	ClaimedChecks []string  `json:"claimed_checks,omitempty"`
	BlockedReason string    `json:"blocked_reason,omitempty"`
	AgentVerified bool      `json:"agent_verified,omitempty"`
	// PullRequest is the pull request the agent claims it opened or
	// updated in the turn's repository (ADR 0015).
	PullRequest int `json:"pull_request,omitempty"`
	// Publish asks the plane to publish the pushed session branch
	// (ADR 0044). Only honored when plane publication is on.
	Publish *PublishRequest `json:"publish,omitempty"`
	// Draft asks the plane to draft definition members (ADR 0084).
	// Applying it is a separate operator Action.
	Draft *DraftRequest `json:"draft,omitempty"`
	// PublishedSHA, Outcome, Publisher, and PushedRef are observed by
	// the plane on Complete; values sent by the runner or guest are discarded.
	PublishedSHA string `json:"published_sha,omitempty"`
	Outcome      string `json:"outcome,omitempty"`
	Publisher    string `json:"publisher,omitempty"`
	// PushedRef is the default branch the plane fast-forwarded for
	// ship=push-base (ADR 0068). Empty when publication is a pull request.
	PushedRef string `json:"pushed_ref,omitempty"`
}

// Turn outcomes the plane records for a run result.
const (
	OutcomePublished   = "published"   // GitHub shows the claimed PR at the candidate SHA
	OutcomeUnconfirmed = "unconfirmed" // a PR was claimed but GitHub does not show it at the candidate SHA
	OutcomeBlocked     = "blocked"     // the agent reported why it stopped
	OutcomeReported    = "reported"    // findings only
	OutcomeProposed    = "proposed"    // the turn asked to write; the Action has not applied it
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
	if len(r.Findings) == 0 && r.BlockedReason == "" && r.PullRequest <= 0 && r.Publish == nil && r.Draft == nil {
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
	pushed := r.PushedRef
	r.PushedRef = ""
	switch {
	case pushed != "" && r.Publisher == PublisherPlane && r.CandidateSHA != "":
		r.PushedRef = pushed
		r.PublishedSHA = r.CandidateSHA
		r.Outcome = OutcomePublished
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
		} else if pr.HeadSHA != "" && slices.Contains(r.BranchHeads, pr.HeadSHA) {
			// The guest committed on a branch of this workspace other than
			// its HEAD, such as one checked out in a nested worktree.
			r.CandidateSHA = pr.HeadSHA
			r.Outcome = OutcomePublished
		}
	case r.BlockedReason != "":
		r.Outcome = OutcomeBlocked
	case r.Publish != nil || r.Draft != nil:
		r.Outcome = OutcomeProposed
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
	prev, ok := priorPublication(e.Store, jobID)
	if !ok {
		return
	}
	prevPR, prevSHA := prev.PullRequest, prev.SHA
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
func priorPublication(s *store.Store, jobID int64) (Publication, bool) {
	p, ok, err := newestPublication(s, `SELECT payload FROM review_revisions WHERE job_id=? ORDER BY id DESC`, jobID)
	if err != nil || !ok {
		return Publication{}, false
	}
	return p, true
}

// Publication is a pull request the plane observed on GitHub at the
// candidate SHA of a run result. Publisher and Branch are set when the
// plane wrote it (ADR 0044).
type Publication struct {
	Repo        string `json:"repo"`
	PullRequest int    `json:"pull_request"`
	SHA         string `json:"sha"`
	Publisher   string `json:"publisher,omitempty"`
	Branch      string `json:"branch,omitempty"`
}

// SessionPublication is the newest GitHub write the operator applied for
// the session, or the newest published review result if no write.apply
// exists. ok is false when nothing has been applied.
func SessionPublication(s *store.Store, sessionID int64) (Publication, bool, error) {
	if p, ok, err := publicationFromWriteApply(s, sessionID); err != nil || ok {
		return p, ok, err
	}
	return newestPublication(s, `SELECT r.payload FROM review_revisions r JOIN turns t ON t.id=r.job_id WHERE t.session_id=? ORDER BY r.id DESC`, sessionID)
}

func publicationFromWriteApply(s *store.Store, sessionID int64) (Publication, bool, error) {
	a, err := store.LatestAction(s, sessionID, ActionWriteApply)
	if errors.Is(err, sql.ErrNoRows) {
		return Publication{}, false, nil
	}
	if err != nil {
		return Publication{}, false, err
	}
	var rec WriteApplyReceipt
	if json.Unmarshal([]byte(a.Body), &rec) != nil || rec.PullRequest <= 0 || rec.SHA == "" {
		return Publication{}, false, nil
	}
	return Publication{Repo: a.Repo, PullRequest: rec.PullRequest, SHA: rec.SHA, Publisher: rec.Publisher, Branch: rec.Branch}, true, nil
}

// priorPublishAsk is the branch the session already proposed, so a
// follow-up cannot switch heads.
func priorPublishAsk(s *store.Store, sessionID int64) (string, bool) {
	rows, err := s.DB.Query(`SELECT r.payload FROM review_revisions r JOIN turns t ON t.id=r.job_id WHERE t.session_id=? ORDER BY r.id DESC`, sessionID)
	if err != nil {
		return "", false
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return "", false
		}
		var prev Artifact
		if json.Unmarshal([]byte(raw), &prev) != nil || prev.Result == nil || prev.Result.Publish == nil {
			continue
		}
		if prev.Result.BlockedReason != "" {
			continue
		}
		if b := prev.Result.Publish.Branch; b != "" {
			return b, true
		}
	}
	return "", false
}

func newestPublication(s *store.Store, query string, id int64) (Publication, bool, error) {
	rows, err := s.DB.Query(query, id)
	if err != nil {
		return Publication{}, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return Publication{}, false, err
		}
		var prev Artifact
		if json.Unmarshal([]byte(raw), &prev) != nil || prev.Result == nil {
			continue
		}
		if r := prev.Result; r.Outcome == OutcomePublished && r.PullRequest > 0 && r.PublishedSHA != "" {
			p := Publication{Repo: prev.Repo, PullRequest: r.PullRequest, SHA: r.PublishedSHA, Publisher: r.Publisher}
			if r.Publisher == PublisherPlane && r.Publish != nil {
				p.Branch = r.Publish.Branch
			}
			return p, true, nil
		}
	}
	return Publication{}, false, rows.Err()
}
