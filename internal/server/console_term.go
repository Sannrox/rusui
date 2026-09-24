package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

const terminalLeaseTTL = engine.GrantTTL
const terminalGenerationHeader = "X-Rusui-Terminal-Generation"

type termSess struct {
	mu     sync.Mutex
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stop   func()
	buf    []byte
	handle string
}

func (s *Server) consoleTerminal(w http.ResponseWriter, r *http.Request) {
	if !s.consoleRequire(w, r) {
		return
	}
	sess, envRow, err := s.termSessionEnv(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	notice := r.URL.Query().Get("notice")
	lease, held, _ := store.GetTerminalLease(s.Eng.Store, envRow.ID)
	write := held && time.Now().UTC().Before(lease.ExpiresAt)
	gen := 0
	if write {
		gen = lease.Generation
	}
	state := envRow.State
	if envRow.Handle == "" {
		notice = "environment handle missing"
	} else if envRow.State == store.EnvExpired {
		notice = "environment expired"
	} else if envRow.State == store.EnvSleeping {
		notice = "environment sleeping"
	}
	s.renderConsole(w, consolePage{
		Title: "Terminal", View: "terminal", Authed: true, CSRF: s.consoleCSRF(),
		Sess: sess, Notice: notice, TermHandle: envRow.Handle, TermDriver: envRow.Driver,
		TermState: state, TermWrite: write, TermGen: gen,
	})
}

func (s *Server) consoleTermLease(w http.ResponseWriter, r *http.Request) {
	if !s.consoleRequire(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil || r.FormValue("csrf") != s.consoleCSRF() {
		http.Error(w, "csrf", http.StatusForbidden)
		return
	}
	sess, envRow, err := s.termSessionEnv(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if envRow.Handle == "" || envRow.State != store.EnvReady {
		http.Error(w, "environment not ready", http.StatusConflict)
		return
	}
	lease, held, err := store.GetTerminalLease(s.Eng.Store, envRow.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if held && time.Now().UTC().Before(lease.ExpiresAt) {
		_ = store.InsertTerminalAccess(s.Eng.Store, envRow.ID, sess.ID, "refuse", "write lease held")
		http.Error(w, "write lease held", http.StatusConflict)
		return
	}
	gen := 1
	if held {
		gen = lease.Generation + 1
		s.stopTerm(envRow.ID)
	}
	if err := s.ensureTerm(envRow); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err := store.PutTerminalLease(s.Eng.Store, store.TerminalLease{
		EnvironmentID: envRow.ID, SessionID: sess.ID, Generation: gen,
		ExpiresAt: time.Now().UTC().Add(terminalLeaseTTL),
	}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set(terminalGenerationHeader, strconv.Itoa(gen))
	_ = store.InsertTerminalAccess(s.Eng.Store, envRow.ID, sess.ID, "acquire", envRow.Handle)
	http.Redirect(w, r, "/console/sessions/"+strconv.FormatInt(sess.ID, 10)+"/terminal", http.StatusSeeOther)
}

func (s *Server) consoleTermRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.consoleRequire(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil || r.FormValue("csrf") != s.consoleCSRF() {
		http.Error(w, "csrf", http.StatusForbidden)
		return
	}
	sess, envRow, err := s.termSessionEnv(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	want, err := strconv.Atoi(r.FormValue("generation"))
	if err != nil || want < 1 {
		http.Error(w, "generation", http.StatusBadRequest)
		return
	}
	lease, held, err := store.GetTerminalLease(s.Eng.Store, envRow.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !held || lease.SessionID != sess.ID || lease.Generation != want {
		_ = store.InsertTerminalAccess(s.Eng.Store, envRow.ID, sess.ID, "refuse", "stale generation")
		http.Error(w, "stale generation", http.StatusConflict)
		return
	}
	s.stopTerm(envRow.ID)
	lease.ExpiresAt = time.Now().UTC().Add(-time.Second)
	_ = store.PutTerminalLease(s.Eng.Store, *lease)
	_ = store.InsertTerminalAccess(s.Eng.Store, envRow.ID, sess.ID, "revoke", "")
	http.Redirect(w, r, "/console/sessions/"+strconv.FormatInt(sess.ID, 10)+"/terminal", http.StatusSeeOther)
}

func (s *Server) consoleTermInput(w http.ResponseWriter, r *http.Request) {
	if !s.consoleRequire(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil || r.FormValue("csrf") != s.consoleCSRF() {
		http.Error(w, "csrf", http.StatusForbidden)
		return
	}
	sess, envRow, err := s.termSessionEnv(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if envRow.Handle == "" || envRow.State != store.EnvReady {
		http.Error(w, "environment not ready", http.StatusConflict)
		return
	}
	lease, held, err := store.GetTerminalLease(s.Eng.Store, envRow.ID)
	if err != nil || !held || time.Now().UTC().After(lease.ExpiresAt) {
		http.Error(w, "no write lease", http.StatusConflict)
		return
	}
	want, _ := strconv.Atoi(r.FormValue("generation"))
	if want != lease.Generation {
		http.Error(w, "stale generation", http.StatusConflict)
		return
	}
	data := r.FormValue("data")
	if data == "" {
		var body struct {
			Data string `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		data = body.Data
	}
	if !strings.HasSuffix(data, "\n") {
		data += "\n"
	}
	s.termMu.Lock()
	ts := s.terms[envRow.ID]
	s.termMu.Unlock()
	if ts == nil || ts.stdin == nil {
		http.Error(w, "terminal closed", http.StatusConflict)
		return
	}
	if _, err := io.WriteString(ts.stdin, data); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	lease.ExpiresAt = time.Now().UTC().Add(terminalLeaseTTL)
	_ = store.PutTerminalLease(s.Eng.Store, *lease)
	_ = store.InsertTerminalAccess(s.Eng.Store, envRow.ID, sess.ID, "input", "")
	if r.Header.Get("Accept") == "application/json" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/console/sessions/"+strconv.FormatInt(sess.ID, 10)+"/terminal", http.StatusSeeOther)
}

func (s *Server) consoleTermOutput(w http.ResponseWriter, r *http.Request) {
	if !s.consoleRequire(w, r) {
		return
	}
	_, envRow, err := s.termSessionEnv(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if envRow.Handle == "" || envRow.State != store.EnvReady {
		http.Error(w, "environment not ready", http.StatusConflict)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	s.termMu.Lock()
	ts := s.terms[envRow.ID]
	s.termMu.Unlock()
	off := 0
	if ts != nil {
		ts.mu.Lock()
		if len(ts.buf) > 0 {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", jsonEscape(string(ts.buf)))
			fl.Flush()
			off = len(ts.buf)
		}
		ts.mu.Unlock()
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			s.termMu.Lock()
			ts = s.terms[envRow.ID]
			s.termMu.Unlock()
			if ts == nil {
				_, _ = fmt.Fprintf(w, "data: \n\n")
				fl.Flush()
				continue
			}
			ts.mu.Lock()
			if len(ts.buf) > off {
				chunk := ts.buf[off:]
				off = len(ts.buf)
				ts.mu.Unlock()
				_, _ = fmt.Fprintf(w, "data: %s\n\n", jsonEscape(string(chunk)))
				fl.Flush()
			} else {
				ts.mu.Unlock()
			}
		}
	}
}

func (s *Server) termSessionEnv(r *http.Request) (*store.Session, *store.Environment, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("id")
	}
	sess, err := store.GetSession(s.Eng.Store, id)
	if err != nil {
		return nil, nil, fmt.Errorf("not found")
	}
	envRow, err := store.GetEnvironment(s.Eng.Store, sess.EnvironmentID)
	if err != nil {
		return nil, nil, fmt.Errorf("environment missing")
	}
	return sess, envRow, nil
}

func (s *Server) ensureTerm(envRow *store.Environment) error {
	s.termMu.Lock()
	if s.terms == nil {
		s.terms = map[int64]*termSess{}
	}
	if ts, ok := s.terms[envRow.ID]; ok && ts.handle == envRow.Handle {
		s.termMu.Unlock()
		return nil
	}
	s.termMu.Unlock()
	s.stopTerm(envRow.ID)
	stdin, stdout, stop, err := s.openShell(envRow)
	if err != nil {
		return err
	}
	ts := &termSess{stdin: stdin, stdout: stdout, stop: stop, handle: envRow.Handle}
	go ts.pump()
	s.termMu.Lock()
	s.terms[envRow.ID] = ts
	s.termMu.Unlock()
	return nil
}

func (s *Server) stopTerm(envID int64) {
	s.termMu.Lock()
	ts := s.terms[envID]
	delete(s.terms, envID)
	s.termMu.Unlock()
	if ts != nil && ts.stop != nil {
		ts.stop()
	}
}

func (s *Server) openShell(envRow *store.Environment) (io.WriteCloser, io.ReadCloser, func(), error) {
	argv := []string{"/bin/sh"}
	clean := []string{"TERM=xterm", "HOME=/workspace", "PATH=/usr/bin:/bin"}
	if envRow.Driver == env.KindContainer {
		c, ok := s.Eng.Container.(env.Container)
		if !ok || c.RT == nil {
			return nil, nil, nil, fmt.Errorf("container driver required")
		}
		return c.ExecStdio(envRow.Handle, argv, clean)
	}
	if envRow.Handle == "" {
		return nil, nil, nil, fmt.Errorf("workspace missing")
	}
	cmd := exec.Command(argv[0])
	cmd.Dir = envRow.Handle
	cmd.Env = []string{"TERM=xterm", "HOME=" + envRow.Handle, "PATH=/usr/bin:/bin"}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	stop := func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}
	return stdin, stdout, stop, nil
}

func (t *termSess) pump() {
	buf := make([]byte, 1024)
	for {
		n, err := t.stdout.Read(buf)
		if n > 0 {
			t.mu.Lock()
			t.buf = append(t.buf, buf[:n]...)
			if len(t.buf) > 8192 {
				t.buf = t.buf[len(t.buf)-8192:]
			}
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return s
}
