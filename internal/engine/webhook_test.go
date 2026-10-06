package engine_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/store"
)

func TestSessionWebhookValidDeliveryQueuesSameEnvironment(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	path, secret, err := h.e.EnsureSessionWebhook(sid)
	if err != nil || path == "" || secret == "" {
		t.Fatalf("ensure %q %q %v", path, secret, err)
	}
	c := h.claim()
	body := []byte(`{"ping":true}`)
	if err := h.e.DeliverSessionWebhook(sid, gh.Sign(secret, body), body, "evt-1"); err != nil {
		t.Fatal(err)
	}
	steer, err := h.e.HeartbeatSteer(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision)
	if err != nil || steer != nil {
		t.Fatalf("delivery steered: %+v %v", steer, err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil || !strings.Contains(c2.Snapshot.Body, "evt-1") {
		t.Fatalf("queued prompt %+v %v", c2, err)
	}
	turn, err := store.GetTurn(h.st, c2.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.SessionID != sid {
		t.Fatalf("session %d want %d", turn.SessionID, sid)
	}
	sess2, err := store.GetSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	if sess2.EnvironmentID != sess.EnvironmentID {
		t.Fatalf("environment %d want %d", sess2.EnvironmentID, sess.EnvironmentID)
	}
}

func TestSessionWebhookBadSignatureDoesNotQueue(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.e.EnsureSessionWebhook(sid); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"ping":true}`)
	if err := h.e.DeliverSessionWebhook(sid, gh.Sign("wrong", body), body, "evt-bad"); err == nil {
		t.Fatal("expected bad signature")
	}
	var n int
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM session_webhook_deliveries WHERE session_id=? AND accepted=0`, sid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("refusals %d %v", n, err)
	}
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM followup_queue WHERE session_id=? AND consumed=0`, sid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("queued %d %v", n, err)
	}
}

func TestSessionWebhookArchivedRefusesDelivery(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := h.e.EnsureSessionWebhook(sid)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.e.ArchiveSession(sid); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"ping":true}`)
	if err := h.e.DeliverSessionWebhook(sid, gh.Sign(secret, body), body, "evt-arch"); err == nil {
		t.Fatal("expected archived refusal")
	}
	var n int
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM session_webhook_deliveries WHERE session_id=? AND accepted=0`, sid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("refusals %d %v", n, err)
	}
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM followup_queue WHERE session_id=? AND consumed=0`, sid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("queued %d %v", n, err)
	}
}

func TestSessionWebhookSameDeliveryDoesNotDoubleQueue(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := h.e.EnsureSessionWebhook(sid)
	if err != nil {
		t.Fatal(err)
	}
	c := h.claim()
	body := []byte(`{"ping":true}`)
	sig := gh.Sign(secret, body)
	if err := h.e.DeliverSessionWebhook(sid, sig, body, "evt-dup"); err != nil {
		t.Fatal(err)
	}
	if err := h.e.DeliverSessionWebhook(sid, sig, body, "evt-dup"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM followup_queue WHERE session_id=? AND consumed=0`, sid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("queued %d %v", n, err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, runArt(c)); err != nil {
		t.Fatal(err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil || !strings.Contains(c2.Snapshot.Body, "evt-dup") {
		t.Fatalf("claim %+v %v", c2, err)
	}
	if _, err := h.e.Complete(c2.Job.ID, c2.Job.LeaseGeneration, c2.Job.ClaimedRevision, runArt(c2)); err != nil {
		t.Fatal(err)
	}
	if c3, err := h.e.Claim("example/test-repo"); err != nil || c3 != nil {
		t.Fatalf("double queue %+v %v", c3, err)
	}
}

func TestSessionWebhookHTTPAndGitHubIntakeUnchanged(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, body string, hdr map[string]string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, h.http.URL+path, strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		return res.StatusCode, b
	}
	code, out := do("POST", "/sessions/"+strconv.FormatInt(sid, 10)+"/webhook", "", map[string]string{"Authorization": "Bearer wsec"})
	if code != 200 {
		t.Fatalf("mint %d %s", code, out)
	}
	var minted struct {
		URL    string `json:"url"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(out, &minted); err != nil || minted.Secret == "" || minted.URL != "/hooks/sessions/"+strconv.FormatInt(sid, 10) {
		t.Fatalf("mint body %s %v", out, err)
	}
	payload := `{"ping":true}`
	code, _ = do("POST", minted.URL, payload, map[string]string{"X-Rusui-Signature-256": gh.Sign(minted.Secret, []byte(payload)), "X-Rusui-Delivery": "http-1"})
	if code != 202 {
		t.Fatalf("valid delivery %d", code)
	}
	code, _ = do("POST", minted.URL, payload, map[string]string{"X-Rusui-Signature-256": gh.Sign("nope", []byte(payload)), "X-Rusui-Delivery": "http-bad"})
	if code != 401 {
		t.Fatalf("bad sig %d", code)
	}
	ghBody := `{"repository":{"full_name":"example/test-repo"},"issue":{"number":1}}`
	code, _ = do("POST", "/hooks/github", ghBody, map[string]string{"X-Hub-Signature-256": gh.Sign("whsec", []byte(ghBody)), "X-GitHub-Delivery": "gh-1"})
	if code != 200 {
		t.Fatalf("github intake %d", code)
	}
	if h.f.CallCount() != 0 {
		t.Fatalf("github webhook fetched: %d", h.f.CallCount())
	}
}
