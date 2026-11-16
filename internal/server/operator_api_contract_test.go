package server

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/acp"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

// operatorAPIGolden pins the response shape of the supported operator
// HTTP+SSE surface (ADR 0086). Regenerate with
// `go test ./internal/server -run TestOperatorAPIResponseShapes -update`
// only for a change the compatibility contract allows.
const operatorAPIGolden = "testdata/operator-api.golden.json"

// TestOperatorAPIResponseShapes is the operator-api positive check: the
// JSON key paths and value types of create, detail, list, read, follow
// events, and follow-up must match the golden file, so a renamed, removed,
// or retyped response field fails.
func TestOperatorAPIResponseShapes(t *testing.T) {
	_, hs, e := consoleEnv(t)
	got := map[string][]string{}

	created := operatorCall(t, hs, http.MethodPost, "/projects/test/sessions", `{"kind":"run","prompt":"shape"}`, http.StatusCreated)
	got["POST /projects/{slug}/sessions"] = jsonShape(t, created)
	var c struct {
		SessionID int64 `json:"session_id"`
	}
	if err := json.Unmarshal(created, &c); err != nil || c.SessionID == 0 {
		t.Fatalf("create %s %v", created, err)
	}
	sid := fmt.Sprint(c.SessionID)
	addEntry(t, e, c.SessionID, "shape-entry", acp.ActionUpdate, "entry body")

	got["POST /sessions/{id}/turns"] = jsonShape(t, operatorCall(t, hs, http.MethodPost, "/sessions/"+sid+"/turns", `{"prompt":"again"}`, http.StatusOK))
	got["GET /sessions/{id}"] = jsonShape(t, operatorCall(t, hs, http.MethodGet, "/sessions/"+sid, "", http.StatusOK))
	got["GET /sessions"] = jsonShape(t, operatorCall(t, hs, http.MethodGet, "/sessions", "", http.StatusOK))
	got["GET /sessions/{id}/read"] = jsonShape(t, operatorCall(t, hs, http.MethodGet, "/sessions/"+sid+"/read", "", http.StatusOK))

	s, res := openFollow(t, hs, c.SessionID, 0, "op-tok")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("follow %d", res.StatusCode)
	}
	// History and the queued state arrive before the turn completes.
	follow := func(ev sseEvent) {
		key := "GET /sessions/{id}/read/follow event:" + ev.event
		got[key] = mergeShape(got[key], jsonShape(t, []byte(ev.data)))
	}
	for _, want := range []string{"session", "entry", "state"} {
		follow(s.expect(want))
	}
	setTurns(t, e, c.SessionID, "completed")
	for {
		ev := s.nextNonComment()
		follow(ev)
		if ev.event == "end" {
			break
		}
	}
	s.closed()

	if *updateGolden {
		b, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(operatorAPIGolden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(operatorAPIGolden, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(operatorAPIGolden)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string][]string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for k := range want {
		names[k] = true
	}
	for k := range got {
		names[k] = true
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		missing, extra := shapeDiff(want[name], got[name])
		if len(missing) > 0 || len(extra) > 0 {
			t.Errorf("%s: missing %v, unexpected %v (see %s)", name, missing, extra, operatorAPIGolden)
		}
	}
}

// TestOperatorAPIRefusesUnsupportedRequests is the operator-api negative
// check: a create without the required `kind` and a follow-up that is both
// a steer and queued are refused with 400 instead of guessed.
func TestOperatorAPIRefusesUnsupportedRequests(t *testing.T) {
	_, hs, _ := consoleEnv(t)
	operatorCall(t, hs, http.MethodPost, "/projects/test/sessions", `{"prompt":"no kind"}`, http.StatusBadRequest)
	sid := createRunSession(t, hs, "p")
	operatorCall(t, hs, http.MethodPost, fmt.Sprintf("/sessions/%d/turns", sid), `{"prompt":"x","steer":true,"queued":true}`, http.StatusBadRequest)
}

// operatorCall sends one operator-token request and returns the body after
// checking the status.
func operatorCall(t *testing.T, hs *httptest.Server, method, path, body string, status int) []byte {
	t.Helper()
	req, err := http.NewRequest(method, hs.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != status {
		t.Fatalf("%s %s: %d %s, want %d", method, path, res.StatusCode, b, status)
	}
	return b
}

// jsonShape lists every key path in a JSON document with its value type,
// for example `transcript[].id:string`. Array elements share one path.
func jsonShape(t *testing.T, raw []byte) []string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("json %s: %v", raw, err)
	}
	seen := map[string]bool{}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			seen[path+":object"] = true
			for k, child := range x {
				walk(strings.TrimPrefix(path+"."+k, "."), child)
			}
		case []any:
			seen[path+":array"] = true
			for _, child := range x {
				walk(path+"[]", child)
			}
		case string:
			seen[path+":string"] = true
		case float64:
			seen[path+":number"] = true
		case bool:
			seen[path+":bool"] = true
		case nil:
			seen[path+":null"] = true
		}
	}
	walk("", v)
	return slices.Sorted(maps.Keys(seen))
}

// mergeShape is the sorted union of two shapes.
func mergeShape(a, b []string) []string {
	out := slices.Concat(a, b)
	slices.Sort(out)
	return slices.Compact(out)
}

func shapeDiff(want, got []string) (missing, extra []string) {
	for _, w := range want {
		if !slices.Contains(got, w) {
			missing = append(missing, w)
		}
	}
	for _, g := range got {
		if !slices.Contains(want, g) {
			extra = append(extra, g)
		}
	}
	return missing, extra
}
