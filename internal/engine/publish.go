package engine

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

// PullPublisher creates or updates one pull request as the plane's GitHub
// App (ADR 0020, ADR 0044, ADR 0080). A nil Engine.Publisher refuses apply.
type PullPublisher interface {
	PublishPull(repo string, s gh.PullSpec) (int, error)
	// PushBase fast-forwards the repository default branch to sha.
	// It does not open a pull request (ADR 0068).
	PushBase(repo, branch, sha string) error
}

// PublishRequest is the agent's ask, in its turn result, that the plane
// publish its pushed session branch.
type PublishRequest struct {
	Branch string `json:"branch"`
	Title  string `json:"title"`
	Body   string `json:"body,omitempty"`
}

// PublisherPlane marks a result whose pull request the plane wrote.
const PublisherPlane = "plane"

// ImplementTask returns the open implementation task when sess is an
// implement session: a run session for an open task on a bound repository
// with implement enabled, in a project that admits run. Only these
// sessions may publish, and the permission fence applies to them.
func (e *Engine) ImplementTask(sess *store.Session) (*store.Task, bool) {
	if sess == nil || sess.Kind != store.SessionKindRun {
		return nil, false
	}
	pol := e.PolicySnapshot()
	rr, ok := pol.Repo(sess.Repo)
	if !ok || !rr.Implement {
		return nil, false
	}
	if p, ok := pol.Project(sess.Project); !ok || !p.AllowsKind(policy.KindRun) {
		return nil, false
	}
	task, err := store.OpenTaskForSession(e.Store, sess.ID)
	if err != nil || task == nil || task.Repo != sess.Repo {
		return nil, false
	}
	return task, true
}

// SessionRefAllowed reports whether ref (refs/heads/...) is under the
// session's own prefix, the only refs a session may push or publish
// unless the project ship is push-base.
func SessionRefAllowed(sessionID int64, ref string) bool {
	prefix := fmt.Sprintf("refs/heads/rusui/%d", sessionID)
	return ref == prefix || strings.HasPrefix(ref, prefix+"/")
}

// PushRefAllowed reports whether the session may receive-pack ref.
// Guests may push only their session branches; default-branch writes
// go through ApplySessionWrite (#607).
func (e *Engine) PushRefAllowed(sessionID int64, ref string) bool {
	return SessionRefAllowed(sessionID, ref)
}

// projectShip is the project's ship behavior, default pull-request.
func (e *Engine) projectShip(project string) string {
	if p, ok := e.PolicySnapshot().Project(project); ok && p.Ship != "" {
		return p.Ship
	}
	return policy.ShipPullRequest
}

// publishResult validates a run result that asks to write. It does not
// call GitHub. Complete records write.propose; ApplySessionWrite writes.
func (e *Engine) publishResult(jobID int64, gen, claimed int, art *Artifact) {
	r := art.Result
	if art.ItemKind != "run" || r == nil {
		return
	}
	r.Publisher = ""
	block := func(reason string) {
		r.PullRequest = 0
		r.BlockedReason = "publication refused: " + reason
	}
	if r.Publish == nil {
		if e.Publisher != nil && r.PullRequest > 0 {
			block("only the plane opens pull requests")
		}
		return
	}
	req := r.Publish
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
		// Complete rejects the stale lease too; nothing is written.
		return
	}
	var sess *store.Session
	if err == nil {
		sess, err = store.GetSession(e.Store, sessionID)
	}
	if err != nil {
		block("publication state unavailable")
		return
	}
	task, ok := e.ImplementTask(sess)
	if e.Publisher == nil {
		block("plane publication is off")
		return
	}
	switch {
	case !ok:
		block("not an implement session")
		return
	case task.Repo != art.Repo:
		block("repository mismatch")
		return
	case !SessionRefAllowed(sess.ID, "refs/heads/"+req.Branch):
		block(fmt.Sprintf("branch must be under rusui/%d/", sess.ID))
		return
	case strings.TrimSpace(req.Title) == "":
		block("title required")
		return
	case r.CandidateSHA == "":
		block("candidate commit required")
		return
	}
	if prev, ok := priorPublishAsk(e.Store, sessionID); ok && prev != req.Branch {
		block(fmt.Sprintf("follow-up must publish branch %s", prev))
		return
	}
	// The turn records a proposal. GitHub writes happen only when the
	// operator applies the write Action (#607).
	r.PullRequest = 0
	r.Publisher = ""
}
