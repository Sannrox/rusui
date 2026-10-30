package gh

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ActionMarker is the hidden identity rusui uses to find its comment on
// an item. One marker means one comment.
func ActionMarker(actionID string) string {
	return "<!-- rusui-action:" + actionID + " -->"
}

// CommentSpec is one issue comment the plane publishes from a durable
// result. Body must already include ActionMarker(ActionID).
type CommentSpec struct {
	ActionID string
	Body     string
	ID       int64 // update this comment when > 0
}

// CommentReceipt is the GitHub comment the plane observed after a write
// or a lookup of an existing marker.
type CommentReceipt struct {
	ID   int64
	Body string
}

// CommentPublisher posts or updates one issue comment. The GitHub Client
// stays read-only.
type CommentPublisher interface {
	PublishComment(repo string, item int, spec CommentSpec) (CommentReceipt, error)
}

// ErrCommentUncertain means the write may have happened. Reconcile by
// marker instead of posting again.
var ErrCommentUncertain = errors.New("github: comment write uncertain")

// ErrCommentDenied is a definite refusal (auth, permission, or bad spec).
var ErrCommentDenied = errors.New("github: comment write denied")

type issueComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

// PublishComment creates or updates one comment identified by action
// marker. A lost create response becomes an update after listing. An
// unchanged GitHub body is left alone.
func (p *Publisher) PublishComment(repo string, item int, spec CommentSpec) (CommentReceipt, error) {
	if spec.ActionID == "" || !strings.Contains(spec.Body, ActionMarker(spec.ActionID)) {
		return CommentReceipt{}, fmt.Errorf("%w: missing action marker", ErrCommentDenied)
	}
	owner, name, err := splitRepo(repo)
	if err != nil {
		return CommentReceipt{}, err
	}
	tok, err := p.Tokens.CommentToken(repo)
	if err != nil {
		return CommentReceipt{}, fmt.Errorf("%w: %w", ErrCommentDenied, err)
	}
	base := "/repos/" + owner + "/" + name
	found, err := p.commentFor(tok, base, item, spec.ActionID)
	if err != nil {
		return CommentReceipt{}, uncertainOr(err)
	}
	if found.ID == 0 && spec.ID > 0 {
		found.ID = spec.ID
		found.Body = ""
	}
	if found.ID > 0 {
		if found.Body == spec.Body {
			return found, nil
		}
		rec, err := p.updateComment(tok, base, found.ID, spec)
		if errors.Is(err, errCommentDeleted) {
			return p.createComment(tok, base, item, spec)
		}
		return rec, err
	}
	return p.createComment(tok, base, item, spec)
}

func (p *Publisher) commentFor(tok, repoPath string, item int, actionID string) (CommentReceipt, error) {
	marker := ActionMarker(actionID)
	path := repoPath + "/issues/" + strconv.Itoa(item) + "/comments?per_page=100"
	for page := 1; page <= 10; page++ {
		var list []issueComment
		q := path + "&page=" + strconv.Itoa(page)
		if page == 1 {
			q = path
		}
		if err := p.do(tok, http.MethodGet, q, nil, &list); err != nil {
			return CommentReceipt{}, err
		}
		for _, c := range list {
			if strings.Contains(c.Body, marker) {
				return CommentReceipt(c), nil
			}
		}
		if len(list) < 100 {
			return CommentReceipt{}, nil
		}
	}
	return CommentReceipt{}, nil
}

func (p *Publisher) createComment(tok, repoPath string, item int, spec CommentSpec) (CommentReceipt, error) {
	var out issueComment
	err := p.do(tok, http.MethodPost, repoPath+"/issues/"+strconv.Itoa(item)+"/comments", map[string]string{"body": spec.Body}, &out)
	if err != nil {
		if found, lookErr := p.commentFor(tok, repoPath, item, spec.ActionID); lookErr == nil && found.ID > 0 {
			if found.Body == spec.Body {
				return found, nil
			}
			return p.updateComment(tok, repoPath, found.ID, spec)
		}
		return CommentReceipt{}, uncertainOr(err)
	}
	if out.ID <= 0 {
		return CommentReceipt{}, ErrCommentUncertain
	}
	return CommentReceipt{ID: out.ID, Body: spec.Body}, nil
}

func (p *Publisher) updateComment(tok, repoPath string, id int64, spec CommentSpec) (CommentReceipt, error) {
	var out issueComment
	err := p.do(tok, http.MethodPatch, repoPath+"/issues/comments/"+strconv.FormatInt(id, 10), map[string]string{"body": spec.Body}, &out)
	var se *statusError
	if errors.As(err, &se) && se.code == http.StatusNotFound {
		return CommentReceipt{}, errCommentDeleted
	}
	if err != nil {
		return CommentReceipt{}, uncertainOr(err)
	}
	return CommentReceipt{ID: id, Body: spec.Body}, nil
}

var errCommentDeleted = errors.New("github: comment deleted")

func uncertainOr(err error) error {
	var se *statusError
	if errors.As(err, &se) && se.code >= 400 && se.code < 500 && se.code != http.StatusNotFound && se.code != http.StatusTooManyRequests {
		return fmt.Errorf("%w: %w", ErrCommentDenied, err)
	}
	return fmt.Errorf("%w: %w", ErrCommentUncertain, err)
}
