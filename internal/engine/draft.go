package engine

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/sannrox/rusui/internal/core"
	"github.com/sannrox/rusui/internal/store"
)

const (
	ActionCoreDraft      = "core.draft"
	ActionCoreDraftApply = "core.draft.apply"
	maxSourceTurnBytes   = 256
)

// ErrDraftNoProposal is returned when the session has no unapplied core.draft.
var ErrDraftNoProposal = errors.New("draft: no proposal")

// ErrDraftCoreOff is returned when the plane has no core client.
var ErrDraftCoreOff = errors.New("draft: core client is off")

// DraftRequest is the agent's ask, in its turn result, that the plane
// draft definition members through scoped authoring.
type DraftRequest struct {
	Namespace            string        `json:"namespace"`
	BranchID             string        `json:"branch_id"`
	ParentRevisionDigest string        `json:"parent_revision_digest"`
	Members              []DraftMember `json:"members"`
}

// DraftMember is one object type or link type to upsert on the branch.
type DraftMember struct {
	MemberKind     string `json:"member_kind"`
	MemberID       string `json:"member_id"`
	DefinitionJSON string `json:"definition_json"`
}

// DraftApplyReceipt is returned by ApplySessionDraft and stored as
// core.draft.apply. A PermissionDenied is a refused receipt, not a lost write.
type DraftApplyReceipt struct {
	ActionID           string           `json:"action_id"`
	ProposalID         string           `json:"proposal_id"`
	Namespace          string           `json:"namespace,omitempty"`
	BranchID           string           `json:"branch_id,omitempty"`
	ExpectedHeadDigest string           `json:"expected_head_digest,omitempty"`
	HeadDigest         string           `json:"head_digest,omitempty"`
	CoreProposalID     string           `json:"core_proposal_id,omitempty"`
	ProposalDigest     string           `json:"proposal_digest,omitempty"`
	Lint               *core.LintReport `json:"lint,omitempty"`
	Refused            bool             `json:"refused,omitempty"`
	Code               string           `json:"code,omitempty"`
	Message            string           `json:"message,omitempty"`
}

func draftApplyDone(rec DraftApplyReceipt) bool {
	return rec.Refused || rec.CoreProposalID != ""
}

func draftHeadPinID(proposalID string) string {
	return "draftexp" + proposalID
}

// draftResult validates a run result that asks to draft. It does not call
// the core. Complete records core.draft; ApplySessionDraft writes.
func (e *Engine) draftResult(jobID int64, gen, claimed int, art *Artifact) {
	r := art.Result
	if art.ItemKind != "run" || r == nil || r.Draft == nil {
		return
	}
	block := func(reason string) {
		r.Draft = nil
		if r.BlockedReason == "" {
			r.BlockedReason = "draft refused: " + reason
		}
	}
	if r.Publish != nil {
		block("cannot share a turn with a GitHub publish")
		return
	}
	var sessionID int64
	err := e.Store.Tx(func(tx *sql.Tx) error {
		j, err := store.GetJobByIDTx(tx, jobID)
		if err != nil {
			return err
		}
		if err := e.acceptLease(tx, j, gen, claimed); err != nil {
			return err
		}
		turn, err := store.GetTurnTx(tx, jobID)
		if err != nil {
			return err
		}
		sessionID = turn.SessionID
		return nil
	})
	if errors.Is(err, errReject) {
		return
	}
	if err != nil {
		block("draft state unavailable")
		return
	}
	if r.Draft.BranchID == "" {
		r.Draft.BranchID = fmt.Sprintf("interview-%d", sessionID)
	}
	if reason := validateDraftRequest(*r.Draft); reason != "" {
		block(reason)
	}
}

func validateDraftRequest(d DraftRequest) string {
	if strings.TrimSpace(d.Namespace) == "" {
		return "namespace required"
	}
	if strings.TrimSpace(d.BranchID) == "" {
		return "branch_id required"
	}
	if strings.TrimSpace(d.ParentRevisionDigest) == "" {
		return "parent_revision_digest required"
	}
	if len(d.Members) == 0 {
		return "members required"
	}
	var types, links int
	for _, m := range d.Members {
		if m.MemberKind == "" || m.MemberID == "" {
			return "member_kind and member_id required"
		}
		if m.MemberKind == "agent" {
			return "agent members cannot carry source_turn"
		}
		if err := validateDraftMemberJSON(m); err != nil {
			return err.Error()
		}
		switch m.MemberKind {
		case "object_type":
			types++
		case "link_type":
			links++
		}
	}
	if types == 0 || links == 0 {
		return "draft needs object_type and link_type members"
	}
	return ""
}

func validateDraftMemberJSON(m DraftMember) error {
	if m.DefinitionJSON == "" {
		return fmt.Errorf("definition_json required")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(m.DefinitionJSON), &obj); err != nil {
		return fmt.Errorf("definition_json must be a JSON object")
	}
	raw, ok := obj["source_turn"]
	if !ok {
		return fmt.Errorf("source_turn required")
	}
	turn, ok := raw.(string)
	if !ok || turn == "" || len(turn) > maxSourceTurnBytes {
		return fmt.Errorf("source_turn must be a non-empty string of at most %d bytes without control characters or surrounding whitespace", maxSourceTurnBytes)
	}
	if strings.TrimSpace(turn) != turn {
		return fmt.Errorf("source_turn must be a non-empty string of at most %d bytes without control characters or surrounding whitespace", maxSourceTurnBytes)
	}
	for _, r := range turn {
		if unicode.IsControl(r) {
			return fmt.Errorf("source_turn must be a non-empty string of at most %d bytes without control characters or surrounding whitespace", maxSourceTurnBytes)
		}
	}
	return nil
}

// ApplySessionDraft performs the core draft recorded in the session's
// latest core.draft Action and stores a core.draft.apply receipt. A second
// apply of the same proposal returns the existing receipt, including a
// recorded PermissionDenied.
func (e *Engine) ApplySessionDraft(sessionID int64) (DraftApplyReceipt, error) {
	defer e.lockWrite(sessionID)()
	if e.Core == nil {
		return DraftApplyReceipt{}, ErrDraftCoreOff
	}
	if _, err := store.GetSession(e.Store, sessionID); err != nil {
		return DraftApplyReceipt{}, err
	}
	proposal, err := store.LatestAction(e.Store, sessionID, ActionCoreDraft)
	if errors.Is(err, sql.ErrNoRows) {
		return DraftApplyReceipt{}, ErrDraftNoProposal
	}
	if err != nil {
		return DraftApplyReceipt{}, err
	}
	if existing, err := store.ActionByReason(e.Store, sessionID, ActionCoreDraftApply, proposal.ID); err == nil {
		var rec DraftApplyReceipt
		if json.Unmarshal([]byte(existing.Body), &rec) == nil && draftApplyDone(rec) {
			rec.ActionID = existing.ID
			return rec, nil
		}
	}
	var ask DraftRequest
	if err := json.Unmarshal([]byte(proposal.Body), &ask); err != nil {
		return DraftApplyReceipt{}, err
	}
	rec, err := e.executeDraft(sessionID, proposal, ask)
	if err != nil && !core.IsPermissionDenied(err) {
		return DraftApplyReceipt{}, err
	}
	if core.IsPermissionDenied(err) {
		rec = DraftApplyReceipt{
			ProposalID: proposal.ID,
			Namespace:  ask.Namespace,
			BranchID:   ask.BranchID,
			Refused:    true,
			Code:       "permission_denied",
			Message:    err.Error(),
		}
	}
	rec.ProposalID = proposal.ID
	id, err := newActionID()
	if err != nil {
		return DraftApplyReceipt{}, err
	}
	rec.ActionID = id
	body, err := json.Marshal(rec)
	if err != nil {
		return DraftApplyReceipt{}, err
	}
	a := store.Action{
		ID: id, SessionID: &sessionID, Repo: proposal.Repo, Item: proposal.Item,
		Type: ActionCoreDraftApply, ReasonCode: proposal.ID,
		EvidenceClass: "plane_observed",
		LimitSentence: "The operator applied a session definition draft; publishing remains a core approval.",
		Body:          string(body),
	}
	if proposal.TurnID != nil {
		a.TurnID = proposal.TurnID
	}
	if proposal.ReviewRevisionID != nil {
		a.ReviewRevisionID = proposal.ReviewRevisionID
	}
	if err := store.InsertAction(e.Store, a); err != nil {
		e.exception(fmt.Sprintf("core.draft.apply session=%d: wrote %s but failed to record receipt: %v", sessionID, rec.HeadDigest, err))
		return DraftApplyReceipt{}, err
	}
	return rec, nil
}

func (e *Engine) executeDraft(sessionID int64, proposal store.Action, ask DraftRequest) (DraftApplyReceipt, error) {
	branch, err := e.resolveDraftBranch(ask, proposal.ID)
	if err != nil {
		return DraftApplyReceipt{}, err
	}
	expected, err := e.pinDraftExpectedHead(sessionID, proposal, ask, branch.HeadRevisionDigest)
	if err != nil {
		return DraftApplyReceipt{}, err
	}
	upserts := make([]core.MemberInput, 0, len(ask.Members))
	for _, m := range ask.Members {
		upserts = append(upserts, core.MemberInput{MemberKind: m.MemberKind, MemberID: m.MemberID, DefinitionJSON: m.DefinitionJSON})
	}
	req := core.ApplyEditRequest{
		Namespace:          ask.Namespace,
		BranchID:           ask.BranchID,
		ExpectedHeadDigest: expected,
		Upserts:            upserts,
		IdempotencyKey:     "rusui-draft-edit-" + proposal.ID,
	}
	edit, err := e.Core.ApplyDefinitionBranchEdit(req)
	if core.IsAlreadyExists(err) {
		// Same key and pinned expected head replays this proposal's
		// committed edit. A different expected head fails closed.
		edit, err = e.Core.ApplyDefinitionBranchEdit(req)
	}
	if err != nil {
		return DraftApplyReceipt{}, err
	}
	lint, err := e.Core.GetOntologyLintReport(ask.Namespace, ask.BranchID)
	if err != nil {
		return DraftApplyReceipt{}, err
	}
	if lint.RevisionDigest != edit.Branch.HeadRevisionDigest {
		return DraftApplyReceipt{}, fmt.Errorf("draft: lint revision %s does not match candidate %s", lint.RevisionDigest, edit.Branch.HeadRevisionDigest)
	}
	coreProposalID := "rusui-" + proposal.ID
	prop, err := e.Core.CreateDefinitionProposal(core.CreateProposalRequest{
		Namespace:       ask.Namespace,
		BranchID:        ask.BranchID,
		ProposalID:      coreProposalID,
		BaseDigest:      ask.ParentRevisionDigest,
		CandidateDigest: edit.Branch.HeadRevisionDigest,
		IdempotencyKey:  "rusui-draft-propose-" + proposal.ID,
	})
	if err != nil {
		return DraftApplyReceipt{}, err
	}
	return DraftApplyReceipt{
		Namespace:          ask.Namespace,
		BranchID:           ask.BranchID,
		ExpectedHeadDigest: expected,
		HeadDigest:         edit.Branch.HeadRevisionDigest,
		CoreProposalID:     prop.ProposalID,
		ProposalDigest:     prop.ProposalDigest,
		Lint:               &lint,
	}, nil
}

// pinDraftExpectedHead records the CAS head this proposal will edit.
// The action id is the fence: the first persisted head wins across plane instances.
func (e *Engine) pinDraftExpectedHead(sessionID int64, proposal store.Action, ask DraftRequest, head string) (string, error) {
	id := draftHeadPinID(proposal.ID)
	if existing, err := store.GetAction(e.Store, id); err == nil {
		var rec DraftApplyReceipt
		if json.Unmarshal([]byte(existing.Body), &rec) == nil && rec.ExpectedHeadDigest != "" {
			return rec.ExpectedHeadDigest, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	rec := DraftApplyReceipt{
		ActionID: id, ProposalID: proposal.ID,
		Namespace: ask.Namespace, BranchID: ask.BranchID,
		ExpectedHeadDigest: head,
	}
	body, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	a := store.Action{
		ID: id, SessionID: &sessionID, Repo: proposal.Repo, Item: proposal.Item,
		Type: ActionCoreDraftApply, ReasonCode: proposal.ID,
		EvidenceClass: "plane_observed",
		LimitSentence: "The plane pinned the expected definition-branch head for this draft.",
		Body:          string(body),
	}
	if proposal.TurnID != nil {
		a.TurnID = proposal.TurnID
	}
	if proposal.ReviewRevisionID != nil {
		a.ReviewRevisionID = proposal.ReviewRevisionID
	}
	if err := store.InsertAction(e.Store, a); err != nil {
		if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return "", err
		}
		existing, getErr := store.GetAction(e.Store, id)
		if getErr != nil {
			return "", err
		}
		var pinned DraftApplyReceipt
		if json.Unmarshal([]byte(existing.Body), &pinned) != nil || pinned.ExpectedHeadDigest == "" {
			return "", err
		}
		return pinned.ExpectedHeadDigest, nil
	}
	return head, nil
}

func (e *Engine) resolveDraftBranch(ask DraftRequest, proposalID string) (core.DefinitionBranch, error) {
	branch, err := e.Core.GetDefinitionBranch(ask.Namespace, ask.BranchID)
	if err == nil {
		return branch, nil
	}
	if !core.IsNotFound(err) {
		return core.DefinitionBranch{}, err
	}
	branch, err = e.Core.CreateDefinitionBranch(core.CreateBranchRequest{
		Namespace:            ask.Namespace,
		BranchID:             ask.BranchID,
		ParentRevisionDigest: ask.ParentRevisionDigest,
		IdempotencyKey:       "rusui-draft-create-" + proposalID,
	})
	if err == nil {
		return branch, nil
	}
	if core.IsAlreadyExists(err) {
		return e.Core.GetDefinitionBranch(ask.Namespace, ask.BranchID)
	}
	return core.DefinitionBranch{}, err
}

// SessionDraft is the latest completed draft apply receipt, including a refusal.
func SessionDraft(s *store.Store, sessionID int64) (DraftApplyReceipt, bool, error) {
	rows, err := s.DB.Query(`SELECT action_id, body FROM actions WHERE session_id=? AND action_type=? ORDER BY rowid DESC`, sessionID, ActionCoreDraftApply)
	if err != nil {
		return DraftApplyReceipt{}, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, body string
		if err := rows.Scan(&id, &body); err != nil {
			return DraftApplyReceipt{}, false, err
		}
		var rec DraftApplyReceipt
		if json.Unmarshal([]byte(body), &rec) != nil || !draftApplyDone(rec) {
			continue
		}
		rec.ActionID = id
		return rec, true, nil
	}
	return DraftApplyReceipt{}, false, rows.Err()
}
