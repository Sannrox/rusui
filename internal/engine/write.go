package engine

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

const (
	ActionWritePropose = "write.propose"
	ActionWriteApply   = "write.apply"
)

// ErrWriteNoProposal is returned when the session has no unapplied
// write.propose Action.
var ErrWriteNoProposal = errors.New("write: no proposal")

// ErrWritePublisherOff is returned when the plane has no Publisher.
var ErrWritePublisherOff = errors.New("write: plane publication is off")

// ErrWriteNotImplement is returned when the session is not an implement
// session.
var ErrWriteNotImplement = errors.New("write: not an implement session")

// WriteProposal is the body of a write.propose Action.
type WriteProposal struct {
	Branch       string `json:"branch"`
	Title        string `json:"title"`
	Body         string `json:"body,omitempty"`
	CandidateSHA string `json:"candidate_sha"`
}

// WriteApplyReceipt is returned by ApplySessionWrite and stored as the
// write.apply Action body.
type WriteApplyReceipt struct {
	ActionID    string `json:"action_id"`
	ProposalID  string `json:"proposal_id"`
	PullRequest int    `json:"pull_request,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
	Branch      string `json:"branch,omitempty"`
	SHA         string `json:"sha,omitempty"`
	PushedRef   string `json:"pushed_ref,omitempty"`
}

// ApplySessionWrite performs the GitHub write recorded in the session's
// latest write.propose Action and stores a write.apply receipt. A second
// apply of the same proposal returns the existing receipt.
func (e *Engine) ApplySessionWrite(sessionID int64) (WriteApplyReceipt, error) {
	defer e.lockWrite(sessionID)()
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return WriteApplyReceipt{}, err
	}
	task, ok := e.ImplementTask(sess)
	if !ok {
		return WriteApplyReceipt{}, ErrWriteNotImplement
	}
	if e.Publisher == nil {
		return WriteApplyReceipt{}, ErrWritePublisherOff
	}
	proposal, err := store.LatestAction(e.Store, sessionID, ActionWritePropose)
	if errors.Is(err, sql.ErrNoRows) {
		return WriteApplyReceipt{}, ErrWriteNoProposal
	}
	if err != nil {
		return WriteApplyReceipt{}, err
	}
	if existing, err := store.ActionByReason(e.Store, sessionID, ActionWriteApply, proposal.ID); err == nil {
		var rec WriteApplyReceipt
		if json.Unmarshal([]byte(existing.Body), &rec) == nil {
			rec.ActionID = existing.ID
			return rec, nil
		}
	}
	var ask WriteProposal
	if err := json.Unmarshal([]byte(proposal.Body), &ask); err != nil {
		return WriteApplyReceipt{}, err
	}
	rec, err := e.executePublication(sess, task, ask)
	if err != nil {
		return WriteApplyReceipt{}, err
	}
	rec.ProposalID = proposal.ID
	id, err := newActionID()
	if err != nil {
		return WriteApplyReceipt{}, err
	}
	rec.ActionID = id
	body, err := json.Marshal(rec)
	if err != nil {
		return WriteApplyReceipt{}, err
	}
	a := store.Action{
		ID: id, SessionID: &sessionID, Repo: proposal.Repo, Item: proposal.Item,
		Type: ActionWriteApply, ReasonCode: proposal.ID,
		EvidenceClass: "plane_observed",
		LimitSentence: "The operator applied a session write proposal.",
		Body:          string(body),
	}
	if proposal.TurnID != nil {
		a.TurnID = proposal.TurnID
	}
	if proposal.ReviewRevisionID != nil {
		a.ReviewRevisionID = proposal.ReviewRevisionID
	}
	if err := store.InsertAction(e.Store, a); err != nil {
		e.exception(fmt.Sprintf("write.apply session=%d: wrote %s but failed to record receipt: %v", sessionID, rec.SHA, err))
		return WriteApplyReceipt{}, err
	}
	return rec, nil
}

func (e *Engine) executePublication(sess *store.Session, task *store.Task, ask WriteProposal) (WriteApplyReceipt, error) {
	if err := e.recheckRepair(task, ask); err != nil {
		return WriteApplyReceipt{}, err
	}
	base := strings.TrimPrefix(task.Ref, "refs/heads/")
	if e.projectShip(sess.Project) == policy.ShipPushBase {
		if err := e.Publisher.PushBase(task.Repo, base, ask.CandidateSHA); err != nil {
			return WriteApplyReceipt{}, err
		}
		return WriteApplyReceipt{
			Publisher: PublisherPlane,
			Branch:    ask.Branch,
			SHA:       ask.CandidateSHA,
			PushedRef: "refs/heads/" + base,
		}, nil
	}
	spec := gh.PullSpec{Head: ask.Branch, SHA: ask.CandidateSHA, Base: base, Title: ask.Title, Body: ask.Body}
	if prev, ok, err := SessionPublication(e.Store, sess.ID); err != nil {
		return WriteApplyReceipt{}, err
	} else if ok {
		switch {
		case prev.Publisher != PublisherPlane:
			return WriteApplyReceipt{}, fmt.Errorf("write: pull request #%d was not published by the plane", prev.PullRequest)
		case prev.Branch != ask.Branch:
			return WriteApplyReceipt{}, fmt.Errorf("write: follow-up must publish branch %s", prev.Branch)
		}
		spec.Number = prev.PullRequest
	}
	n, err := e.Publisher.PublishPull(task.Repo, spec)
	if err != nil {
		return WriteApplyReceipt{}, err
	}
	return WriteApplyReceipt{
		PullRequest: n,
		Publisher:   PublisherPlane,
		Branch:      ask.Branch,
		SHA:         ask.CandidateSHA,
	}, nil
}

func (e *Engine) lockWrite(sessionID int64) func() {
	e.writeMu.Lock()
	if e.writing == nil {
		e.writing = map[int64]*completeLock{}
	}
	l, ok := e.writing[sessionID]
	if !ok {
		l = &completeLock{}
		e.writing[sessionID] = l
	}
	l.refs++
	e.writeMu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		e.writeMu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(e.writing, sessionID)
		}
		e.writeMu.Unlock()
	}
}
