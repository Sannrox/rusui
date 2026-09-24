package server

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
	"github.com/sannrox/rusui/internal/sumika"
)

func TestLocalRuntimeHTTPAndSumikaLifecycle(t *testing.T) {
	_, hs, e := consoleEnv(t)
	cwd := t.TempDir()
	rawPolicy := fmt.Sprintf(`
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
projects:
  test:
    session_kinds: [local]
    local_runtime:
      argv: ["/bin/sh", "-i"]
      cwd: %q
    repos: {}
`, cwd)
	pol, err := policy.Parse([]byte(rawPolicy))
	if err != nil {
		t.Fatal(err)
	}
	e.ReloadPolicy(pol)
	socketDir, err := os.MkdirTemp("/tmp", "rs-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "s.sock")
	t.Setenv("SUMIKA_SOCK", socketPath)
	daemon := newLocalFakeDaemon(t, socketPath)
	var unstartedID int64
	if err := e.Store.Tx(func(tx *sql.Tx) error {
		var err error
		unstartedID, err = store.InsertLocalSessionTx(tx, "test", time.Now().UTC())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.AttachLocalSession(unstartedID); !errors.Is(err, store.ErrProcessNotAttachable) {
		t.Fatalf("attach before first Process reservation returned %v", err)
	}

	response, body := localRequest(t, hs, http.MethodPost, "/projects/test/sessions", `{"kind":"local"}`, "first")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create local session: %d %s", response.StatusCode, body)
	}
	var created struct {
		SessionID int64         `json:"session_id"`
		Kind      string        `json:"kind"`
		Process   store.Process `json:"process"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if created.SessionID == 0 || created.Kind != "local" || created.Process.State != store.ProcessRunning || created.Process.Name != fmt.Sprintf("rusui-%d", created.SessionID) {
		t.Fatalf("create response %+v", created)
	}
	if got := daemon.startCount(); got != 1 {
		t.Fatalf("Sumika start count %d", got)
	}
	response, body = localRequest(t, hs, http.MethodPost, "/sessions/"+strconv.FormatInt(created.SessionID, 10)+"/restart", `{}`, "")
	if response.StatusCode != http.StatusConflict || daemon.startCount() != 1 {
		t.Fatalf("active process restart status=%d starts=%d body=%s", response.StatusCode, daemon.startCount(), body)
	}
	response, body = localRequest(t, hs, http.MethodPost, "/projects/test/sessions", `{"kind":"local"}`, "first")
	if response.StatusCode != http.StatusCreated || daemon.startCount() != 1 {
		t.Fatalf("idempotent create: code=%d starts=%d body=%s", response.StatusCode, daemon.startCount(), body)
	}
	response, detail := localRequest(t, hs, http.MethodGet, "/sessions/"+strconv.FormatInt(created.SessionID, 10), "", "")
	if response.StatusCode != http.StatusOK || strings.Contains(strings.ToLower(string(detail)), `"argv"`) || strings.Contains(strings.ToLower(string(detail)), `"cwd"`) || strings.Contains(strings.ToLower(string(detail)), `"identity_hash"`) {
		t.Fatalf("session detail leaked local runtime configuration: %d %s", response.StatusCode, detail)
	}
	response, body = localRequest(t, hs, http.MethodPost, "/projects/test/sessions", `{"kind":"local","argv":["/bin/false"]}`, "bad-argv")
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("request-supplied argv accepted: %d %s", response.StatusCode, body)
	}

	daemon.setStatus(created.Process.Name, sumika.StatusIdle)
	if err := e.ReconcileSumika(); err != nil {
		t.Fatal(err)
	}
	process, err := store.LatestSumikaProcess(e.Store, created.SessionID)
	if err != nil || process.State != store.ProcessIdle {
		t.Fatalf("idle observation %+v: %v", process, err)
	}
	daemon.setStatus(created.Process.Name, sumika.StatusBlocked)
	if err := e.ReconcileSumika(); err != nil {
		t.Fatal(err)
	}
	process, err = store.LatestSumikaProcess(e.Store, created.SessionID)
	if err != nil || process.State != store.ProcessBlocked {
		t.Fatalf("blocked observation %+v: %v", process, err)
	}
	changedCwd := t.TempDir()
	changedPolicyYAML := strings.Replace(rawPolicy, `"/bin/sh"`, `"/bin/false"`, 1)
	changedPolicyYAML = strings.Replace(changedPolicyYAML, cwd, changedCwd, 1)
	changedPolicy, err := policy.Parse([]byte(changedPolicyYAML))
	if err != nil {
		t.Fatal(err)
	}
	e.ReloadPolicy(changedPolicy)
	if err := e.ReconcileSumika(); err != nil {
		t.Fatal(err)
	}
	process, err = store.LatestSumikaProcess(e.Store, created.SessionID)
	if err != nil || process.State != store.ProcessBlocked {
		t.Fatalf("profile reload changed existing process identity: %+v %v", process, err)
	}
	e.ReloadPolicy(pol)

	firstAttach, firstConn, err := e.AttachLocalSession(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if firstAttach.Generation != 1 {
		t.Fatalf("first attach %+v", firstAttach)
	}
	pty := make([]byte, len("pty-ready\n"))
	if _, err := io.ReadFull(firstConn, pty); err != nil || string(pty) != "pty-ready\n" {
		t.Fatalf("raw PTY after protocol response %q: %v", pty, err)
	}
	secondAttach, secondConn, err := e.AttachLocalSession(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	attaches, err := store.ListSumikaAttaches(e.Store, created.Process.ID)
	if err != nil || len(attaches) != 2 || attaches[0].State != store.AttachStolen || attaches[1].State != store.AttachAttached || secondAttach.Generation != 2 {
		t.Fatalf("stolen attach history %+v: %v", attaches, err)
	}
	_ = firstConn.Close()
	_ = secondConn.Close()
	attaches, err = store.ListSumikaAttaches(e.Store, created.Process.ID)
	if err != nil || attaches[1].State != store.AttachDetached {
		t.Fatalf("detached attach history %+v: %v", attaches, err)
	}
	if info := daemon.session(created.Process.Name); info.Status != sumika.StatusBlocked {
		t.Fatalf("detach changed process status: %+v", info)
	}

	_, thirdConn, err := e.AttachLocalSession(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	daemon.setStatus(created.Process.Name, sumika.StatusDead)
	if err := e.ReconcileSumika(); err != nil {
		t.Fatal(err)
	}
	_ = thirdConn.Close()
	process, err = store.LatestSumikaProcess(e.Store, created.SessionID)
	attaches, attachErr := store.ListSumikaAttaches(e.Store, created.Process.ID)
	sess, sessionErr := store.GetSession(e.Store, created.SessionID)
	if err != nil || attachErr != nil || sessionErr != nil || process.State != store.ProcessDead || attaches[2].State != store.AttachProcessExited || sess.State != "open" {
		t.Fatalf("death state process=%+v attaches=%+v session=%+v errors=%v/%v/%v", process, attaches, sess, err, attachErr, sessionErr)
	}

	response, body = localRequest(t, hs, http.MethodPost, "/sessions/"+strconv.FormatInt(created.SessionID, 10)+"/restart", `{}`, "")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("explicit process restart status=%d body=%s", response.StatusCode, body)
	}
	var restarted struct {
		Process store.Process `json:"process"`
	}
	if err := json.Unmarshal(body, &restarted); err != nil || restarted.Process.Generation != 2 || restarted.Process.State != store.ProcessRunning {
		t.Fatalf("explicit process restart %+v: %v", restarted, err)
	}
	daemon.stop()
	if err := e.ReconcileSumika(); err == nil {
		t.Fatal("socket loss was not reported")
	}
	process, err = store.LatestSumikaProcess(e.Store, created.SessionID)
	if err != nil || process.State != store.ProcessUnknown {
		t.Fatalf("socket loss did not become unknown: %+v %v", process, err)
	}
	response, body = localRequest(t, hs, http.MethodPost, "/sessions/"+strconv.FormatInt(created.SessionID, 10)+"/restart", `{}`, "")
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("unknown process restart status=%d body=%s", response.StatusCode, body)
	}

	daemon = newLocalFakeDaemon(t, socketPath)
	if err := e.ReconcileSumika(); err != nil {
		t.Fatal(err)
	}
	process, err = store.LatestSumikaProcess(e.Store, created.SessionID)
	if err != nil || process.State != store.ProcessLost {
		t.Fatalf("daemon restart reconciliation %+v: %v", process, err)
	}
	response, body = localRequest(t, hs, http.MethodPost, "/sessions/"+strconv.FormatInt(created.SessionID, 10)+"/restart", `{}`, "")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("restart after loss status=%d body=%s", response.StatusCode, body)
	}
	var restartedAfterLoss struct {
		Process store.Process `json:"process"`
	}
	if err := json.Unmarshal(body, &restartedAfterLoss); err != nil || restartedAfterLoss.Process.Generation != 3 || restartedAfterLoss.Process.State != store.ProcessRunning {
		t.Fatalf("restart after loss %+v: %v", restartedAfterLoss, err)
	}

	daemon.stop()
	if confirmed, err := e.CancelLocalSession(created.SessionID); err == nil || confirmed {
		t.Fatalf("offline cancellation confirmed=%v err=%v", confirmed, err)
	}
	process, err = store.LatestSumikaProcess(e.Store, created.SessionID)
	if err != nil || process.CancelRequestedAt == nil || process.State != store.ProcessUnknown {
		t.Fatalf("offline cancellation state %+v: %v", process, err)
	}
	daemon = newLocalFakeDaemon(t, socketPath)
	if err := e.ReconcileSumika(); err != nil {
		t.Fatal(err)
	}
	process, err = store.LatestSumikaProcess(e.Store, created.SessionID)
	sess, sessionErr = store.GetSession(e.Store, created.SessionID)
	if err != nil || sessionErr != nil || process.State != store.ProcessLost || process.CancelRequestedAt == nil || sess.State != "open" {
		t.Fatalf("lost process cancellation result process=%+v session=%+v errors=%v/%v", process, sess, err, sessionErr)
	}
	response, body = localRequest(t, hs, http.MethodPost, "/sessions/"+strconv.FormatInt(created.SessionID, 10)+"/restart", `{}`, "")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("explicit restart after lost cancellation status=%d body=%s", response.StatusCode, body)
	}
	var restartedAfterCancel struct {
		Process store.Process `json:"process"`
	}
	if err := json.Unmarshal(body, &restartedAfterCancel); err != nil || restartedAfterCancel.Process.Generation != 4 || restartedAfterCancel.Process.State != store.ProcessRunning {
		t.Fatalf("explicit restart after lost cancellation %+v: %v", restartedAfterCancel, err)
	}

	response, body = localRequest(t, hs, http.MethodPost, "/projects/test/sessions", `{"kind":"local"}`, "second")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create second local session: %d %s", response.StatusCode, body)
	}
	var second struct {
		SessionID int64 `json:"session_id"`
	}
	if err := json.Unmarshal(body, &second); err != nil {
		t.Fatal(err)
	}
	response, body = localRequest(t, hs, http.MethodPost, "/sessions/"+strconv.FormatInt(second.SessionID, 10)+"/cancel", `{}`, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("cancel second session: %d %s", response.StatusCode, body)
	}
	var cancel struct {
		Confirmed bool   `json:"confirmed"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal(body, &cancel); err != nil {
		t.Fatal(err)
	}
	if !cancel.Confirmed || cancel.State != "cancelled" {
		t.Fatalf("confirmed cancellation response %+v", cancel)
	}
	secondSession, err := store.GetSession(e.Store, second.SessionID)
	if err != nil || secondSession.State != "cancelled" {
		t.Fatalf("cancelled session %+v: %v", secondSession, err)
	}
	secondProcess, err := store.LatestSumikaProcess(e.Store, second.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	rogue := daemon.session(secondProcess.Name)
	rogue.Status = sumika.StatusRunning
	daemon.restoreSession(rogue)
	closedKillsBefore := daemon.killCount()
	var notices []string
	previousNotify := e.Notify
	e.Notify = func(message string) { notices = append(notices, message) }
	reconcileErr := e.ReconcileSumika()
	e.Notify = previousNotify
	if reconcileErr != nil || daemon.killCount() != closedKillsBefore || daemon.session(rogue.Name).Status != sumika.StatusRunning {
		t.Fatalf("closed-session process was touched: err=%v kills=%d/%d runtime=%+v", reconcileErr, daemon.killCount(), closedKillsBefore, daemon.session(rogue.Name))
	}
	orphanReported := false
	for _, message := range notices {
		if strings.Contains(message, "orphan Sumika process \""+rogue.Name+"\"") {
			orphanReported = true
			break
		}
	}
	if !orphanReported {
		t.Fatalf("orphan Sumika process %q was not reported: %v", rogue.Name, notices)
	}
	fakeClock, ok := e.Clock.(*clock.Fake)
	if !ok {
		t.Fatal("console environment did not install a fake clock")
	}
	response, body = localRequest(t, hs, http.MethodPost, "/projects/test/sessions", `{"kind":"local"}`, "stubborn-cancel")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create stubborn-cancel session: %d %s", response.StatusCode, body)
	}
	var stubborn struct {
		SessionID int64         `json:"session_id"`
		Process   store.Process `json:"process"`
	}
	if err := json.Unmarshal(body, &stubborn); err != nil {
		t.Fatal(err)
	}
	daemon.setIgnoreTerm(stubborn.Process.Name)
	response, body = localRequest(t, hs, http.MethodPost, "/sessions/"+strconv.FormatInt(stubborn.SessionID, 10)+"/cancel", `{}`, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("request stubborn cancellation: %d %s", response.StatusCode, body)
	}
	var stubbornCancel struct {
		Confirmed bool   `json:"confirmed"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal(body, &stubbornCancel); err != nil || stubbornCancel.Confirmed || stubbornCancel.State != "open" {
		t.Fatalf("graceful cancellation result %+v: %v", stubbornCancel, err)
	}
	fakeClock.T = fakeClock.T.Add(29 * time.Second)
	if err := e.ReconcileSumika(); err != nil {
		t.Fatal(err)
	}
	if daemon.forceKillCount(stubborn.Process.Name) != 0 {
		t.Fatal("forced cancellation occurred before the grace period")
	}
	fakeClock.T = fakeClock.T.Add(time.Second)
	if err := e.ReconcileSumika(); err != nil {
		t.Fatal(err)
	}
	process, err = store.LatestSumikaProcess(e.Store, stubborn.SessionID)
	stubbornSession, sessionErr := store.GetSession(e.Store, stubborn.SessionID)
	if err != nil || sessionErr != nil || process.State != store.ProcessDead || stubbornSession.State != "cancelled" || daemon.forceKillCount(stubborn.Process.Name) != 1 {
		t.Fatalf("forced cancellation process=%+v session=%+v forces=%d errors=%v/%v", process, stubbornSession, daemon.forceKillCount(stubborn.Process.Name), err, sessionErr)
	}

	response, body = localRequest(t, hs, http.MethodPost, "/projects/test/sessions", `{"kind":"local"}`, "pending-cancel")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create pending-cancel session: %d %s", response.StatusCode, body)
	}
	var pending struct {
		SessionID int64         `json:"session_id"`
		Process   store.Process `json:"process"`
	}
	if err := json.Unmarshal(body, &pending); err != nil {
		t.Fatal(err)
	}
	liveRuntime := daemon.session(pending.Process.Name)
	daemon.stop()
	response, body = localRequest(t, hs, http.MethodPost, "/sessions/"+strconv.FormatInt(pending.SessionID, 10)+"/cancel", `{}`, "")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("offline pending cancellation status=%d body=%s", response.StatusCode, body)
	}
	var pendingCancel struct {
		Confirmed bool   `json:"confirmed"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal(body, &pendingCancel); err != nil || pendingCancel.Confirmed || pendingCancel.State != "open" {
		t.Fatalf("offline pending cancellation response %+v: %v", pendingCancel, err)
	}
	process, err = store.LatestSumikaProcess(e.Store, pending.SessionID)
	if err != nil || process.State != store.ProcessUnknown || process.CancelRequestedAt == nil {
		t.Fatalf("offline pending cancel state %+v: %v", process, err)
	}
	disabledPolicy, err := policy.Parse([]byte(`
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
projects:
  test:
    session_kinds: [run]
    repos: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	e.ReloadPolicy(disabledPolicy)
	daemon = newLocalFakeDaemon(t, socketPath)
	daemon.restoreSession(liveRuntime)
	if err := e.ReconcileSumika(); err != nil {
		t.Fatal(err)
	}
	process, err = store.LatestSumikaProcess(e.Store, pending.SessionID)
	pendingSession, sessionErr := store.GetSession(e.Store, pending.SessionID)
	if err != nil || sessionErr != nil || process.State != store.ProcessDead || pendingSession.State != "cancelled" {
		t.Fatalf("reconciled pending cancellation process=%+v session=%+v errors=%v/%v", process, pendingSession, err, sessionErr)
	}
	e.ReloadPolicy(pol)

	response, body = localRequest(t, hs, http.MethodPost, "/projects/test/sessions", `{"kind":"local"}`, "collision")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create collision test session: %d %s", response.StatusCode, body)
	}
	var collision struct {
		Process store.Process `json:"process"`
	}
	if err := json.Unmarshal(body, &collision); err != nil {
		t.Fatal(err)
	}
	killsBefore := daemon.killCount()
	daemon.setIdentity(collision.Process.Name, []string{"/bin/false"}, cwd)
	if err := e.ReconcileSumika(); err != nil {
		t.Fatal(err)
	}
	process, err = store.LatestSumikaProcess(e.Store, collision.Process.SessionID)
	if err != nil || process.State != store.ProcessLost || daemon.killCount() != killsBefore || daemon.session(collision.Process.Name).Status != sumika.StatusRunning {
		t.Fatalf("identity collision handling process=%+v err=%v kills=%d/%d runtime=%+v", process, err, daemon.killCount(), killsBefore, daemon.session(collision.Process.Name))
	}
	var collisionNotices []string
	previousNotify = e.Notify
	e.Notify = func(message string) { collisionNotices = append(collisionNotices, message) }
	err = e.ReconcileSumika()
	e.Notify = previousNotify
	identityWarning, orphanWarning := false, false
	for _, message := range collisionNotices {
		if strings.Contains(message, collision.Process.Name) && strings.Contains(message, "unresolved local identity") {
			identityWarning = true
		}
		if strings.Contains(message, "orphan Sumika process \""+collision.Process.Name+"\"") {
			orphanWarning = true
		}
	}
	if err != nil || !identityWarning || orphanWarning {
		t.Fatalf("lost identity notifications=%v err=%v", collisionNotices, err)
	}
	daemon.setStartError("invalid_request")
	response, body = localRequest(t, hs, http.MethodPost, "/projects/test/sessions", `{"kind":"local"}`, "rejected-start")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("rejected local start status=%d body=%s", response.StatusCode, body)
	}
	var rejectedStart struct {
		Process    store.Process `json:"process"`
		StartError string        `json:"start_error"`
	}
	if err := json.Unmarshal(body, &rejectedStart); err != nil || rejectedStart.StartError == "" || rejectedStart.Process.State != store.ProcessUnknown {
		t.Fatalf("rejected local start %+v: %v", rejectedStart, err)
	}
}

func localRequest(t *testing.T, hs *httptest.Server, method, path, body, idem string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, hs.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp, data
}

type localFakeDaemon struct {
	mu         sync.Mutex
	path       string
	listener   net.Listener
	sessions   map[string]sumika.Session
	attaches   map[string]net.Conn
	conns      map[net.Conn]struct{}
	ignoreTerm map[string]bool
	forceKills map[string]int
	startError string
	starts     int
	kills      int
}

func newLocalFakeDaemon(t *testing.T, path string) *localFakeDaemon {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	d := &localFakeDaemon{path: path, listener: listener, sessions: map[string]sumika.Session{}, attaches: map[string]net.Conn{}, conns: map[net.Conn]struct{}{}, ignoreTerm: map[string]bool{}, forceKills: map[string]int{}}
	go d.accept()
	t.Cleanup(d.stop)
	return d
}

func (d *localFakeDaemon) accept() {
	listener := d.listener
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		d.mu.Lock()
		d.conns[conn] = struct{}{}
		d.mu.Unlock()
		go d.serve(conn)
	}
}

func (d *localFakeDaemon) serve(conn net.Conn) {
	defer func() {
		d.mu.Lock()
		delete(d.conns, conn)
		for name, attached := range d.attaches {
			if attached == conn {
				delete(d.attaches, name)
			}
		}
		d.mu.Unlock()
		_ = conn.Close()
	}()
	var req struct {
		Op      string   `json:"op"`
		Name    string   `json:"name"`
		Argv    []string `json:"argv"`
		Cwd     string   `json:"cwd"`
		Project string   `json:"project"`
		Force   bool     `json:"force"`
	}
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		return
	}
	switch req.Op {
	case "start":
		d.mu.Lock()
		if d.startError != "" {
			code := d.startError
			d.mu.Unlock()
			_ = json.NewEncoder(conn).Encode(map[string]any{"ok": false, "error": map[string]string{"code": code, "message": "rejected by fake daemon"}})
			return
		}
		session, ok := d.sessions[req.Name]
		if !ok || session.Status == sumika.StatusDead {
			d.starts++
			project := req.Project
			session = sumika.Session{Name: req.Name, Argv: req.Argv, Cwd: req.Cwd, Status: sumika.StatusRunning, Project: &project}
			d.sessions[req.Name] = session
		}
		d.mu.Unlock()
		_ = json.NewEncoder(conn).Encode(map[string]any{"ok": true, "session": session})
	case "list":
		d.mu.Lock()
		infos := make([]sumika.Session, 0, len(d.sessions))
		for name, session := range d.sessions {
			_, session.Focused = d.attaches[name]
			infos = append(infos, session)
		}
		d.mu.Unlock()
		_ = json.NewEncoder(conn).Encode(map[string]any{"ok": true, "sessions": infos})
	case "kill":
		d.mu.Lock()
		session, ok := d.sessions[req.Name]
		if ok {
			d.kills++
			if req.Force {
				d.forceKills[req.Name]++
			}
		}
		if ok && d.ignoreTerm[req.Name] && !req.Force {
			d.mu.Unlock()
			_ = json.NewEncoder(conn).Encode(map[string]any{"ok": true, "session": session})
			return
		}
		attached := d.attaches[req.Name]
		delete(d.attaches, req.Name)
		d.mu.Unlock()
		if !ok {
			_ = json.NewEncoder(conn).Encode(map[string]any{"ok": false, "error": map[string]string{"code": "unknown_session", "message": "not found"}})
			return
		}
		session.Status = sumika.StatusDead
		session.Focused = false
		d.mu.Lock()
		d.sessions[req.Name] = session
		d.mu.Unlock()
		_ = json.NewEncoder(conn).Encode(map[string]any{"ok": true, "session": session})
		if attached != nil {
			_ = attached.Close()
		}
	case "attach":
		d.mu.Lock()
		session, ok := d.sessions[req.Name]
		previous := d.attaches[req.Name]
		if ok && session.Status != sumika.StatusDead {
			session.Focused = true
			d.attaches[req.Name] = conn
		}
		d.mu.Unlock()
		if !ok || session.Status == sumika.StatusDead {
			_ = json.NewEncoder(conn).Encode(map[string]any{"ok": false, "error": map[string]string{"code": "unknown_session", "message": "not found"}})
			return
		}
		if previous != nil {
			_ = previous.Close()
		}
		_ = json.NewEncoder(conn).Encode(map[string]any{"ok": true, "session": session})
		_, _ = conn.Write([]byte("pty-ready\n"))
		_, _ = io.Copy(io.Discard, conn)
	}
}

func (d *localFakeDaemon) setStatus(name string, status sumika.Status) {
	d.mu.Lock()
	session := d.sessions[name]
	session.Status = status
	d.sessions[name] = session
	if status == sumika.StatusDead {
		if conn := d.attaches[name]; conn != nil {
			delete(d.attaches, name)
			_ = conn.Close()
		}
	}
	d.mu.Unlock()
}

func (d *localFakeDaemon) session(name string) sumika.Session {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sessions[name]
}

func (d *localFakeDaemon) restoreSession(session sumika.Session) {
	d.mu.Lock()
	d.sessions[session.Name] = session
	d.mu.Unlock()
}

func (d *localFakeDaemon) setStartError(code string) {
	d.mu.Lock()
	d.startError = code
	d.mu.Unlock()
}

func (d *localFakeDaemon) startCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.starts
}

func (d *localFakeDaemon) killCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.kills
}

func (d *localFakeDaemon) setIgnoreTerm(name string) {
	d.mu.Lock()
	d.ignoreTerm[name] = true
	d.mu.Unlock()
}

func (d *localFakeDaemon) forceKillCount(name string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.forceKills[name]
}

func (d *localFakeDaemon) setIdentity(name string, argv []string, cwd string) {
	d.mu.Lock()
	session := d.sessions[name]
	session.Argv = argv
	session.Cwd = cwd
	d.sessions[name] = session
	d.mu.Unlock()
}

func (d *localFakeDaemon) stop() {
	if d.listener == nil {
		return
	}
	_ = d.listener.Close()
	d.mu.Lock()
	conns := make([]net.Conn, 0, len(d.conns))
	for conn := range d.conns {
		conns = append(conns, conn)
	}
	d.listener = nil
	d.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
	_ = os.Remove(d.path)
}
