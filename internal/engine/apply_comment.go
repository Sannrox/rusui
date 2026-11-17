package engine

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

var secretToken = regexp.MustCompile(`(?i)\b(ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]+\b`)

func liveCommentBlocked(it snapshot.Item) bool {
	if it.Draft || it.State != "open" {
		return true
	}
	for _, l := range it.Labels {
		if l == "rusui:hold" || l == "security" {
			return true
		}
	}
	return false
}

func redactSecrets(s string) string {
	return secretToken.ReplaceAllString(s, "[redacted]")
}

func privateLeak(s string) bool {
	u := strings.ToUpper(s)
	return strings.Contains(u, "BEGIN ") && strings.Contains(u, "PRIVATE KEY")
}

func publicCommentBody(a ProposedAction, actionID, class, sentence string, publishable map[string]any) (string, string) {
	text := ""
	if publishable != nil {
		if s, ok := publishable["comment"].(string); ok {
			text = strings.TrimSpace(s)
		}
	}
	if text == "" {
		text = fmt.Sprintf("rusui %s (evidence_class=%s). %s", a.ReasonCode, class, sentence)
	}
	text = redactSecrets(text)
	if privateLeak(text) {
		return "", "private evidence"
	}
	if strings.TrimSpace(text) == "" {
		return "", "empty comment"
	}
	return text + "\n\n" + gh.ActionMarker(actionID), ""
}

func (e *Engine) publishPendingComments(repo string, item int) error {
	if e.Comments == nil {
		return nil
	}
	var list []store.PendingComment
	err := e.Store.Tx(func(tx *store.Tx) error {
		var err error
		list, err = store.ListPendingCommentsTx(tx, repo, item)
		return err
	})
	if err != nil {
		return err
	}
	var first error
	for _, p := range list {
		rec, pubErr := e.Comments.PublishComment(repo, item, gh.CommentSpec{
			ActionID: p.ActionID, Body: p.Body, ID: p.CommentID,
		})
		err := e.Store.Tx(func(tx *store.Tx) error {
			if pubErr != nil {
				state := "failed"
				if errors.Is(pubErr, gh.ErrCommentUncertain) {
					state = "uncertain"
				} else if errors.Is(pubErr, gh.ErrCommentDenied) {
					state = "cancelled"
				}
				return store.SettlePendingCommentTx(tx, p.ActionID, state)
			}
			return store.CompletePendingCommentTx(tx, p.ActionID, rec.ID)
		})
		if err != nil && first == nil {
			first = err
		}
		if pubErr != nil && first == nil {
			first = pubErr
		}
	}
	return first
}
