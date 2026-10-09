package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

// errNoDurableTranscript is the local-session read when the plane has no
// stored transcript to show. The caller must not open a terminal instead.
var errNoDurableTranscript = errors.New("local session has no durable transcript")

type sessionRead struct {
	SessionID       int64              `json:"session_id"`
	Kind            string             `json:"kind"`
	Prompt          string             `json:"prompt"`
	Transcript      []sessionReadEntry `json:"transcript"`
	TranscriptState string             `json:"transcript_state,omitempty"`
	Diff            string             `json:"diff"`
	DiffState       string             `json:"diff_state,omitempty"`
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
	view, err := s.sessionRead(id)
	if errors.Is(err, sql.ErrNoRows) {
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
func (s *Server) sessionRead(id int64) (sessionRead, error) {
	page, err := s.sessionPage(id)
	if err != nil {
		return sessionRead{}, err
	}
	out := sessionRead{
		SessionID:  page.Sess.ID,
		Kind:       page.Sess.Kind,
		Prompt:     page.Sess.Prompt,
		Transcript: []sessionReadEntry{},
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
	handle, envState := "", ""
	envRow, err := store.GetEnvironment(s.Eng.Store, page.Sess.EnvironmentID)
	if err == nil {
		handle, envState = envRow.Handle, envRow.State
	} else {
		envRow = nil
	}
	out.Diff, out.DiffState = s.workspaceDiff(page, envRow, handle, envState)
	return out, nil
}

// workspaceDiff is the console file diff for each top-level workspace file:
// the file text as a unified patch from an empty baseline. It is not a git
// diff. No files is an empty diff. A workspace the console cannot list is
// the console's unavailable state.
// sessionDiffCap bounds the bodies one container session read carries.
const sessionDiffCap = 4 << 20

func (s *Server) workspaceDiff(page consolePage, envRow *store.Environment, handle, envState string) (string, string) {
	if page.FilesErr != "" {
		return "", page.FilesErr
	}
	if envRow != nil && envRow.Driver == env.KindContainer {
		return s.containerDiff(envRow)
	}
	if len(page.Files) == 0 {
		return "", "empty"
	}
	var b strings.Builder
	for _, file := range page.Files {
		body, state := s.inspectFile(envRow, handle, envState, file.Name, true)
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		if state != "" {
			fmt.Fprintf(&b, "# %s\n%s\n", file.Name, state)
			continue
		}
		b.WriteString(body)
	}
	return b.String(), ""
}

// containerDiff is the workspace diff of a ready container from one
// runtime exec, not one per file (#449).
func (s *Server) containerDiff(envRow *store.Environment) (string, string) {
	in, ok := s.containerInspector()
	if !ok {
		return "", "workspace unavailable"
	}
	entries, err := in.WorkspaceSnapshot(envRow.Handle, consoleFileCap, sessionDiffCap)
	if errors.Is(err, env.ErrWorkspaceReplaced) {
		return "", env.FileRootReplaced
	}
	if err != nil {
		return "", "workspace unavailable"
	}
	if len(entries) == 0 {
		return "", "empty"
	}
	var b strings.Builder
	for _, e := range entries {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		state := e.State
		if state == "" && (bytes.IndexByte(e.Body, 0) >= 0 || !utf8.Valid(e.Body)) {
			state = "binary"
		}
		if state != "" {
			fmt.Fprintf(&b, "# %s\n%s\n", e.Name, state)
			continue
		}
		b.WriteString(unifiedFromEmpty(e.Name, string(e.Body)))
	}
	return b.String(), ""
}
