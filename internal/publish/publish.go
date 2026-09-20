// Package publish is the fail-closed gate for exact-artifact verification
// and plane-owned publication (ADR 0013). It does not talk to GitHub.
package publish

import (
	"fmt"
	"strconv"
	"strings"
)

const MaxRepair = 3

type Outcome string

const (
	Denied            Outcome = "denied"
	Cancelled         Outcome = "cancelled"
	SourceDrift       Outcome = "source_drift"
	ChecksUnavailable Outcome = "checks_unavailable"
	Ambiguous         Outcome = "ambiguous"
	Proven            Outcome = "proven"
)

type Action string

const (
	ActionOpenPR   Action = "open_pr"
	ActionUpdatePR Action = "update_pr"
)

type Source struct {
	Repo         string `json:"repo"`
	Item         int    `json:"item"`
	SnapshotHash string `json:"snapshot_hash"`
	MainSHA      string `json:"main_sha"`
}

type Candidate struct {
	SessionID int64  `json:"session_id"`
	CommitSHA string `json:"commit_sha"`
	TreeSHA   string `json:"tree_sha"`
	Ref       string `json:"ref"`
}

type Proof struct {
	Command                string
	ExitCode               int
	LogDigest              string
	HeadSHA                string
	BaseSHA                string
	Isolated               bool
	HadWriteCreds          bool
	AuthorSessionID        int64
	ReviewerSessionID      int64
	RepairCount            int
	SourceSnapshotHash     string
	Expired                bool
	ChecksAvailable        bool
	AgentDeclaredVerified  bool
	PolicyMutated          bool
	RequiredChecksDisabled bool
}

type Attempt struct {
	Source    Source
	Candidate Candidate
	Proof     Proof
	Action    Action
	Cancelled bool
}

// Permitted reports whether action is in the D3 set. Unnamed writes are not.
func Permitted(action Action) bool {
	return action == ActionOpenPR || action == ActionUpdatePR
}

// Evaluate returns the publication-gate outcome. Proven is not published;
// a later publisher may consume only Proven. Live GitHub writes are out.
func Evaluate(a Attempt) Outcome {
	if a.Cancelled {
		return Cancelled
	}
	if !Permitted(a.Action) {
		return Denied
	}
	if a.Proof.HadWriteCreds || a.Proof.AgentDeclaredVerified || a.Proof.PolicyMutated || a.Proof.RequiredChecksDisabled {
		return Denied
	}
	if !a.Proof.Isolated {
		return Denied
	}
	if a.Proof.Expired {
		return Denied
	}
	if a.Proof.RepairCount > MaxRepair {
		return Denied
	}
	if a.Source.Repo == "" || a.Source.SnapshotHash == "" || a.Candidate.CommitSHA == "" {
		return Denied
	}
	if err := refOK(a.Candidate); err != nil {
		return Denied
	}
	if a.Proof.SourceSnapshotHash != a.Source.SnapshotHash {
		return SourceDrift
	}
	if a.Proof.HeadSHA != a.Candidate.CommitSHA {
		return Ambiguous
	}
	if a.Proof.ExitCode != 0 {
		return Denied
	}
	if !a.Proof.ChecksAvailable {
		return ChecksUnavailable
	}
	if a.Proof.ReviewerSessionID == 0 || a.Proof.ReviewerSessionID == a.Proof.AuthorSessionID {
		return Denied
	}
	if a.Proof.AuthorSessionID != 0 && a.Proof.AuthorSessionID != a.Candidate.SessionID {
		return Denied
	}
	return Proven
}

func refOK(c Candidate) error {
	if c.SessionID == 0 {
		return fmt.Errorf("session")
	}
	want := "refs/heads/rusui/" + strconv.FormatInt(c.SessionID, 10) + "/"
	if !strings.HasPrefix(c.Ref, want) || c.Ref == want {
		return fmt.Errorf("ref")
	}
	rest := strings.TrimPrefix(c.Ref, want)
	if strings.Contains(rest, "..") || strings.Contains(rest, "//") {
		return fmt.Errorf("ref")
	}
	return nil
}
