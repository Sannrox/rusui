// Package gl is the GitLab.com intake edge: signed issue webhooks and a
// read-only Issues API client. Credentials stay here; guests never receive them.
package gl

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
)

const (
	// Origin is the only GitLab host this profile accepts (ADR 0021).
	Origin = "https://gitlab.com"
	// IssueHook is X-Gitlab-Event for ordinary project issues.
	IssueHook = "Issue Hook"
	// MaxTimestampAge is Standard Webhooks replay protection.
	MaxTimestampAge = 5 * time.Minute
	apiBase         = Origin + "/api/v4"
)

var (
	errBadSigningToken = errors.New("gitlab: signing token must be whsec_ plus base64")
	// ErrConfidential is a confidential-issue event or issue.
	ErrConfidential = errors.New("gitlab: confidential issue events are refused")
	errUnsupported  = errors.New("gitlab: not an ordinary issue event")
)

// DeliveryID namespaces webhook-id by provider and origin (ADR 0021).
func DeliveryID(webhookID string) string {
	return "gitlab:" + Origin + ":" + webhookID
}

// NewSigningToken returns a GitLab-shaped signing token for tests.
func NewSigningToken() (string, error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return "", err
	}
	return "whsec_" + base64.StdEncoding.EncodeToString(key[:]), nil
}

func signingKey(secret string) ([]byte, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(secret), "whsec_")
	if !ok || rest == "" {
		return nil, errBadSigningToken
	}
	key, err := base64.StdEncoding.DecodeString(rest)
	if err != nil || len(key) == 0 {
		return nil, errBadSigningToken
	}
	return key, nil
}

// Sign returns the webhook-signature value for secret, id, timestamp, and body.
func Sign(secret, id, timestamp string, body []byte) (string, error) {
	key, err := signingKey(secret)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id))
	mac.Write([]byte("."))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Verify checks webhook-signature against the exact signed bytes.
func Verify(secret, id, timestamp, header string, body []byte) bool {
	if secret == "" || id == "" || timestamp == "" || header == "" {
		return false
	}
	want, err := Sign(secret, id, timestamp, body)
	if err != nil {
		return false
	}
	for sig := range strings.SplitSeq(header, " ") {
		sig = strings.TrimSpace(sig)
		if sig == "" {
			continue
		}
		if hmac.Equal([]byte(want), []byte(sig)) {
			return true
		}
	}
	return false
}

// TimestampFresh reports whether webhook-timestamp is within maxAge of now.
func TimestampFresh(timestamp string, now time.Time, maxAge time.Duration) bool {
	sec, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil || sec <= 0 {
		return false
	}
	ts := time.Unix(sec, 0).UTC()
	delta := now.UTC().Sub(ts)
	if delta < 0 {
		delta = -delta
	}
	return delta <= maxAge
}

// IssueEvent is the routing fields of a GitLab.com Issue Hook.
type IssueEvent struct {
	ProjectID   int64
	ProjectPath string
	IID         int
	Action      string
}

// ParseIssueWebhook extracts an ordinary issue wake-up. Confidential,
// merge-request, and other work-item events are refused.
func ParseIssueWebhook(eventHeader string, body []byte) (IssueEvent, error) {
	if eventHeader != IssueHook {
		return IssueEvent{}, errUnsupported
	}
	var p struct {
		ObjectKind string `json:"object_kind"`
		EventType  string `json:"event_type"`
		Project    struct {
			ID                int64  `json:"id"`
			PathWithNamespace string `json:"path_with_namespace"`
			WebURL            string `json:"web_url"`
		} `json:"project"`
		ObjectAttributes struct {
			IID          int    `json:"iid"`
			Action       string `json:"action"`
			Confidential bool   `json:"confidential"`
			Type         string `json:"type"`
		} `json:"object_attributes"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return IssueEvent{}, err
	}
	if p.ObjectKind != "issue" {
		return IssueEvent{}, errUnsupported
	}
	if p.EventType != "" && p.EventType != "issue" {
		return IssueEvent{}, errUnsupported
	}
	if p.ObjectAttributes.Type != "" && !strings.EqualFold(p.ObjectAttributes.Type, "Issue") {
		return IssueEvent{}, errUnsupported
	}
	if p.ObjectAttributes.Confidential {
		return IssueEvent{}, ErrConfidential
	}
	switch p.ObjectAttributes.Action {
	case "open", "update", "close", "reopen":
	default:
		return IssueEvent{}, errUnsupported
	}
	if p.Project.ID <= 0 || p.ObjectAttributes.IID <= 0 {
		return IssueEvent{}, errUnsupported
	}
	if err := requireGitLabCom(p.Project.WebURL); err != nil {
		return IssueEvent{}, err
	}
	return IssueEvent{
		ProjectID:   p.Project.ID,
		ProjectPath: p.Project.PathWithNamespace,
		IID:         p.ObjectAttributes.IID,
		Action:      p.ObjectAttributes.Action,
	}, nil
}

func requireGitLabCom(webURL string) error {
	u, err := url.Parse(webURL)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "gitlab.com") {
		return fmt.Errorf("gitlab: origin must be %s", Origin)
	}
	return nil
}

func itemFromIssue(projectID int64, path string, in glIssue) (snapshot.Item, error) {
	if in.Confidential {
		return snapshot.Item{}, ErrConfidential
	}
	if in.IID <= 0 {
		return snapshot.Item{}, fmt.Errorf("gitlab: missing iid")
	}
	state := in.State
	switch state {
	case "opened":
		state = "open"
	case "closed":
		state = "closed"
	}
	it := snapshot.Item{
		Repo:        policy.GitLabSourceKey(projectID),
		Item:        in.IID,
		ItemKind:    "issue",
		State:       state,
		Title:       in.Title,
		Body:        in.Description,
		Labels:      append([]string(nil), in.Labels...),
		UpdatedAt:   rfc3339(in.UpdatedAt),
		CreatedAt:   rfc3339(in.CreatedAt),
		Provider:    policy.ProviderGitLab,
		Origin:      Origin,
		ProjectID:   projectID,
		ProjectPath: path,
	}
	for _, a := range in.Assignees {
		if a.Username != "" {
			it.Assignees = append(it.Assignees, a.Username)
		}
	}
	return it, nil
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
