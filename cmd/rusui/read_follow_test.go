package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/server"
	"github.com/sannrox/rusui/internal/store"
)

type followPlane struct {
	t   *testing.T
	st  *store.Store
	h   http.Handler
	sid int64
	id  string
}

func newFollowPlane(t *testing.T) *followPlane {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pol, err := policy.Parse([]byte(attachPolicy))
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(st, pol, gh.NewFake(), clock.Real{})
	e.Env = env.Process{Root: t.TempDir()}
	e.ReloadPolicy(pol)
	h := (&server.Server{Eng: e, WorkerSec: "wsec", OperatorTok: "op-tok"}).Handler()
	req := httptest.NewRequest(http.MethodPost, "/projects/test/sessions", strings.NewReader(`{"kind":"run","prompt":"first prompt"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var created struct {
		SessionID int64 `json:"session_id"`
	}
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &created) != nil || created.SessionID == 0 {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	return &followPlane{t: t, st: st, h: h, sid: created.SessionID, id: strconv.FormatInt(created.SessionID, 10)}
}

func (p *followPlane) add(id, body string) {
	p.t.Helper()
	if err := store.InsertAction(p.st, store.Action{
		ID: id, SessionID: &p.sid, Repo: "example/test-repo", Item: 1,
		Type: acp.ActionUpdate, ReasonCode: acp.ReasonRecorded,
		EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
		Body: body,
	}); err != nil {
		p.t.Error(err)
	}
}

func (p *followPlane) finish(state string) {
	p.t.Helper()
	if _, err := p.st.DB.Exec(`UPDATE turns SET state=?, claimed_revision=pending_revision WHERE session_id=?`, state, p.sid); err != nil {
		p.t.Error(err)
	}
}

// cutWriter passes the stream through until the first transcript entry
// has reached the client, then runs onCut and ends the response, as a
// dropped tunnel or proxy would.
type cutWriter struct {
	http.ResponseWriter
	cancel context.CancelFunc
	onCut  func()
	once   sync.Once
}

func (c *cutWriter) Write(b []byte) (int, error) {
	n, err := c.ResponseWriter.Write(b)
	if bytes.Contains(b, []byte("event: entry")) {
		c.once.Do(func() {
			c.ResponseWriter.(http.Flusher).Flush()
			c.cancel()
			c.onCut()
		})
	}
	return n, err
}

func (c *cutWriter) Flush() { c.ResponseWriter.(http.Flusher).Flush() }

func fastReconnect(t *testing.T) {
	prevMin, prevMax, prevIdle := followBackoffMin, followBackoffMax, followIdle
	followBackoffMin, followBackoffMax = time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { followBackoffMin, followBackoffMax, followIdle = prevMin, prevMax, prevIdle })
}

func TestReadFollowPrintsHistoryThenLiveEventsAndCompletion(t *testing.T) {
	p := newFollowPlane(t)
	p.add("ff", "history one")
	p.add("00", "history two")
	hs := httptest.NewServer(p.h)
	t.Cleanup(hs.Close)
	out := &lockedBuffer{}
	go func() {
		// New work starts only after the client printed the history.
		for !strings.Contains(out.String(), "state: queued") {
			time.Sleep(10 * time.Millisecond)
		}
		p.add("live", "live \x1b]52;c;eA==\a event")
		p.finish("completed")
	}()

	var errOut bytes.Buffer
	if code := readMain([]string{"-follow", "-url", hs.URL, "-token", "op-tok", p.id}, out, &errOut); code != 0 {
		t.Fatalf("follow %d %s", code, errOut.String())
	}
	got := out.String()
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\a") || !strings.Contains(got, `live \u001b]52;c;eA==\u0007 event`) {
		t.Fatalf("terminal control reached stdout: %q", got)
	}
	order := []string{"session " + p.id + " run\nprompt: first prompt\n", "history one", "history two", "state: queued", "live ", "state: completed"}
	at := 0
	for _, want := range order {
		i := strings.Index(got[at:], want)
		if i < 0 {
			t.Fatalf("missing %q after offset %d in\n%s", want, at, got)
		}
		at += i + len(want)
	}
	if strings.Contains(got, "diff:") || strings.Count(got, "history one") != 1 {
		t.Fatalf("follow output\n%s", got)
	}
}

func TestReadFollowReconnectsWithoutLosingOrRepeatingEvents(t *testing.T) {
	fastReconnect(t)
	p := newFollowPlane(t)
	p.add("a", "before the cut")
	var requests []string
	var mu sync.Mutex
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.RequestURI())
		first := len(requests) == 1
		mu.Unlock()
		if !first {
			p.h.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		// Work continues and finishes while the client is disconnected.
		p.h.ServeHTTP(&cutWriter{ResponseWriter: w, cancel: cancel, onCut: func() {
			p.add("b", "gap one")
			p.add("c", "gap two")
			p.finish("completed")
		}}, r.WithContext(ctx))
	}))
	t.Cleanup(hs.Close)

	var out, errOut bytes.Buffer
	if code := readMain([]string{"-follow", "-url", hs.URL, "-token", "op-tok", p.id}, &out, &errOut); code != 0 {
		t.Fatalf("follow %d %s\n%s", code, errOut.String(), out.String())
	}
	got := out.String()
	for _, want := range []string{"session " + p.id + " run", "before the cut", "gap one", "gap two", "state: completed"} {
		if strings.Count(got, want) != 1 {
			t.Fatalf("%q printed %d times in\n%s", want, strings.Count(got, want), got)
		}
	}
	if !strings.Contains(errOut.String(), "reconnecting") {
		t.Fatalf("stderr %s", errOut.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || !strings.HasSuffix(requests[0], "after=0") || strings.HasSuffix(requests[1], "after=0") {
		t.Fatalf("requests %v", requests)
	}
}

func TestReadFollowStopsWhenAccessIsRevoked(t *testing.T) {
	fastReconnect(t)
	p := newFollowPlane(t)
	p.add("a", "seen")
	var n atomic.Int32
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			p.h.ServeHTTP(&cutWriter{ResponseWriter: w, cancel: cancel, onCut: func() {}}, r.WithContext(ctx))
			return
		}
		// The plane came back with a rotated operator token.
		r.Header.Set("Authorization", "Bearer rotated")
		p.h.ServeHTTP(w, r)
	}))
	t.Cleanup(hs.Close)

	var out, errOut bytes.Buffer
	if code := readMain([]string{"-follow", "-url", hs.URL, "-token", "op-tok", p.id}, &out, &errOut); code != 1 {
		t.Fatalf("follow %d %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "401") || n.Load() != 2 {
		t.Fatalf("requests %d stderr %s", n.Load(), errOut.String())
	}
	if !strings.Contains(out.String(), "seen") {
		t.Fatalf("stdout %s", out.String())
	}
}

func TestReadFollowExplicitErrors(t *testing.T) {
	p := newFollowPlane(t)
	var n atomic.Int32
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		p.h.ServeHTTP(w, r)
	}))
	t.Cleanup(hs.Close)
	var out, errOut bytes.Buffer
	if code := readMain([]string{"-follow", "-url", hs.URL, "-token", "op-tok", "999999"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "not found") || n.Load() != 1 {
		t.Fatalf("missing %d %d %s", code, n.Load(), errOut.String())
	}
	errOut.Reset()
	if code := readMain([]string{"-follow", "-url", hs.URL, "-token", "nope", p.id}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "auth") {
		t.Fatalf("auth %d %s", code, errOut.String())
	}

	p.finish("failed")
	out.Reset()
	if code := readMain([]string{"-follow", "-url", hs.URL, "-token", "op-tok", p.id}, &out, &errOut); code != 1 || !strings.Contains(out.String(), "state: failed") {
		t.Fatalf("failed session %d %s", code, out.String())
	}

	old := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(old.Close)
	errOut.Reset()
	if code := readMain([]string{"-follow", "-url", old.URL, "-token", "op-tok", p.id}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "does not support read -follow") {
		t.Fatalf("old plane %d %s", code, errOut.String())
	}
}

func TestReadFollowReconnectsAfterSilentConnection(t *testing.T) {
	fastReconnect(t)
	followIdle = 100 * time.Millisecond
	var n atomic.Int32
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: session\ndata: {\"session_id\":7,\"kind\":\"run\",\"prompt\":\"p\"}\n\n")
		w.(http.Flusher).Flush()
		if n.Add(1) == 1 {
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "event: end\ndata: {\"state\":\"completed\",\"turn_id\":3,\"revision\":1}\n\n")
	}))
	t.Cleanup(hs.Close)
	var out, errOut bytes.Buffer
	if code := readMain([]string{"-follow", "-url", hs.URL, "-token", "op-tok", "7"}, &out, &errOut); code != 0 {
		t.Fatalf("follow %d %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "no data from the plane") || n.Load() != 2 {
		t.Fatalf("requests %d stderr %s", n.Load(), errOut.String())
	}
	if strings.Count(out.String(), "session 7 run") != 1 || !strings.Contains(out.String(), "state: completed (turn 3 revision 1)") {
		t.Fatalf("stdout %s", out.String())
	}
}

// Heartbeats keep an https follow open through an idle period longer than
// the client's silence limit (#398 removed the short total timeout).
func TestReadFollowStaysConnectedOverHTTPSWhileIdle(t *testing.T) {
	fastReconnect(t)
	followIdle = 200 * time.Millisecond
	var n atomic.Int32
	hs := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: session\ndata: {\"session_id\":7,\"kind\":\"run\",\"prompt\":\"p\"}\n\n")
		w.(http.Flusher).Flush()
		for range 12 {
			time.Sleep(50 * time.Millisecond)
			_, _ = io.WriteString(w, ": keepalive\n\n")
			w.(http.Flusher).Flush()
		}
		_, _ = fmt.Fprint(w, "event: end\ndata: {\"state\":\"completed\"}\n\n")
	}))
	t.Cleanup(hs.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: hs.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUSUI_PLANE_CA", ca)
	var out, errOut bytes.Buffer
	if code := readMain([]string{"-follow", "-url", hs.URL, "-token", "op-tok", "7"}, &out, &errOut); code != 0 {
		t.Fatalf("follow %d %s", code, errOut.String())
	}
	if n.Load() != 1 || errOut.Len() != 0 {
		t.Fatalf("requests %d stderr %s", n.Load(), errOut.String())
	}
}
