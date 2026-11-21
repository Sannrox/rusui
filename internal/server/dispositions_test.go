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
	t.Parallel()
	_, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "disp", "")
	if err != nil {
		t.Fatal(err)
	}
	var tid int64
	if err := e.Store.DB.QueryRow(`INSERT INTO turns (session_id, lane, state) VALUES (?, 'review', 'completed') RETURNING id`, sid).Scan(&tid); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := e.Store.DB.QueryRow(`INSERT INTO review_revisions (job_id, claimed_revision, item_hash, main_sha, payload) VALUES (?, 1, 'h', 'm', '{}') RETURNING id`, tid).Scan(&id); err != nil {
		t.Fatal(err)
	}
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

func TestDispositionAPIRoundTripsOptionalSeconds(t *testing.T) {
	t.Parallel()
	_, hs, e := consoleEnv(t)
	_, withTime := inboxResult(t, e, "review", 11, "h11")
	_, withoutTime := inboxResult(t, e, "review", 12, "h12")
	do := func(path, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, hs.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer op-tok")
		out, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = out.Body.Close() })
		return out
	}
	ok := do("/results/"+strconv.FormatInt(withTime, 10)+"/dispositions", `{"disposition":"useful","seconds":90}`)
	var d store.Disposition
	if ok.StatusCode != http.StatusCreated || json.NewDecoder(ok.Body).Decode(&d) != nil || d.Seconds == nil || *d.Seconds != 90 {
		t.Fatalf("record seconds %d %+v", ok.StatusCode, d)
	}
	plain := do("/results/"+strconv.FormatInt(withoutTime, 10)+"/dispositions", `{"disposition":"neutral"}`)
	var p store.Disposition
	if plain.StatusCode != http.StatusCreated || json.NewDecoder(plain.Body).Decode(&p) != nil || p.Seconds != nil {
		t.Fatalf("omit seconds %d %+v", plain.StatusCode, p)
	}
	neg := do("/results/"+strconv.FormatInt(withTime, 10)+"/dispositions", `{"disposition":"useful","seconds":-1}`)
	if neg.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative seconds %d", neg.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, hs.URL+"/dispositions/comment-gate", nil)
	req.Header.Set("Authorization", "Bearer op-tok")
	rep, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rep.Body.Close() })
	var g store.CommentGate
	if rep.StatusCode != http.StatusOK || json.NewDecoder(rep.Body).Decode(&g) != nil {
		t.Fatalf("report %d %+v", rep.StatusCode, g)
	}
	if g.Supervision.Seconds != 90 || len(g.Supervision.ByRepo) != 1 || g.Supervision.ByRepo[0].Repo != "example/test-repo" || g.Supervision.ByRepo[0].Seconds != 90 {
		t.Fatalf("supervision %+v", g.Supervision)
	}
}
