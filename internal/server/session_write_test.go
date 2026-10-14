package server

import (
	"net/http"
	"strconv"
	"testing"
)

func TestApplySessionWriteRequiresOperatorAndProposal(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "ordinary", "")
	if err != nil {
		t.Fatal(err)
	}
	path := hs.URL + "/sessions/" + strconv.FormatInt(sid, 10) + "/write-actions"
	if code := postJSONStatus(t, path, "wsec", `{}`); code != http.StatusUnauthorized {
		t.Fatalf("worker %d", code)
	}
	if code := postJSONStatus(t, path, "nope", `{}`); code != http.StatusUnauthorized {
		t.Fatalf("bad token %d", code)
	}
	if code := postJSONStatus(t, path, "op-tok", `{}`); code != http.StatusConflict {
		t.Fatalf("ordinary run %d", code)
	}
	if code := postJSONStatus(t, hs.URL+"/sessions/999999/write-actions", "op-tok", `{}`); code != http.StatusNotFound {
		t.Fatalf("missing %d", code)
	}
}
