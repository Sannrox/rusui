package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

// scriptedFollow serves one fixed event list per connection, in order,
// and ends each response after its list.
func scriptedFollow(t *testing.T, conns ...[]string) string {
	t.Helper()
	var n atomic.Int32
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		if i >= len(conns) {
			http.Error(w, "gone", http.StatusGone)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range conns[i] {
			name, data, _ := strings.Cut(ev, " ")
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
		}
	}))
	t.Cleanup(hs.Close)
	return hs.URL
}

const scriptedSession = `session {"session_id":7,"kind":"run","prompt":"secret prompt"}`

func scriptedState(event, state string, turn int) string {
	return fmt.Sprintf(`%s {"state":%q,"turn_id":%d,"revision":1}`, event, state, turn)
}

var statusReport = regexp.MustCompile(`\x1b\]7501;[^\x1b]*\x1b\\`)

func TestReadFollowReportsProgramStatusOncePerChange(t *testing.T) {
	fastReconnect(t)
	url := scriptedFollow(t,
		[]string{scriptedSession, scriptedState("state", "queued", 0), scriptedState("state", "running", 1)},
		// Reconnect: the plane resends the current state, then a second
		// turn runs in the same state.
		[]string{scriptedSession, scriptedState("state", "running", 1), scriptedState("state", "running", 2),
			scriptedState("state", "waiting", 2), scriptedState("end", "completed", 2)},
	)
	var out, errOut bytes.Buffer
	if code := followRead(url, "op-tok", "7", &out, &errOut, true); code != 0 {
		t.Fatalf("follow %d %s", code, errOut.String())
	}
	got := statusReport.FindAllString(out.String(), -1)
	want := []string{statusWorking, statusBlocked, statusDone}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("reports %q, want %q", got, want)
	}
	if strings.Count(out.String(), "\x1b") != len(want)*2 {
		t.Fatalf("stray control bytes in %q", out.String())
	}
}

func TestReadFollowReportsErrorBeforeErrorExit(t *testing.T) {
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "auth", http.StatusUnauthorized)
	}))
	t.Cleanup(refused.Close)
	var out, errOut bytes.Buffer
	if code := followRead(refused.URL, "nope", "7", &out, &errOut, true); code != 1 || out.String() != statusError {
		t.Fatalf("refused %d %q", code, out.String())
	}

	out.Reset()
	url := scriptedFollow(t, []string{scriptedSession, scriptedState("state", "running", 1), scriptedState("end", "failed", 1)})
	if code := followRead(url, "op-tok", "7", &out, &errOut, true); code != 1 {
		t.Fatalf("failed session %d", code)
	}
	if got := statusReport.FindAllString(out.String(), -1); fmt.Sprint(got) != fmt.Sprint([]string{statusWorking, statusError}) {
		t.Fatalf("reports %q", got)
	}
}

func TestReadFollowWritesNoReportOffTerminal(t *testing.T) {
	events := []string{scriptedSession, scriptedState("state", "waiting", 1), scriptedState("end", "completed", 1)}
	var out, errOut bytes.Buffer
	if code := followRead(scriptedFollow(t, events), "op-tok", "7", &out, &errOut, false); code != 0 {
		t.Fatalf("follow %d %s", code, errOut.String())
	}
	want := "session 7 run\nprompt: secret prompt\nstate: waiting (turn 1 revision 1)\nstate: completed (turn 1 revision 1)\n"
	if out.String() != want {
		t.Fatalf("stdout %q, want %q", out.String(), want)
	}
}
