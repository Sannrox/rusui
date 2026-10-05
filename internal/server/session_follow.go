package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

// followPage bounds one transcript query of a followed read; a long
// backlog is sent as several pages instead of one result set.
const followPage = 256

var (
	// followPoll is the poll interval after a change; while nothing moves
	// it doubles up to followIdlePoll.
	followPoll      = 250 * time.Millisecond
	followIdlePoll  = 2 * time.Second
	followHeartbeat = 15 * time.Second
)

// followStateReads counts the full state reads of followed reads, so the
// cost of an idle follower is measurable.
var followStateReads atomic.Int64

// Followed session states. Completed, failed, and cancelled end the
// stream; the others say what the session is doing now.
const (
	followOpen      = "open"
	followQueued    = "queued"
	followRunning   = "running"
	followWaiting   = "waiting"
	followCompleted = "completed"
	followFailed    = "failed"
	followCancelled = "cancelled"
)

type followSession struct {
	SessionID int64  `json:"session_id"`
	Kind      string `json:"kind"`
	Prompt    string `json:"prompt"`
}

type followEntry struct {
	Seq  int64  `json:"seq"`
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Body string `json:"body"`
}

type followStatus struct {
	State    string `json:"state"`
	TurnID   int64  `json:"turn_id,omitempty"`
	Revision int    `json:"revision,omitempty"`
}

// Turn record states. A turn record is a machine-readable notice that a
// turn needs the operator or has finished; it is sent next to the state,
// never instead of it (#491).
const (
	turnReady   = "ready"
	turnWaiting = "waiting"
	turnBlocked = "blocked"
)

// followTurn is the turn record of a followed read. ApprovalSeq is the
// transcript seq of the approval a waiting turn needs answered.
type followTurn struct {
	SessionID   int64  `json:"session_id"`
	TurnID      int64  `json:"turn_id"`
	State       string `json:"state"`
	Revision    int    `json:"revision,omitempty"`
	ApprovalSeq int64  `json:"approval_seq,omitempty"`
}

func (f followStatus) done() bool {
	return f.State == followCompleted || f.State == followFailed || f.State == followCancelled
}

// followSessionRead streams what a plain read shows of the transcript and
// then keeps streaming: every entry appended after the after cursor, in
// append order, the session state when it changes, and after it the turn
// record when that changes (followTurnRecord). A client that
// reconnects with the seq of the last entry it received resumes without
// a gap or a repeat. The stream ends after a completed, failed, or
// cancelled state. It never carries terminal bytes or the workspace diff.
func (s *Server) followSessionRead(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorBrowserOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	var after int64
	if v := r.URL.Query().Get("after"); v != "" {
		after, err = strconv.ParseInt(v, 10, 64)
		if err != nil || after < 0 {
			http.Error(w, "after", http.StatusBadRequest)
			return
		}
	}
	sess, err := store.GetSession(s.Eng.Store, id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess.Kind == store.SessionKindLocal {
		first, err := store.ListTranscriptAfter(s.Eng.Store, id, 0, 1)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(first) == 0 {
			http.Error(w, errNoDurableTranscript.Error(), http.StatusConflict)
			return
		}
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	writeSSE(w, "session", followSession{SessionID: sess.ID, Kind: sess.Kind, Prompt: sess.Prompt})
	fl.Flush()

	var last followStatus
	var lastRecord followTurn
	lastWrite := time.Now()
	lastToken := ""
	wait := followPoll
	timer := time.NewTimer(0)
	defer timer.Stop()
	first := true
	for {
		if !first {
			select {
			case <-r.Context().Done():
				return
			case <-timer.C:
			}
		}
		first = false
		// One cheap query says whether anything moved; the state and the
		// transcript are read only then (#439).
		token, err := store.FollowToken(s.Eng.Store, id)
		if err != nil {
			return
		}
		if token == lastToken {
			if time.Since(lastWrite) >= followHeartbeat {
				_, _ = fmt.Fprint(w, ": keepalive\n\n")
				fl.Flush()
				lastWrite = time.Now()
			}
			wait = min(2*wait, followIdlePoll)
			timer.Reset(wait)
			continue
		}
		lastToken = token
		wait = followPoll
		// The state is read before the entries: everything recorded before
		// a session finished is sent before the state that ends the stream.
		status, err := s.followState(id)
		if err != nil {
			return
		}
		for {
			page, err := store.ListTranscriptAfter(s.Eng.Store, id, after, followPage)
			if err != nil {
				return
			}
			for _, e := range page {
				writeSSEID(w, e.Seq, "entry", followEntry{Seq: e.Seq, ID: e.ID, Kind: e.Type, Body: e.Body})
				after = e.Seq
			}
			if len(page) > 0 {
				fl.Flush()
				lastWrite = time.Now()
			}
			if len(page) < followPage {
				break
			}
		}
		if status != last {
			last = status
			writeSSE(w, "state", status)
			fl.Flush()
			lastWrite = time.Now()
		}
		record, ok, err := s.followTurnRecord(sess.ID, status)
		if err != nil {
			return
		}
		if ok && record != lastRecord {
			lastRecord = record
			writeSSE(w, "turn", record)
			fl.Flush()
			lastWrite = time.Now()
		}
		if status.done() {
			writeSSE(w, "end", status)
			fl.Flush()
			return
		}
		if time.Since(lastWrite) >= followHeartbeat {
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
			lastWrite = time.Now()
		}
		timer.Reset(wait)
	}
}

// followState is the session state a followed read reports. A pending
// approval is waiting even while its turn is leased. Without turns, as
// for a local session, the session stays open until it is cancelled.
func (s *Server) followState(id int64) (followStatus, error) {
	followStateReads.Add(1)
	cancelled, err := store.SessionCancelled(s.Eng.Store, id)
	if err != nil {
		return followStatus{}, err
	}
	turns, err := store.ListTurnsForSession(s.Eng.Store, id)
	if err != nil {
		return followStatus{}, err
	}
	var out followStatus
	if len(turns) > 0 {
		t := turns[len(turns)-1]
		out = followStatus{TurnID: t.ID, Revision: t.PendingRevision}
	}
	if cancelled {
		out.State = followCancelled
		return out, nil
	}
	pending, err := store.CountPendingApprovals(s.Eng.Store, id)
	if err != nil {
		return followStatus{}, err
	}
	if pending > 0 {
		out.State = followWaiting
		return out, nil
	}
	for _, t := range turns {
		switch t.State {
		case "queued":
			out.State = followQueued
		case "leased":
			out = followStatus{State: followRunning, TurnID: t.ID, Revision: t.PendingRevision}
			return out, nil
		}
	}
	if out.State != "" {
		return out, nil
	}
	switch {
	case len(turns) == 0:
		out.State = followOpen
	case turns[len(turns)-1].State == "failed":
		out.State = followFailed
	case turns[len(turns)-1].State == "completed":
		out.State = followCompleted
	default:
		out.State = turns[len(turns)-1].State
	}
	return out, nil
}

// followTurnRecord is the turn record for a followed state, if it has
// one. A waiting state names its oldest pending approval, so each approval
// request gives one record. A completed state is blocked when the turn's
// newest review has a blocked verdict or a result the plane recorded as
// blocked, and ready otherwise. Queued, running, open, failed, and
// cancelled states have no record. The record depends only on durable
// state, so a reconnect resends the current record unchanged and a client
// drops it as a repeat.
func (s *Server) followTurnRecord(sessionID int64, status followStatus) (followTurn, bool, error) {
	switch status.State {
	case followWaiting:
		seq, turnID, revision, ok, err := store.OldestPendingApproval(s.Eng.Store, sessionID)
		if err != nil || !ok {
			return followTurn{}, false, err
		}
		return followTurn{SessionID: sessionID, TurnID: turnID, State: turnWaiting, Revision: revision, ApprovalSeq: seq}, true, nil
	case followCompleted:
		out := followTurn{SessionID: sessionID, TurnID: status.TurnID, State: turnReady, Revision: status.Revision}
		payload, err := store.LatestReviewJSON(s.Eng.Store, status.TurnID)
		if errors.Is(err, sql.ErrNoRows) {
			return out, true, nil
		}
		if err != nil {
			return followTurn{}, false, err
		}
		var art engine.Artifact
		if json.Unmarshal([]byte(payload), &art) == nil && artifactBlocked(art) {
			out.State = turnBlocked
		}
		return out, true, nil
	}
	return followTurn{}, false, nil
}

// artifactBlocked reads a turn's result outcome when it has one: a run
// artifact carries verdict blocked even when it published, so only a
// review artifact is judged by its verdict.
func artifactBlocked(art engine.Artifact) bool {
	if art.Result != nil {
		return art.Result.Outcome == engine.OutcomeBlocked
	}
	return art.Verdict == turnBlocked
}

func writeSSEID(w http.ResponseWriter, id int64, event string, v any) {
	_, _ = fmt.Fprintf(w, "id: %d\n", id)
	writeSSE(w, event, v)
}
