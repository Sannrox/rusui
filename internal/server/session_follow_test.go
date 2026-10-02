package server

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

type sseEvent struct {
	id, event, data string
}

type sseStream struct {
	t      *testing.T
	cancel context.CancelFunc
	events chan sseEvent
}

func openFollow(t *testing.T, hs *httptest.Server, sid, after int64, token string) (*sseStream, *http.Response) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	url := hs.URL + "/sessions/" + strconv.FormatInt(sid, 10) + "/read/follow?after=" + strconv.FormatInt(after, 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	s := &sseStream{t: t, cancel: cancel, events: make(chan sseEvent, 64)}
	if res.StatusCode != http.StatusOK {
		return s, res
	}
	go func() {
		defer close(s.events)
		defer func() { _ = res.Body.Close() }()
		r := bufio.NewReader(res.Body)
		var ev sseEvent
		for {
			line, err := r.ReadString('\n')
			line = strings.TrimSuffix(line, "\n")
			switch {
			case line == "":
				if ev.event != "" {
					s.events <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, ": "):
				s.events <- sseEvent{event: "comment", data: strings.TrimPrefix(line, ": ")}
			case strings.HasPrefix(line, "id: "):
				ev.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				ev.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			}
			if err != nil {
				return
			}
		}
	}()
	return s, res
}

func (s *sseStream) next() sseEvent {
	s.t.Helper()
	select {
	case ev, ok := <-s.events:
		if !ok {
			s.t.Fatal("stream closed")
		}
		return ev
	case <-time.After(5 * time.Second):
		s.t.Fatal("no event")
	}
	return sseEvent{}
}

// expect skips heartbeats and returns the next event, which must be want.
func (s *sseStream) expect(want string) sseEvent {
	s.t.Helper()
	for {
		ev := s.next()
		if ev.event == "comment" {
			continue
		}
		if ev.event != want {
			s.t.Fatalf("event %q %s, want %q", ev.event, ev.data, want)
		}
		return ev
	}
}

func (s *sseStream) closed() {
	s.t.Helper()
	select {
	case ev, ok := <-s.events:
		if ok {
			s.t.Fatalf("event after end: %+v", ev)
		}
	case <-time.After(5 * time.Second):
		s.t.Fatal("stream stayed open after end")
	}
}

func addEntry(t *testing.T, e *engine.Engine, sid int64, id, typ, body string) {
	t.Helper()
	if err := store.InsertAction(e.Store, store.Action{
		ID: id, SessionID: &sid, Repo: "example/test-repo", Item: 1,
		Type: typ, ReasonCode: acp.ReasonRecorded,
		EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
		Body: body,
	}); err != nil {
		t.Fatal(err)
	}
}

func addApproval(t *testing.T, e *engine.Engine, sid int64, id, reason string) {
	t.Helper()
	turns, err := store.ListTurnsForSession(e.Store, sid)
	if err != nil || len(turns) == 0 {
		t.Fatalf("turns %v %v", turns, err)
	}
	tid := turns[len(turns)-1].ID
	if err := store.InsertAction(e.Store, store.Action{
		ID: id, SessionID: &sid, TurnID: &tid, Repo: "example/test-repo", Item: 1,
		Type: acp.ActionApproval, ReasonCode: reason,
		EvidenceClass: acp.EvidenceObserved, LimitSentence: acp.LimitSentence,
		Body: "may I",
	}); err != nil {
		t.Fatal(err)
	}
}

func setTurns(t *testing.T, e *engine.Engine, sid int64, state string) {
	t.Helper()
	if _, err := e.Store.DB.Exec(`UPDATE turns SET state=?, claimed_revision=pending_revision WHERE session_id=?`, state, sid); err != nil {
		t.Fatal(err)
	}
}

func entryOf(t *testing.T, ev sseEvent) followEntry {
	t.Helper()
	var e followEntry
	if err := json.Unmarshal([]byte(ev.data), &e); err != nil {
		t.Fatal(err)
	}
	if ev.id != strconv.FormatInt(e.Seq, 10) {
		t.Fatalf("event id %q for seq %d", ev.id, e.Seq)
	}
	return e
}

func stateOf(t *testing.T, ev sseEvent) followStatus {
	t.Helper()
	var st followStatus
	if err := json.Unmarshal([]byte(ev.data), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestFollowReadStreamsHistoryThenLiveEventsAndOutcome(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid := createRunSession(t, hs, "first prompt")
	// Random action ids do not sort in append order; the stream must.
	addEntry(t, e, sid, "ff-first", acp.ActionUpdate, "one")
	addEntry(t, e, sid, "00-second", acp.ActionUpdate, "two")

	s, res := openFollow(t, hs, sid, 0, "op-tok")
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("follow %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	var head followSession
	if err := json.Unmarshal([]byte(s.expect("session").data), &head); err != nil || head.SessionID != sid || head.Prompt != "first prompt" {
		t.Fatalf("session %+v %v", head, err)
	}
	one, two := entryOf(t, s.expect("entry")), entryOf(t, s.expect("entry"))
	if one.Body != "one" || two.Body != "two" || one.Seq >= two.Seq {
		t.Fatalf("history %+v %+v", one, two)
	}
	if st := stateOf(t, s.expect("state")); st.State != followQueued || st.TurnID == 0 {
		t.Fatalf("state %+v", st)
	}

	setTurns(t, e, sid, "leased")
	if st := stateOf(t, s.expect("state")); st.State != followRunning {
		t.Fatalf("running %+v", st)
	}
	addApproval(t, e, sid, "approve-1", acp.ReasonUnmatched)
	if got := entryOf(t, s.expect("entry")); got.Kind != acp.ActionApproval {
		t.Fatalf("approval entry %+v", got)
	}
	if st := stateOf(t, s.expect("state")); st.State != followWaiting {
		t.Fatalf("waiting %+v", st)
	}
	if err := store.PutApprovalDecision(e.Store, "approve-1", "allow"); err != nil {
		t.Fatal(err)
	}
	if st := stateOf(t, s.expect("state")); st.State != followRunning {
		t.Fatalf("approved %+v", st)
	}

	// The last entry and the completion land between two polls; the entry
	// still comes first.
	addEntry(t, e, sid, "11-last", acp.ActionUpdate, "three")
	setTurns(t, e, sid, "completed")
	if got := entryOf(t, s.expect("entry")); got.Body != "three" {
		t.Fatalf("last entry %+v", got)
	}
	if st := stateOf(t, s.expect("state")); st.State != followCompleted {
		t.Fatalf("completed %+v", st)
	}
	if st := stateOf(t, s.expect("end")); st.State != followCompleted {
		t.Fatalf("end %+v", st)
	}
	s.closed()
}

func TestFollowReadResumesAfterCursorThroughCompletion(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid := createRunSession(t, hs, "p")
	addEntry(t, e, sid, "a", acp.ActionUpdate, "seen")

	s, _ := openFollow(t, hs, sid, 0, "op-tok")
	s.expect("session")
	seen := entryOf(t, s.expect("entry"))
	s.expect("state")
	s.cancel()

	// Recorded while no client was connected, including the completion.
	addEntry(t, e, sid, "b", acp.ActionUpdate, "gap one")
	addEntry(t, e, sid, "c", acp.ActionUpdate, "gap two")
	setTurns(t, e, sid, "completed")

	again, _ := openFollow(t, hs, sid, seen.Seq, "op-tok")
	again.expect("session")
	if got := entryOf(t, again.expect("entry")); got.Body != "gap one" {
		t.Fatalf("first gap entry %+v", got)
	}
	if got := entryOf(t, again.expect("entry")); got.Body != "gap two" {
		t.Fatalf("second gap entry %+v", got)
	}
	if st := stateOf(t, again.expect("state")); st.State != followCompleted {
		t.Fatalf("state %+v", st)
	}
	again.expect("end")
	again.closed()
}

func TestFollowReadEndsOnFailureAndCancellation(t *testing.T) {
	_, hs, e := consoleEnv(t)
	failed := createRunSession(t, hs, "fails")
	setTurns(t, e, failed, "failed")
	s, _ := openFollow(t, hs, failed, 0, "op-tok")
	s.expect("session")
	if st := stateOf(t, s.expect("state")); st.State != followFailed {
		t.Fatalf("failed %+v", st)
	}
	s.expect("end")
	s.closed()

	cancelled := createRunSession(t, hs, "cancel me")
	s, _ = openFollow(t, hs, cancelled, 0, "op-tok")
	s.expect("session")
	s.expect("state")
	if err := e.CancelSession(cancelled); err != nil {
		t.Fatal(err)
	}
	if st := stateOf(t, s.expect("state")); st.State != followCancelled {
		t.Fatalf("cancelled %+v", st)
	}
	s.expect("end")
	s.closed()
}

// A policy denial and a request whose turn ended have no decision row;
// neither keeps the stream waiting after the turn finished.
func TestFollowReadEndsDespiteUnansweredApprovals(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid := createRunSession(t, hs, "p")
	setTurns(t, e, sid, "leased")
	addApproval(t, e, sid, "denied-1", acp.ReasonDenied)
	addApproval(t, e, sid, "ask-1", acp.ReasonUnmatched)
	setTurns(t, e, sid, "completed")
	s, _ := openFollow(t, hs, sid, 0, "op-tok")
	s.expect("session")
	s.expect("entry")
	s.expect("entry")
	if st := stateOf(t, s.expect("state")); st.State != followCompleted {
		t.Fatalf("state %+v", st)
	}
	s.expect("end")
	s.closed()
}

func TestFollowReadSendsHeartbeatsWhileIdle(t *testing.T) {
	prev := followHeartbeat
	followHeartbeat = 50 * time.Millisecond
	t.Cleanup(func() { followHeartbeat = prev })
	_, hs, _ := consoleEnv(t)
	sid := createRunSession(t, hs, "idle")
	s, _ := openFollow(t, hs, sid, 0, "op-tok")
	s.expect("session")
	s.expect("state")
	if ev := s.next(); ev.event != "comment" || ev.data != "keepalive" {
		t.Fatalf("heartbeat %+v", ev)
	}
}

func TestFollowReadExplicitErrors(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid := createRunSession(t, hs, "p")
	for name, tc := range map[string]struct {
		path, token string
		code        int
	}{
		"missing session": {"/sessions/999999/read/follow", "op-tok", http.StatusNotFound},
		"worker token":    {"/sessions/" + strconv.FormatInt(sid, 10) + "/read/follow", "wsec", http.StatusUnauthorized},
		"bad token":       {"/sessions/" + strconv.FormatInt(sid, 10) + "/read/follow", "nope", http.StatusUnauthorized},
		"bad cursor":      {"/sessions/" + strconv.FormatInt(sid, 10) + "/read/follow?after=-1", "op-tok", http.StatusBadRequest},
	} {
		if got := authedGet(t, hs, tc.path, tc.token); got.code != tc.code {
			t.Errorf("%s: %d %s", name, got.code, got.body)
		}
	}

	var local int64
	if err := e.Store.Tx(func(tx *sql.Tx) error {
		var err error
		local, err = store.InsertLocalSessionTx(tx, "test", time.Now().UTC())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := authedGet(t, hs, "/sessions/"+strconv.FormatInt(local, 10)+"/read/follow", "op-tok"); got.code != http.StatusConflict || !strings.Contains(got.body, errNoDurableTranscript.Error()) {
		t.Fatalf("local %d %s", got.code, got.body)
	}
}

// A turn action that arrives after the turn completed is refused, so a
// followed read that ended has shown every recorded entry (#437).
func TestFollowReadEndIsNotAheadOfLateActions(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid := createRunSession(t, hs, "p")
	turns, err := store.ListTurnsForSession(e.Store, sid)
	if err != nil || len(turns) != 1 {
		t.Fatalf("turns %v %v", turns, err)
	}
	tid := turns[0].ID
	post := func(body string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, hs.URL+"/turns/"+strconv.FormatInt(tid, 10)+"/actions", strings.NewReader(`{"type":"acp.update","body":{"text":"`+body+`"}}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer wsec")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	setTurns(t, e, sid, "leased")
	if code := post("while running"); code >= 300 {
		t.Fatalf("leased ingest %d", code)
	}
	setTurns(t, e, sid, "completed")
	if code := post("too late"); code != http.StatusConflict {
		t.Fatalf("late ingest %d, want 409", code)
	}
	s, _ := openFollow(t, hs, sid, 0, "op-tok")
	s.expect("session")
	if got := entryOf(t, s.expect("entry")); !strings.Contains(got.Body, "while running") {
		t.Fatalf("entry %+v", got)
	}
	s.expect("state")
	s.expect("end")
	s.closed()
	plain := authedGet(t, hs, "/sessions/"+strconv.FormatInt(sid, 10)+"/read", "op-tok")
	if strings.Contains(plain.body, "too late") {
		t.Fatalf("plain read shows a refused action: %s", plain.body)
	}
}
