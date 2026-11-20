package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

// errNoDurableTranscript is the local-session read when the plane has no
// stored transcript to show. The caller must not open a terminal instead.
var errNoDurableTranscript = errors.New("local session has no durable transcript")

type sessionRead struct {
	SessionID       int64                      `json:"session_id"`
	Kind            string                     `json:"kind"`
	Prompt          string                     `json:"prompt"`
	Transcript      []sessionReadEntry         `json:"transcript"`
	TranscriptState string                     `json:"transcript_state,omitempty"`
	Diff            string                     `json:"diff"`
	DiffState       string                     `json:"diff_state,omitempty"`
	Services        []store.EnvironmentService `json:"services"`
	ServicesError   string                     `json:"services_error,omitempty"`
}

type sessionReadEntry struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Body string `json:"body"`
}

func (s *Server) readSession(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorAPIOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	view, err := s.sessionRead(r, id)
	if errors.Is(err, store.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, errNoDurableTranscript) {
		http.Error(w, errNoDurableTranscript.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(view)
}

// sessionRead is the session the console shows: durable transcript entries
// and the current workspace diff. It does not open a terminal.
func (s *Server) sessionRead(r *http.Request, id int64) (sessionRead, error) {
	page, err := s.sessionPage(r, id)
	if err != nil {
		return sessionRead{}, err
	}
	out := sessionRead{
		SessionID:  page.Sess.ID,
		Kind:       page.Sess.Kind,
		Prompt:     page.Sess.Prompt,
		Transcript: []sessionReadEntry{},
		Services:   []store.EnvironmentService{},
	}
	if svcSet, err := store.ListSessionServices(s.Eng.Store, id); err == nil {
		out.Services = svcSet.Services
		out.ServicesError = svcSet.ParseError
	}
	if page.HistoryUnavailable {
		out.TranscriptState = "unavailable"
	} else {
		for _, entry := range page.Entries {
			out.Transcript = append(out.Transcript, sessionReadEntry(entry))
		}
	}
	if page.Sess.Kind == store.SessionKindLocal && out.TranscriptState == "" && len(out.Transcript) == 0 {
		return sessionRead{}, errNoDurableTranscript
	}
	handle := ""
	envRow, err := store.GetEnvironment(s.Eng.Store, page.Sess.EnvironmentID)
	if err == nil {
		handle = envRow.Handle
	} else {
		envRow = nil
	}
	out.Diff, out.DiffState = s.workspaceDiff(page, envRow, handle)
	return out, nil
}

// workspaceDiff is git status and a bounded git diff against the workspace
// pin (HEAD). A clean tree, a missing pin, and a workspace the console
// cannot list are stated. Sleeping guests are not woken.
// sessionDiffCap bounds the git output one session read carries.
const sessionDiffCap = 4 << 20

func (s *Server) workspaceDiff(page consolePage, envRow *store.Environment, handle string) (string, string) {
	if page.FilesErr != "" {
		return "", page.FilesErr
	}
	if envRow != nil && envRow.Driver == env.KindContainer {
		return s.containerDiff(envRow)
	}
	body, state, err := env.GitDiff(handle, sessionDiffCap)
	if err != nil {
		return "", "workspace unavailable"
	}
	return string(body), state
}

// containerDiff is the workspace git dirt of a ready container from one
// runtime exec (#449, #792).
func (s *Server) containerDiff(envRow *store.Environment) (string, string) {
	if envRow.State != store.EnvReady {
		return "", "workspace " + envRow.State
	}
	in, ok := s.containerInspector()
	if !ok {
		return "", "workspace unavailable"
	}
	body, state, err := in.WorkspaceGitDiff(envRow.Handle, sessionDiffCap)
	if errors.Is(err, env.ErrWorkspaceReplaced) {
		return "", env.FileRootReplaced
	}
	if err != nil {
		return "", "workspace unavailable"
	}
	return string(body), state
}
