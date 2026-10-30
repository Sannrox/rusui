package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func setupLiveComment(t *testing.T) *harn {
	t.Helper()
	h := setup(t)
	raw := strings.Replace(fixture, "        comments: true\n        close: true", "        comments: true\n        comment: true\n        close: true", 1)
	pol, err := policy.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	h.e.Comments = h.f
	return h
}

func completeComment(t *testing.T, h *harn, item int) engine.Artifact {
	t.Helper()
	it := issue(item)
	h.putRefresh(it)
	c := h.claim()
	a := art(c, "propose_comment", "comment", "note")
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestLiveCommentPostsOnceAfterRetry(t *testing.T) {
	h := setupLiveComment(t)
	completeComment(t, h, 1)
	got := h.f.IssueComments("example/test-repo", 1)
	if len(got) != 1 {
		t.Fatalf("comments %d", len(got))
	}
	if err := h.e.ApplyAttempt("example/test-repo", 1); err != nil {
		t.Fatal(err)
	}
	if err := h.e.RetryApplyAttempts(); err != nil {
		t.Fatal(err)
	}
	got = h.f.IssueComments("example/test-repo", 1)
	if len(got) != 1 {
		t.Fatalf("after retry %d", len(got))
	}
	var state string
	var cid int64
	if err := h.st.DB.QueryRow(`SELECT state, github_comment_id FROM apply_attempts WHERE repo=? AND item=?`, "example/test-repo", 1).Scan(&state, &cid); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" || cid != got[0].ID {
		t.Fatalf("attempt %s id=%d", state, cid)
	}
}

func TestLiveCommentDeniedWhenPolicyOff(t *testing.T) {
	h := setup(t)
	h.e.Comments = h.f
	completeComment(t, h, 1)
	if n := len(h.f.IssueComments("example/test-repo", 1)); n != 0 {
		t.Fatalf("posted %d", n)
	}
	n, _ := store.CountIntended(h.st, "example/test-repo", 1)
	if n != 1 {
		t.Fatalf("dry-run intended %d", n)
	}
}

func TestLiveCommentDeniedWhenPublisherMissing(t *testing.T) {
	h := setupLiveComment(t)
	h.e.Comments = nil
	completeComment(t, h, 1)
	if n := len(h.f.IssueComments("example/test-repo", 1)); n != 0 {
		t.Fatalf("posted %d", n)
	}
}

func TestLiveCommentDeniedOnProtectedItem(t *testing.T) {
	h := setupLiveComment(t)
	it := issue(1)
	it.Labels = []string{"rusui:hold"}
	h.putRefresh(it)
	c := h.claim()
	a := art(c, "propose_comment", "comment", "note")
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	if n := len(h.f.IssueComments(it.Repo, it.Item)); n != 0 {
		t.Fatalf("posted %d", n)
	}
}

func TestLiveCommentDeniedStaleHash(t *testing.T) {
	h := setupLiveComment(t)
	it := issue(1)
	h.putRefresh(it)
	c := h.claim()
	_ = h.e.SetPause("test", true)
	a := art(c, "propose_comment", "comment", "note")
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	_ = h.e.SetPause("test", false)
	it.Body = "moved"
	h.putRefresh(it)
	if err := h.e.ApplyAttempt(it.Repo, it.Item); err != nil {
		t.Fatal(err)
	}
	if n := len(h.f.IssueComments(it.Repo, it.Item)); n != 0 {
		t.Fatalf("posted %d", n)
	}
}

func TestLiveCommentDoesNotClose(t *testing.T) {
	h := setupLiveComment(t)
	it := issue(1)
	it.CreatedAt = h.clk.T.Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	it.LastNonBotCommentAt = it.CreatedAt
	h.putRefresh(it)
	c := h.claim()
	a := art(c, "propose_close", "close", "stale_insufficient_info")
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	if n := len(h.f.IssueComments(it.Repo, it.Item)); n != 0 {
		t.Fatalf("close posted comment %d", n)
	}
	n, _ := store.CountIntended(h.st, it.Repo, it.Item)
	if n != 1 {
		t.Fatalf("close intended %d", n)
	}
}

func TestLiveCommentRedactsSecrets(t *testing.T) {
	h := setupLiveComment(t)
	it := issue(1)
	h.putRefresh(it)
	c := h.claim()
	a := art(c, "propose_comment", "comment", "note")
	a.Publishable = map[string]any{"comment": "see ghp_abcdefghijklmnopqrstuvwxyz012345"}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	got := h.f.IssueComments(it.Repo, it.Item)
	if len(got) != 1 || strings.Contains(got[0].Body, "ghp_") || !strings.Contains(got[0].Body, "[redacted]") {
		t.Fatalf("body %v", got)
	}
}

func TestLiveCommentDeniesPrivateKey(t *testing.T) {
	h := setupLiveComment(t)
	it := issue(1)
	h.putRefresh(it)
	c := h.claim()
	a := art(c, "propose_comment", "comment", "note")
	a.Publishable = map[string]any{"comment": "-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----"}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	if n := len(h.f.IssueComments(it.Repo, it.Item)); n != 0 {
		t.Fatalf("posted %d", n)
	}
}

func TestLiveCommentReconcilesUncertainAndDeleted(t *testing.T) {
	h := setupLiveComment(t)
	h.f.CommentDrop = true
	completeComment(t, h, 1)
	got := h.f.IssueComments("example/test-repo", 1)
	if len(got) != 1 {
		t.Fatalf("drop still writes %d", len(got))
	}
	var state string
	if err := h.st.DB.QueryRow(`SELECT state FROM apply_attempts WHERE repo=? AND item=?`, "example/test-repo", 1).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "uncertain" {
		t.Fatalf("state %s", state)
	}
	if err := h.e.ApplyAttempt("example/test-repo", 1); err != nil {
		t.Fatal(err)
	}
	got = h.f.IssueComments("example/test-repo", 1)
	if len(got) != 1 {
		t.Fatalf("reconcile duplicate %d", len(got))
	}
	h.f.DeleteComment("example/test-repo", 1, got[0].ID)
	if _, err := h.st.DB.Exec(`UPDATE apply_attempts SET state='uncertain' WHERE repo=? AND item=?`, "example/test-repo", 1); err != nil {
		t.Fatal(err)
	}
	if err := h.e.ApplyAttempt("example/test-repo", 1); err != nil {
		t.Fatal(err)
	}
	got = h.f.IssueComments("example/test-repo", 1)
	if len(got) != 1 {
		t.Fatalf("recreate %d", len(got))
	}
}

func TestLiveCommentUpdatesEdited(t *testing.T) {
	h := setupLiveComment(t)
	completeComment(t, h, 1)
	got := h.f.IssueComments("example/test-repo", 1)
	if len(got) != 1 {
		t.Fatal(got)
	}
	h.f.EditComment("example/test-repo", 1, got[0].ID, "human edit")
	if _, err := h.st.DB.Exec(`UPDATE apply_attempts SET state='in_flight' WHERE repo=? AND item=?`, "example/test-repo", 1); err != nil {
		t.Fatal(err)
	}
	if err := h.e.ApplyAttempt("example/test-repo", 1); err != nil {
		t.Fatal(err)
	}
	got = h.f.IssueComments("example/test-repo", 1)
	if len(got) != 1 || got[0].Body == "human edit" || !strings.Contains(got[0].Body, "<!-- rusui-action:") {
		t.Fatalf("updated %v", got)
	}
}

func TestLiveCommentDeniedByGitHub(t *testing.T) {
	h := setupLiveComment(t)
	h.f.CommentDeny = true
	completeComment(t, h, 1)
	if n := len(h.f.IssueComments("example/test-repo", 1)); n != 0 {
		t.Fatalf("posted %d", n)
	}
	var state string
	if err := h.st.DB.QueryRow(`SELECT state FROM apply_attempts WHERE repo=? AND item=?`, "example/test-repo", 1).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "cancelled" {
		t.Fatalf("state %s", state)
	}
}
