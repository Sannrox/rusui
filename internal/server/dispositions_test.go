package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/store"
)

func TestDispositionIsOperatorOnly(t *testing.T) {
	_, hs, e := consoleEnv(t)
	res, err := e.Store.DB.Exec(`INSERT INTO review_revisions (job_id, claimed_revision, item_hash, main_sha, payload) VALUES (1, 1, 'h', 'm', '{}')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	do := func(method, path, tok, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, hs.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		out, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = out.Body.Close() })
		return out
	}
	path := "/results/" + strconv.FormatInt(id, 10) + "/dispositions"
	useful := `{"disposition":"useful","note":"caught the nil deref"}`

	if got := do(http.MethodPost, path, "wsec", useful).StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("worker token %d", got)
	}
	if got := do(http.MethodGet, "/dispositions/comment-gate", "wsec", "").StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("worker report %d", got)
	}
	if got := do(http.MethodPost, "/results/999/dispositions", "op-tok", useful).StatusCode; got != http.StatusNotFound {
		t.Fatalf("unknown result %d", got)
	}
	ok := do(http.MethodPost, path, "op-tok", useful)
	var d store.Disposition
	if ok.StatusCode != http.StatusCreated || json.NewDecoder(ok.Body).Decode(&d) != nil || d.ReviewRevisionID != id || d.Value != "useful" {
		t.Fatalf("record %d %+v", ok.StatusCode, d)
	}

	rep := do(http.MethodGet, "/dispositions/comment-gate", "op-tok", "")
	var g store.CommentGate
	if rep.StatusCode != http.StatusOK || json.NewDecoder(rep.Body).Decode(&g) != nil || g.Status != "incomplete" {
		t.Fatalf("report %d %+v", rep.StatusCode, g)
	}
}
