// Package receipt builds a portable JSON and Markdown copy of one completed
// review from immutable SQLite rows. The export is not a recovery input
// and does not authorize GitHub or apply actions (issue #248, ADR 0086).
package receipt

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sannrox/rusui/internal/ops"
	"github.com/sannrox/rusui/internal/store"
)

const (
	// SchemaVersion is the 1.0 portable review-receipt schema.
	SchemaVersion = 1
	redacted      = "[redacted]"
	minSecretLen  = 6
)

var (
	// ErrNoReview means the session has no completed review revision.
	ErrNoReview = errors.New("no completed review")
	tokenShapes = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{20,}|xai-[A-Za-z0-9_-]{20,}|xox[abpr]-[A-Za-z0-9-]{10,})`)
	authHeader  = regexp.MustCompile(`(?i)\b(bearer\s+|basic\s+|authorization:\s*token\s+)[A-Za-z0-9._~+/=-]{16,}`)
)

// Export is a versioned copy of one stored review and its action rows.
type Export struct {
	SchemaVersion    int          `json:"schema_version"`
	RecoveryInput    bool         `json:"recovery_input"`
	Signed           bool         `json:"signed"`
	SessionID        int64        `json:"session_id"`
	TurnID           int64        `json:"turn_id"`
	ReviewRevisionID int64        `json:"review_revision_id"`
	LeaseGeneration  int          `json:"lease_generation"`
	ClaimedRevision  int          `json:"claimed_revision"`
	PolicyRevisionID int64        `json:"policy_revision_id,omitempty"`
	PolicyRevision   string       `json:"policy_revision,omitempty"`
	Repo             string       `json:"repo"`
	Item             int          `json:"item"`
	ItemKind         string       `json:"item_kind"`
	SourceGitPin     string       `json:"source_git_pin"`
	GuestName        string       `json:"guest_name,omitempty"`
	GuestPin         string       `json:"guest_pin,omitempty"`
	GuestSessionID   string       `json:"guest_session_id,omitempty"`
	Verdict          string       `json:"verdict"`
	EvidenceClass    string       `json:"evidence_class"`
	Usage            *Usage       `json:"usage,omitempty"`
	GitHubAction     GitHubAction `json:"github_action"`
	Actions          []Action     `json:"actions"`
}

// Usage is reported token counts when the stored artifact includes them.
type Usage struct {
	InputTokens  *int `json:"input_tokens,omitempty"`
	OutputTokens *int `json:"output_tokens,omitempty"`
}

// GitHubAction is whether the stored review produced a GitHub write.
type GitHubAction struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Action is one stored action row bound to the exported review revision.
type Action struct {
	ID            string `json:"action_id"`
	Type          string `json:"type"`
	ReasonCode    string `json:"reason_code"`
	EvidenceClass string `json:"evidence_class"`
	LimitSentence string `json:"limit_sentence"`
	Body          string `json:"body,omitempty"`
}

type artifact struct {
	SchemaVersion   int              `json:"schema_version"`
	Repo            string           `json:"repo"`
	Item            int              `json:"item"`
	ItemKind        string           `json:"item_kind"`
	ClaimedRevision int              `json:"claimed_revision"`
	SnapshotHash    string           `json:"snapshot_hash"`
	MainSHA         string           `json:"main_sha"`
	HeadSHA         string           `json:"head_sha"`
	Verdict         string           `json:"verdict"`
	ProposedActions []proposedAction `json:"proposed_actions"`
	InputTokens     *int             `json:"input_tokens,omitempty"`
	OutputTokens    *int             `json:"output_tokens,omitempty"`
	GuestSessionID  string           `json:"guest_session_id,omitempty"`
}

type proposedAction struct {
	Type     string     `json:"type"`
	Evidence []evidence `json:"evidence"`
}

type evidence struct {
	Class string `json:"class"`
}

// FromStore builds an export from the latest immutable review of sessionID.
// secrets are redacted from every exported string. The session prompt and
// guest config are never copied.
func FromStore(s *store.Store, sessionID int64, secrets []string) (Export, error) {
	sess, err := store.GetSession(s, sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Export{}, ErrNoReview
		}
		return Export{}, err
	}
	rev, ok, err := store.LatestReviewRecord(s, sessionID)
	if err != nil {
		return Export{}, err
	}
	if !ok {
		return Export{}, ErrNoReview
	}
	turn, err := store.GetTurn(s, rev.JobID)
	if err != nil {
		return Export{}, err
	}
	var art artifact
	if err := json.Unmarshal([]byte(rev.Payload), &art); err != nil {
		return Export{}, fmt.Errorf("stored review: %w", err)
	}
	actions, err := store.ListActionsForSession(s, sessionID)
	if err != nil {
		return Export{}, err
	}
	exp := Export{
		SchemaVersion:    SchemaVersion,
		RecoveryInput:    false,
		Signed:           false,
		SessionID:        sess.ID,
		TurnID:           turn.ID,
		ReviewRevisionID: rev.ID,
		LeaseGeneration:  turn.LeaseGeneration,
		ClaimedRevision:  rev.ClaimedRevision,
		Repo:             sess.Repo,
		Item:             sess.Item,
		ItemKind:         sess.ItemKind,
		GuestName:        sess.GuestName,
		GuestPin:         sess.GuestPin,
		GuestSessionID:   firstNonEmpty(art.GuestSessionID, sess.GuestSessionID),
		Verdict:          art.Verdict,
		Actions:          boundActions(actions, rev.ID),
	}
	if polID, hash, ok, err := store.LatestPolicyRevision(s); err != nil {
		return Export{}, err
	} else if ok {
		exp.PolicyRevisionID = polID
		exp.PolicyRevision = hash
	}
	exp.SourceGitPin = sourceGitPin(s, sess, rev, art)
	exp.EvidenceClass = evidenceClass(art, exp.Actions)
	exp.GitHubAction = githubAction(exp.Actions)
	if art.InputTokens != nil || art.OutputTokens != nil {
		exp.Usage = &Usage{InputTokens: art.InputTokens, OutputTokens: art.OutputTokens}
	}
	return redactExport(exp, secrets), nil
}

func sourceGitPin(s *store.Store, sess *store.Session, rev store.ReviewRevision, art artifact) string {
	it, err := store.LoadSnapshot(s, sess.Repo, sess.Item, rev.ClaimedRevision)
	if err == nil {
		if pin := it.GitPin(); pin != "" {
			return pin
		}
	}
	if art.HeadSHA != "" {
		return art.HeadSHA
	}
	if art.MainSHA != "" {
		return art.MainSHA
	}
	return rev.MainSHA
}

func boundActions(actions []store.Action, revisionID int64) []Action {
	out := make([]Action, 0)
	for _, a := range actions {
		if a.ReviewRevisionID == nil || *a.ReviewRevisionID != revisionID {
			continue
		}
		out = append(out, Action{
			ID: a.ID, Type: a.Type, ReasonCode: a.ReasonCode,
			EvidenceClass: a.EvidenceClass, LimitSentence: a.LimitSentence, Body: a.Body,
		})
	}
	return out
}

func evidenceClass(art artifact, actions []Action) string {
	class := ""
	for _, a := range actions {
		class = strongerEvidence(class, a.EvidenceClass)
	}
	for _, a := range art.ProposedActions {
		for _, e := range a.Evidence {
			class = strongerEvidence(class, e.Class)
		}
	}
	if class == "" {
		return "model_assertion"
	}
	return class
}

func strongerEvidence(cur, next string) string {
	rank := func(c string) int {
		switch c {
		case "server_verified":
			return 3
		case "plane_observed":
			return 2
		case "model_assertion":
			return 1
		default:
			return 0
		}
	}
	if rank(next) > rank(cur) {
		return next
	}
	return cur
}

func githubAction(actions []Action) GitHubAction {
	applied, dryRun := false, false
	for _, a := range actions {
		switch a.Type {
		case "write.apply":
			applied = true
		case "comment", "close":
			dryRun = true
		}
	}
	switch {
	case applied:
		return GitHubAction{
			Status: "applied",
			Detail: "An operator-applied write Action was recorded. This export does not authorize further writes.",
		}
	case dryRun:
		return GitHubAction{
			Status: "dry-run",
			Detail: "Dry-run only. rusui did not comment, close, merge, or land on GitHub.",
		}
	default:
		return GitHubAction{
			Status: "none",
			Detail: "No GitHub action occurred.",
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func redactExport(exp Export, secrets []string) Export {
	exp.PolicyRevision = redact(exp.PolicyRevision, secrets)
	exp.Repo = redact(exp.Repo, secrets)
	exp.SourceGitPin = redact(exp.SourceGitPin, secrets)
	exp.GuestName = redact(exp.GuestName, secrets)
	exp.GuestPin = redact(exp.GuestPin, secrets)
	exp.GuestSessionID = redact(exp.GuestSessionID, secrets)
	exp.Verdict = redact(exp.Verdict, secrets)
	exp.EvidenceClass = redact(exp.EvidenceClass, secrets)
	exp.GitHubAction.Status = redact(exp.GitHubAction.Status, secrets)
	exp.GitHubAction.Detail = redact(exp.GitHubAction.Detail, secrets)
	for i := range exp.Actions {
		exp.Actions[i].ID = redact(exp.Actions[i].ID, secrets)
		exp.Actions[i].Type = redact(exp.Actions[i].Type, secrets)
		exp.Actions[i].ReasonCode = redact(exp.Actions[i].ReasonCode, secrets)
		exp.Actions[i].EvidenceClass = redact(exp.Actions[i].EvidenceClass, secrets)
		exp.Actions[i].LimitSentence = redact(exp.Actions[i].LimitSentence, secrets)
		exp.Actions[i].Body = redact(exp.Actions[i].Body, secrets)
	}
	return exp
}

func redact(s string, secrets []string) string {
	s = ops.Redact(s, secrets)
	for _, secret := range secrets {
		if len(secret) >= minSecretLen {
			s = strings.ReplaceAll(s, secret, redacted)
		}
	}
	s = tokenShapes.ReplaceAllString(s, redacted)
	return authHeader.ReplaceAllString(s, "${1}"+redacted)
}

// JSON returns the indented portable receipt.
func (e Export) JSON() ([]byte, error) {
	return json.MarshalIndent(e, "", "  ")
}

// Markdown returns a pasteable summary that states GitHub action status
// and distinguishes server-verified evidence from model assertions.
func (e Export) Markdown() string {
	var b strings.Builder
	b.WriteString("# rusui review receipt\n\n")
	fmt.Fprintf(&b, "- Session: %d\n", e.SessionID)
	fmt.Fprintf(&b, "- Turn: %d\n", e.TurnID)
	fmt.Fprintf(&b, "- Review revision: %d\n", e.ReviewRevisionID)
	fmt.Fprintf(&b, "- Lease generation: %d\n", e.LeaseGeneration)
	fmt.Fprintf(&b, "- Claimed revision: %d\n", e.ClaimedRevision)
	if e.Repo != "" {
		fmt.Fprintf(&b, "- Item: %s#%d (%s)\n", e.Repo, e.Item, e.ItemKind)
	}
	fmt.Fprintf(&b, "- Verdict: %s\n", emptyDash(e.Verdict))
	fmt.Fprintf(&b, "- Evidence: %s\n", evidenceLabel(e.EvidenceClass))
	if e.PolicyRevision != "" {
		fmt.Fprintf(&b, "- Policy revision: %s (id %d)\n", e.PolicyRevision, e.PolicyRevisionID)
	}
	fmt.Fprintf(&b, "- Source git pin: %s\n", emptyDash(e.SourceGitPin))
	if e.GuestName != "" || e.GuestPin != "" || e.GuestSessionID != "" {
		fmt.Fprintf(&b, "- Guest: %s\n", guestLine(e))
	}
	if e.Usage != nil {
		fmt.Fprintf(&b, "- Usage: %s\n", usageLine(e.Usage))
	}
	fmt.Fprintf(&b, "- GitHub action: %s\n", e.GitHubAction.Detail)
	b.WriteString("\n## Evidence classes\n\n")
	b.WriteString("- `server_verified`: recorded by the plane from GitHub or its own observation.\n")
	b.WriteString("- `model_assertion`: claimed by the guest; stored, never treated as proof.\n")
	if len(e.Actions) > 0 {
		b.WriteString("\n## Actions\n\n")
		for _, a := range e.Actions {
			fmt.Fprintf(&b, "- `%s` reason `%s` evidence `%s`. %s\n",
				a.Type, emptyDash(a.ReasonCode), evidenceLabel(a.EvidenceClass), a.LimitSentence)
		}
	}
	b.WriteString("\nThis file is an export regenerated from SQLite. It is not a recovery input and does not authorize an action.\n")
	b.WriteString("Unsigned. Signed portable session bundles are not in the 1.0 contract.\n")
	return b.String()
}

func evidenceLabel(class string) string {
	switch class {
	case "server_verified":
		return "server_verified (plane-recorded)"
	case "model_assertion":
		return "model_assertion (guest-claimed, not proof)"
	case "plane_observed":
		return "plane_observed (plane-recorded)"
	case "":
		return "-"
	default:
		return class
	}
}

func guestLine(e Export) string {
	parts := make([]string, 0, 3)
	if e.GuestName != "" {
		parts = append(parts, e.GuestName)
	}
	if e.GuestPin != "" {
		parts = append(parts, "pin "+e.GuestPin)
	}
	if e.GuestSessionID != "" {
		parts = append(parts, "ACP "+e.GuestSessionID)
	}
	return strings.Join(parts, ", ")
}

func usageLine(u *Usage) string {
	parts := make([]string, 0, 2)
	if u.InputTokens != nil {
		parts = append(parts, fmt.Sprintf("input %d", *u.InputTokens))
	}
	if u.OutputTokens != nil {
		parts = append(parts, fmt.Sprintf("output %d", *u.OutputTokens))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// WriteFiles writes r{id}.json and r{id}.md under dir. The files are
// regenerated exports, not recovery inputs.
func WriteFiles(dir string, exp Export, secrets []string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	exp = redactExport(exp, secrets)
	raw, err := exp.JSON()
	if err != nil {
		return err
	}
	base := fmt.Sprintf("r%d", exp.ReviewRevisionID)
	if err := os.WriteFile(filepath.Join(dir, base+".json"), append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, base+".md"), []byte(exp.Markdown()), 0o600)
}
