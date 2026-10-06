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
// App (ADR 0020, ADR 0044). A nil Engine.Publisher keeps ADR 0015 agent
// publication.
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
// Session branches are always allowed. The default branch is allowed
// only for an implement session whose project ship is push-base.
func (e *Engine) PushRefAllowed(sessionID int64, ref string) bool {
	if SessionRefAllowed(sessionID, ref) {
		return true
	}
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return false
	}
	task, ok := e.ImplementTask(sess)
	if !ok {
		return false
	}
	p, ok := e.PolicySnapshot().Project(sess.Project)
	if !ok || p.Ship != policy.ShipPushBase {
		return false
	}
	base := strings.TrimPrefix(task.Ref, "refs/heads/")
	if base == "" {
		return false
	}
	return ref == "refs/heads/"+base
}

// projectShip is the project's ship behavior, default pull-request.
func (e *Engine) projectShip(project string) string {
	if p, ok := e.PolicySnapshot().Project(project); ok && p.Ship != "" {
		return p.Ship
	}
	return policy.ShipPullRequest
}

// publishResult performs the plane's one GitHub write for a run result
// that asks for publication. It runs before the result transaction, so it
// checks the lease itself first; Complete checks it again when recording.
// Under plane publication only the plane sets the pull request number. A
// refusal or failed write becomes a blocked result; there is no fallback
// credential.
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
			// The guest cannot name a pull request the plane did not write.
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
		if !ok || e.projectShip(sess.Project) != policy.ShipPushBase {
			block("plane publication is off")
			return
		}
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
	base := strings.TrimPrefix(task.Ref, "refs/heads/")
	if e.projectShip(sess.Project) == policy.ShipPushBase {
		if e.Publisher == nil {
			// Agent publication: the guest pushed the default branch.
			r.PullRequest = 0
			return
		}
		if err := e.Publisher.PushBase(task.Repo, base, r.CandidateSHA); err != nil {
			if errors.Is(err, gh.ErrHeadMismatch) {
				block("branch head is not the candidate commit")
				return
			}
			e.exception(fmt.Sprintf("publish job=%d: %v", jobID, err))
			block("github write failed")
			return
		}
		r.PullRequest = 0
		r.Publisher = PublisherPlane
		r.PushedRef = "refs/heads/" + base
		return
	}
	if e.Publisher == nil {
		block("plane publication is off")
		return
	}
	// The publisher writes only while the branch points at the candidate,
	// so a pull request never goes live on a commit the turn did not report.
	spec := gh.PullSpec{Head: req.Branch, SHA: r.CandidateSHA, Base: base, Title: req.Title, Body: req.Body}
	if prev, ok := priorPublication(e.Store, jobID); ok {
		switch {
		case prev.Publisher != PublisherPlane:
			block(fmt.Sprintf("pull request #%d was not published by the plane", prev.PullRequest))
			return
		case prev.Branch != req.Branch:
			block(fmt.Sprintf("follow-up must publish branch %s", prev.Branch))
			return
		}
		spec.Number = prev.PullRequest
	}
	n, err := e.Publisher.PublishPull(task.Repo, spec)
	if errors.Is(err, gh.ErrHeadMismatch) {
		block("branch head is not the candidate commit")
		return
	}
	if err != nil {
		e.exception(fmt.Sprintf("publish job=%d: %v", jobID, err))
		block("github write failed")
		return
	}
	r.PullRequest = n
	r.Publisher = PublisherPlane
}
