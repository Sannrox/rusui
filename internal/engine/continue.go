package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/sannrox/rusui/internal/publish"
	"github.com/sannrox/rusui/internal/store"
)

var (
	ErrNoEffort      = fmt.Errorf("effort not found")
	ErrEffortClosed  = fmt.Errorf("effort closed")
	ErrOutOfOrder    = fmt.Errorf("out of order feedback")
	ErrRefNotOwned   = fmt.Errorf("ref not owned")
	ErrFeedbackClash = fmt.Errorf("duplicate seq with different prompt")
)

type FeedbackInput struct {
	EffortKey    string
	Seq          int
	CandidateSHA string
	Prompt       string
}

type FeedbackResult struct {
	ID      int64
	Applied bool
}

func promptHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (e *Engine) ApplyFeedback(in FeedbackInput) (*FeedbackResult, error) {
	if in.EffortKey == "" || in.Seq < 1 || in.Prompt == "" || in.CandidateSHA == "" {
		return nil, fmt.Errorf("invalid feedback")
	}
	task, err := store.GetLatestTask(e.Store, in.EffortKey)
	if err != nil || task == nil {
		return nil, ErrNoEffort
	}
	if task.State == "abandoned" || task.State == "cancelled" {
		return nil, ErrEffortClosed
	}
	existing, err := store.GetFeedback(e.Store, in.EffortKey, in.Seq)
	if err != nil {
		return nil, err
	}
	h := promptHash(in.Prompt)
	if existing != nil {
		if existing.PromptHash != h {
			return nil, ErrFeedbackClash
		}
		return &FeedbackResult{ID: existing.ID, Applied: false}, nil
	}
	latest, err := store.LatestFeedback(e.Store, in.EffortKey)
	if err != nil {
		return nil, err
	}
	want := 1
	if latest != nil {
		want = latest.Seq + 1
	}
	if in.Seq != want {
		return nil, ErrOutOfOrder
	}
	if latest != nil && latest.CandidateSHA != in.CandidateSHA {
		if err := store.InvalidateProofs(e.Store, latest.CandidateSHA); err != nil {
			return nil, err
		}
	}
	id, err := store.InsertFeedback(e.Store, store.EffortFeedback{
		EffortKey: in.EffortKey, Seq: in.Seq, TaskID: task.ID,
		CandidateSHA: in.CandidateSHA, PromptHash: h, Prompt: in.Prompt,
	})
	if err != nil {
		return nil, err
	}
	return &FeedbackResult{ID: id, Applied: true}, nil
}

func (e *Engine) AbandonEffort(effortKey string) error {
	return e.closeEffort(effortKey, "abandoned")
}

func (e *Engine) CancelEffort(effortKey string) error {
	return e.closeEffort(effortKey, "cancelled")
}

func (e *Engine) closeEffort(effortKey, state string) error {
	task, err := store.GetLatestTask(e.Store, effortKey)
	if err != nil || task == nil {
		return ErrNoEffort
	}
	return store.SetTaskState(e.Store, task.ID, state)
}

func (e *Engine) ContinuePublish(effortKey string, req PublishRequest) (*PublishResult, error) {
	task, err := store.GetLatestTask(e.Store, effortKey)
	if err != nil || task == nil {
		return &PublishResult{State: PubDenied}, ErrNoEffort
	}
	if task.State == "abandoned" || task.State == "cancelled" {
		res := &PublishResult{State: PubDenied, Gate: publish.Denied}
		return res, ErrEffortClosed
	}
	sess, err := store.GetSession(e.Store, task.SessionID)
	if err != nil {
		return nil, err
	}
	if pub, err := store.LatestSucceededPublication(e.Store, sess.Repo, sess.Item); err != nil {
		return nil, err
	} else if pub != nil {
		req.Action = publish.ActionUpdatePR
		if req.Source.Item == 0 {
			req.Source.Item = sess.Item
		}
	} else if req.Action == "" {
		req.Action = publish.ActionOpenPR
	}
	if req.Source.Repo == "" {
		req.Source.Repo = task.Repo
	}
	if req.Source.Item == 0 {
		req.Source.Item = sess.Item
	}
	return e.Publish(req)
}

func (e *Engine) CleanupOwnedRefs(effortKey, ref string) error {
	if !strings.HasPrefix(strings.TrimPrefix(ref, "refs/heads/"), "rusui/") {
		return ErrRefNotOwned
	}
	task, err := store.GetLatestTask(e.Store, effortKey)
	if err != nil || task == nil {
		return ErrNoEffort
	}
	want := "rusui/" + fmt.Sprint(task.SessionID) + "/"
	branch := strings.TrimPrefix(ref, "refs/heads/")
	if !strings.HasPrefix(branch, want) {
		return ErrRefNotOwned
	}
	if e.Publisher == nil {
		return fmt.Errorf("publisher required")
	}
	return e.Publisher.DeleteRef(task.Repo, ref)
}
