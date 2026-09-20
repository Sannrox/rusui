package server

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/acp"
)

func TestOperatorTokenReadsSession(t *testing.T) {
	s, hs, e := consoleEnv(t)
	_ = s
	sid, err := e.StartRun("test", "editor", "")
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("GET", hs.URL+"/sessions/"+strconv.FormatInt(sid, 10), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), `"ID":`+strconv.FormatInt(sid, 10)) && !strings.Contains(string(body), strconv.FormatInt(sid, 10)) {
		t.Fatalf("operator get %d %s", res.StatusCode, body)
	}
}

func TestEditorHTTPPlaneFollowUp(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "editor", "")
	if err != nil {
		t.Fatal(err)
	}
	p := &acp.HTTPPlane{Base: hs.URL, Token: "op-tok"}
	sess, err := p.GetSession(context.Background(), sid)
	if err != nil || sess.ID != sid {
		t.Fatalf("%+v %v", sess, err)
	}
	if err := p.FollowUp(context.Background(), sid, "from editor"); err != nil {
		t.Fatal(err)
	}
	pBad := &acp.HTTPPlane{Base: hs.URL, Token: "nope"}
	if _, err := pBad.GetSession(context.Background(), sid); err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("bad token %v", err)
	}
}
