package engine

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/snapshot"
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
	type pending struct {
		actionID  string
		body      string
		commentID int64
	}
	var list []pending
	err := e.Store.Tx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT aa.action_id, aa.github_comment_id, a.body
FROM apply_attempts aa JOIN actions a ON a.action_id=aa.action_id
WHERE aa.repo=? AND aa.item=? AND aa.state IN ('in_flight','uncertain') AND a.action_type='comment'`, repo, item)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var p pending
			if err := rows.Scan(&p.actionID, &p.commentID, &p.body); err != nil {
				return err
			}
			list = append(list, p)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	var first error
	for _, p := range list {
		rec, pubErr := e.Comments.PublishComment(repo, item, gh.CommentSpec{
			ActionID: p.actionID, Body: p.body, ID: p.commentID,
		})
		err := e.Store.Tx(func(tx *sql.Tx) error {
			if pubErr != nil {
				state := "failed"
				if errors.Is(pubErr, gh.ErrCommentUncertain) {
					state = "uncertain"
				} else if errors.Is(pubErr, gh.ErrCommentDenied) {
					state = "cancelled"
				}
				_, err := tx.Exec(`UPDATE apply_attempts SET state=? WHERE action_id=? AND state IN ('in_flight','uncertain')`, state, p.actionID)
				return err
			}
			_, err := tx.Exec(`UPDATE apply_attempts SET state='succeeded', github_comment_id=? WHERE action_id=? AND state IN ('in_flight','uncertain')`, rec.ID, p.actionID)
			return err
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
