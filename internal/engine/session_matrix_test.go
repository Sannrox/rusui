package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/store"
)

func TestD6SessionWorkflowMatrix(t *testing.T) {
	t.Run("R01_review", func(t *testing.T) {
		h := setup(t)
		h.putRefresh(issue(1))
		c := h.claim()
		if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
			t.Fatal(err)
		}
		n, err := store.CountReceipts(h.st, c.Job.ID)
		if err != nil || n < 1 {
			t.Fatalf("receipts %d %v", n, err)
		}
	})
	t.Run("R02_run", func(t *testing.T) {
		h := setup(t)
		id, err := h.e.StartRun("test", "do the thing", "")
		if err != nil || id == 0 {
			t.Fatal(err)
		}
		c, err := h.e.Claim("example/test-repo")
		if err != nil || c == nil || c.Job.Lane != "run" {
			t.Fatalf("%+v %v", c, err)
		}
	})
	t.Run("R03_scheduled", func(t *testing.T) {
		h := setup(t)
		if _, err := h.e.CreateSchedule("test", "hourly", "1m", "scheduled work"); err != nil {
			t.Fatal(err)
		}
		t0 := time.Unix(1_700_000_000, 0).UTC()
		if err := h.e.StepSchedules(t0); err != nil {
			t.Fatal(err)
		}
		c, err := h.e.Claim("example/test-repo")
		if err != nil || c == nil || c.Job.Lane != "scheduled" {
			t.Fatalf("%+v %v", c, err)
		}
	})
	t.Run("R04_R13_ten_followups", func(t *testing.T) {
		h := setup(t)
		sid, err := h.e.StartRun("test", "first", "")
		if err != nil {
			t.Fatal(err)
		}
		for i := range 10 {
			if _, _, err := h.e.PromptFollowUp(sid, fmt.Sprintf("fu-%02d", i)); err != nil {
				t.Fatal(err)
			}
		}
		for i := range 10 {
			c, err := h.e.Claim("example/test-repo")
			if err != nil || c == nil {
				t.Fatalf("claim %d %v %v", i, c, err)
			}
			want := fmt.Sprintf("fu-%02d", i)
			if c.Snapshot.Body != want {
				t.Fatalf("order %d got %q", i, c.Snapshot.Body)
			}
			if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("R14_disconnect", func(t *testing.T) {
		h := setup(t)
		sid, err := h.e.StartRun("test", "watch", "")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "GET", h.http.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/attach", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer wsec")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		c, err := h.e.Claim("example/test-repo")
		if err != nil || c == nil {
			t.Fatalf("claim after disconnect %v %v", c, err)
		}
	})
	t.Run("R15_plane_restart", func(t *testing.T) {
		h := setup(t)
		sid, err := h.e.StartRun("test", "restart", "")
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest("POST", h.http.URL+"/jobs/claim", strings.NewReader(`{"repo":"example/test-repo"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer wsec")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 {
			t.Fatalf("claim %d %s", res.StatusCode, body)
		}
		var claimed struct {
			JobID int64 `json:"job_id"`
		}
		if err := json.Unmarshal(body, &claimed); err != nil || claimed.JobID == 0 {
			t.Fatalf("claim body %s %v", body, err)
		}
		dst := filepath.Join(t.TempDir(), "restore.db")
		q := fmt.Sprintf("VACUUM INTO '%s'", strings.ReplaceAll(dst, "'", "''"))
		if _, err := h.st.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
		st2, rep, err := store.Restore(dst)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st2.Close() })
		if rep.Sessions < 1 || rep.LeasesCleared < 1 || rep.GrantsDropped < 1 {
			t.Fatalf("restore report %+v", rep)
		}
		sess, err := store.GetSession(st2, sid)
		if err != nil || sess.Prompt != "restart" {
			t.Fatalf("session %+v %v", sess, err)
		}
		turn, err := store.GetTurn(st2, claimed.JobID)
		if err != nil || turn.State != "queued" {
			t.Fatalf("turn %+v %v", turn, err)
		}
		if err := h.e.Recover(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("R16_approval_deny_grant_expiry", func(t *testing.T) {
		h := setup(t)
		h.putRefresh(issue(1))
		c := h.claim()
		turn, err := store.GetTurn(h.st, c.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		sid := turn.SessionID
		if err := store.InsertAction(h.st, store.Action{
			ID: "d6-deny", SessionID: &sid, TurnID: &c.Job.ID, Repo: c.Job.Repo, Item: c.Job.Item,
			Type: acp.ActionApproval, ReasonCode: acp.ReasonUnmatched,
			EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
			Body: `{"tool":"shell"}`,
		}); err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest("POST", h.http.URL+"/approvals/d6-deny", strings.NewReader(`{"decision":"deny"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer wsec")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusNoContent {
			t.Fatalf("deny %d", res.StatusCode)
		}
		req, err = http.NewRequest("GET", h.http.URL+"/approvals/d6-deny", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer wsec")
		res, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Decision string `json:"decision"`
			Valid    bool   `json:"valid"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got.Decision != "deny" || !got.Valid {
			t.Fatalf("deny visible %+v %s", got, body)
		}
		h.clk.Advance(4 * time.Minute)
		if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err == nil {
			t.Fatal("expired lease complete")
		}
	})
	t.Run("R17_duplicate_intake", func(t *testing.T) {
		h := setup(t)
		it := issue(1)
		h.f.Put(it)
		body := []byte(`{"repository":{"full_name":"example/test-repo"},"issue":{"number":1}}`)
		for range 2 {
			req := httptest.NewRequest("POST", "/hooks/github", bytes.NewReader(body))
			req.Header.Set("X-Hub-Signature-256", gh.Sign("whsec", body))
			req.Header.Set("X-GitHub-Delivery", "dup-1")
			rr := httptest.NewRecorder()
			h.srv.Handler().ServeHTTP(rr, req)
			if rr.Code != 200 {
				t.Fatalf("code %d", rr.Code)
			}
		}
		var n int
		if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM deliveries WHERE delivery_id=?`, "dup-1").Scan(&n); err != nil || n != 1 {
			t.Fatalf("deliveries %d %v", n, err)
		}
	})
	t.Run("R18_schedule_skip_live", func(t *testing.T) {
		h := setup(t)
		if _, err := h.e.CreateSchedule("test", "hourly", "1m", "scheduled work"); err != nil {
			t.Fatal(err)
		}
		t0 := time.Unix(1_700_000_000, 0).UTC()
		if err := h.e.StepSchedules(t0); err != nil {
			t.Fatal(err)
		}
		if err := h.e.StepSchedules(t0.Add(61 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE kind=?`, store.SessionKindScheduled).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("live skip %d", n)
		}
	})
	t.Run("R19_cancel", func(t *testing.T) {
		h := setup(t)
		sid, err := h.e.StartRun("test", "first", "")
		if err != nil {
			t.Fatal(err)
		}
		c, err := h.e.Claim("example/test-repo")
		if err != nil || c == nil {
			t.Fatal(err)
		}
		if err := h.e.CancelSession(sid); err != nil {
			t.Fatal(err)
		}
		if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err == nil {
			t.Fatal("complete after cancel")
		}
	})
	t.Run("R20_budget_cap", func(t *testing.T) {
		h := setup(t)
		h.putRefresh(issue(1))
		h.putRefresh(issue(2))
		_ = h.claim()
		c2, err := h.e.Claim("example/test-repo")
		if !errors.Is(err, engine.ErrBudget) || c2 != nil {
			t.Fatalf("second claim %v %v", c2, err)
		}
	})
}
