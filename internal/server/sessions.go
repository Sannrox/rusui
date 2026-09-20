package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := store.ListSessions(s.Eng.Store, r.URL.Query().Get("project"), limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if list == nil {
		list = []store.Session{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", 400)
		return
	}
	sess, err := store.GetSession(s.Eng.Store, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	turns, err := store.ListTurnsForSession(s.Eng.Store, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if turns == nil {
		turns = []store.Turn{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"session": sess, "turns": turns})
}

func (s *Server) attachSession(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", 400)
		return
	}
	sess, err := store.GetSession(s.Eng.Store, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	writeSSE(w, "session", sess)
	fl.Flush()
	seen := map[string]string{}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			turns, err := store.ListTurnsForSession(s.Eng.Store, id)
			if err != nil {
				return
			}
			for _, t := range turns {
				key := fmt.Sprintf("turn:%d", t.ID)
				if seen[key] != t.State {
					seen[key] = t.State
					writeSSE(w, "turn", t)
					fl.Flush()
				}
			}
			acts, err := store.ListActionsForSession(s.Eng.Store, id)
			if err != nil {
				return
			}
			for _, a := range acts {
				key := "action:" + a.ID
				if _, ok := seen[key]; !ok {
					seen[key] = a.Type
					writeSSE(w, "action", a)
					fl.Flush()
				}
			}
		}
	}
}

func (s *Server) followUpTurn(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", 400)
		return
	}
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	turnID, pending, err := s.Eng.PromptFollowUp(id, req.Prompt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"turn_id": turnID, "pending_revision": pending})
}

func (s *Server) cancelSession(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", 400)
		return
	}
	if err := s.Eng.CancelSession(id); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func writeSSE(w http.ResponseWriter, event string, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
}
