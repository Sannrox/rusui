package gh

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePulls is a GitHub stand-in for the App publisher: it mints scoped
// tokens and keeps pull requests by number.
type fakePulls struct {
	mu       sync.Mutex
	mints    []map[string]any
	pulls    map[int]fakePull
	heads    map[string]string // branch -> commit
	calls    []string
	dropNext bool // create the next pull request but fail the response
}

type fakePull struct {
	head string
	open bool
}

func (f *fakePulls) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	num, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/repos/o/r/pulls/"))
	switch {
	case r.URL.Path == "/app/installations/9/access_tokens":
		var scope map[string]any
		_ = json.NewDecoder(r.Body).Decode(&scope)
		f.mints = append(f.mints, scope)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_repo", "expires_at": time.Unix(1_700_003_600, 0).UTC()})
	case r.Header.Get("Authorization") != "Bearer ghs_repo":
		w.WriteHeader(http.StatusUnauthorized)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/o/r/git/ref/heads/"):
		sha, ok := f.heads[strings.TrimPrefix(r.URL.Path, "/repos/o/r/git/ref/heads/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": sha}})
	case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls":
		head := strings.TrimPrefix(r.URL.Query().Get("head"), "o:")
		out := []map[string]int{}
		for n, p := range f.pulls {
			if p.open && p.head == head {
				out = append(out, map[string]int{"number": n})
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodGet && num > 0:
		p, ok := f.pulls[num]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		state := "closed"
		if p.open {
			state = "open"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"state": state, "head": map[string]any{"ref": p.head, "repo": map[string]string{"full_name": "o/r"}}})
	case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/pulls":
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		n := 40 + len(f.pulls)
		f.pulls[n] = fakePull{head: in["head"], open: true}
		if f.dropNext {
			f.dropNext = false
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]int{"number": n})
	case r.Method == http.MethodPatch && num > 0:
		_, _ = w.Write([]byte(`{"number":` + strconv.Itoa(num) + `}`))
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/repos/o/r/git/refs/heads/"):
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		sha, _ := in["sha"].(string)
		force, _ := in["force"].(bool)
		if force || sha == "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		branch := strings.TrimPrefix(r.URL.Path, "/repos/o/r/git/refs/heads/")
		f.heads[branch] = sha
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": sha}})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakePulls) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

func newFakePublisher(t *testing.T) (*fakePulls, *Publisher) {
	t.Helper()
	f := &fakePulls{pulls: map[int]fakePull{}, heads: map[string]string{"rusui/7/fix": "c1"}}
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	return f, &Publisher{
		Tokens: &InstallationTokens{AppID: 1, InstallationID: 9, Key: testKey(t), BaseURL: ts.URL, HTTP: ts.Client(),
			Now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }},
		BaseURL: ts.URL, HTTP: ts.Client(),
	}
}

func TestPublisherCreatesOnceAndUpdates(t *testing.T) {
	f, p := newFakePublisher(t)
	spec := PullSpec{Head: "rusui/7/fix", SHA: "c1", Base: "main", Title: "fix", Body: "b"}
	n, err := p.PublishPull("o/r", spec)
	if err != nil || n != 40 {
		t.Fatalf("create %d %v", n, err)
	}
	// A retry after a lost response finds the open pull request by head.
	if n, err = p.PublishPull("o/r", spec); err != nil || n != 40 {
		t.Fatalf("retry %d %v", n, err)
	}
	if n, err = p.PublishPull("o/r", PullSpec{Number: 40, Head: "rusui/7/fix", SHA: "c1", Title: "fix v2"}); err != nil || n != 40 {
		t.Fatalf("update %d %v", n, err)
	}
	if got := f.count("POST /repos/o/r/pulls"); got != 1 {
		t.Fatalf("created %d pull requests", got)
	}
	if len(f.mints) != 1 {
		t.Fatalf("scoped token minted %d times", len(f.mints))
	}
	scope, _ := json.Marshal(f.mints[0])
	if string(scope) != `{"permissions":{"contents":"write","pull_requests":"write"},"repositories":["r"]}` {
		t.Fatalf("scope %s", scope)
	}
}

func TestPublisherRecoversALostCreateResponse(t *testing.T) {
	f, p := newFakePublisher(t)
	f.dropNext = true
	n, err := p.PublishPull("o/r", PullSpec{Head: "rusui/7/fix", SHA: "c1", Base: "main", Title: "fix"})
	if err != nil || n != 40 || f.count("POST /repos/o/r/pulls") != 1 {
		t.Fatalf("recover %d %v %v", n, err, f.calls)
	}
}

// An update never edits a pull request that is not the session branch's
// open pull request.
func TestPublisherRefusesToEditForeignOrClosedPullRequest(t *testing.T) {
	f, p := newFakePublisher(t)
	f.pulls[5] = fakePull{head: "someone/else", open: true}
	f.pulls[6] = fakePull{head: "rusui/7/fix", open: false}
	for _, n := range []int{5, 6, 99} {
		if _, err := p.PublishPull("o/r", PullSpec{Number: n, Head: "rusui/7/fix", SHA: "c1", Title: "x"}); err == nil {
			t.Fatalf("edited #%d", n)
		}
	}
	if got := f.count("PATCH /repos/o/r/pulls/5") + f.count("PATCH /repos/o/r/pulls/6"); got != 0 {
		t.Fatalf("patched %d", got)
	}
}

// The plane writes only for the commit it was asked to publish: a moved,
// missing, or unnamed branch head is refused before any create or edit.
func TestPublisherRefusesWhenBranchHeadIsNotTheCandidate(t *testing.T) {
	f, p := newFakePublisher(t)
	f.pulls[5] = fakePull{head: "rusui/7/fix", open: true}
	for name, spec := range map[string]PullSpec{
		"moved":   {Head: "rusui/7/fix", SHA: "c0", Base: "main", Title: "x"},
		"update":  {Number: 5, Head: "rusui/7/fix", SHA: "c0", Title: "x"},
		"missing": {Head: "rusui/7/gone", SHA: "c1", Base: "main", Title: "x"},
		"unnamed": {Head: "rusui/7/fix", Base: "main", Title: "x"},
		"query":   {Head: "rusui/7/fix?x=1", SHA: "c1", Base: "main", Title: "x"},
		"dotdot":  {Head: "rusui/7/../fix", SHA: "c1", Base: "main", Title: "x"},
	} {
		if _, err := p.PublishPull("o/r", spec); !errors.Is(err, ErrHeadMismatch) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if got := f.count("POST /repos/o/r/pulls") + f.count("PATCH /repos/o/r/pulls/5"); got != 0 {
		t.Fatalf("wrote %d times: %v", got, f.calls)
	}
}

func TestPublisherPushBaseFastForwardsAndDoesNotOpenPullRequest(t *testing.T) {
	f, p := newFakePublisher(t)
	if err := p.PushBase("o/r", "main", "c1"); err != nil {
		t.Fatal(err)
	}
	if f.heads["main"] != "c1" {
		t.Fatalf("heads %+v", f.heads)
	}
	if got := f.count("POST /repos/o/r/pulls"); got != 0 {
		t.Fatalf("opened %d pull requests", got)
	}
	if got := f.count("PATCH /repos/o/r/git/refs/heads/main"); got != 1 {
		t.Fatalf("patched %d: %v", got, f.calls)
	}
	if err := p.PushBase("o/r", "main", ""); !errors.Is(err, ErrHeadMismatch) {
		t.Fatalf("empty sha: %v", err)
	}
	if err := p.PushBase("o/r", "../main", "c1"); !errors.Is(err, ErrHeadMismatch) {
		t.Fatalf("dotdot: %v", err)
	}
}
