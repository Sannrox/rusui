package server

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

// followPage bounds one transcript query of a followed read; a long
// backlog is sent as several pages instead of one result set.
const followPage = 256

var (
	followPoll      = 250 * time.Millisecond
	followHeartbeat = 15 * time.Second
)

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

func (f followStatus) done() bool {
	return f.State == followCompleted || f.State == followFailed || f.State == followCancelled
}

// followSessionRead streams what a plain read shows of the transcript and
// then keeps streaming: every entry appended after the after cursor, in
// append order, and the session state when it changes. A client that
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
	lastWrite := time.Now()
	tick := time.NewTicker(followPoll)
	defer tick.Stop()
	for {
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
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

// followState is the session state a followed read reports. A pending
// approval is waiting even while its turn is leased. Without turns, as
// for a local session, the session stays open until it is cancelled.
func (s *Server) followState(id int64) (followStatus, error) {
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

func writeSSEID(w http.ResponseWriter, id int64, event string, v any) {
	_, _ = fmt.Fprintf(w, "id: %d\n", id)
	writeSSE(w, event, v)
}
