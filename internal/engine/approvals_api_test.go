package engine_test

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/store"
)

func TestLogsAndApprovalsInbox(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "p", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertAction(h.st, store.Action{
		ID: "act-1", SessionID: &sid, Repo: sess.Repo, Item: sess.Item,
		Type: acp.ActionApproval, ReasonCode: acp.ReasonUnmatched,
		EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
		Body: `{"tool":"shell"}`,
	}); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", h.http.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/logs", nil)
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "act-1") {
		t.Fatalf("logs %d %s", res.StatusCode, b)
	}
	req, _ = http.NewRequest("GET", h.http.URL+"/approvals", nil)
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "act-1") {
		t.Fatalf("approvals %d %s", res.StatusCode, b)
	}
	req, _ = http.NewRequest("POST", h.http.URL+"/approvals/act-1", strings.NewReader(`{"decision":"allow"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("decide %d", res.StatusCode)
	}
	req, _ = http.NewRequest("GET", h.http.URL+"/approvals", nil)
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(b), "act-1") {
		t.Fatalf("still pending %s", b)
	}
}
