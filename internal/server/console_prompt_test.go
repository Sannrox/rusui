package server

import (
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestConsolePromptSteersWithCSRF(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "original", "")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := e.Claim("example/test-repo")
	if err != nil || claim == nil {
		t.Fatalf("claim %+v err %v", claim, err)
	}
	operator := operatorClient(t, hs)
	operator.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	page, err := operator.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if page.StatusCode != http.StatusOK || !strings.Contains(string(html), `value="steer">Steer running turn`) {
		t.Fatalf("session page %d %s", page.StatusCode, html)
	}
	csrf := csrfFrom(string(html))
	values := url.Values{"csrf": {csrf}, "mode": {"steer"}, "prompt": {"correct the direction"}}
	res, err := operator.PostForm(hs.URL+"/console/sessions/"+strconv.FormatInt(sid, 10)+"/prompt", values)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "Steer+queued") {
		t.Fatalf("steer response %d location=%q", res.StatusCode, res.Header.Get("Location"))
	}
	steer, err := e.HeartbeatSteer(claim.Job.ID, claim.Job.LeaseGeneration, claim.Job.ClaimedRevision)
	if err != nil || steer == nil || steer.Prompt != "correct the direction" {
		t.Fatalf("heartbeat steer %+v err %v", steer, err)
	}
	bad := url.Values{"mode": {"steer"}, "prompt": {"cannot forge this"}}
	res, err = operator.PostForm(hs.URL+"/console/sessions/"+strconv.FormatInt(sid, 10)+"/prompt", bad)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("missing csrf status %d", res.StatusCode)
	}
}
