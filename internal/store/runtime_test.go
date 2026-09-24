package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestSumikaProcessAndAttachGenerations(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sessionID := insertLocalSession(t, st)
	now := time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC)
	process, err := StartSumikaProcess(st, sessionID, "test-process-identity", now)
	if err != nil {
		t.Fatal(err)
	}
	if process.Generation != 1 || process.Name != fmt.Sprintf("rusui-%d", sessionID) || process.State != ProcessStarting {
		t.Fatalf("initial process %+v", process)
	}
	process, err = ObserveSumikaProcess(st, process.ID, process.Generation, process.Revision, ProcessRunning, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	first, err := BeginSumikaAttach(st, process.ID, process.Generation, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	closed, err := EndSumikaAttach(st, process.ID, process.Generation, first.Generation, AttachDetached, now.Add(3*time.Second))
	if err != nil || closed.State != AttachDetached {
		t.Fatalf("detach %+v %v", closed, err)
	}
	duplicate, err := EndSumikaAttach(st, process.ID, process.Generation, first.Generation, AttachDetached, now.Add(4*time.Second))
	if err != nil || duplicate.State != AttachDetached || duplicate.Revision != closed.Revision {
		t.Fatalf("duplicate detach %+v (original %+v): %v", duplicate, closed, err)
	}
	stillRunning, err := GetSumikaProcess(st, process.ID)
	if err != nil || stillRunning.State != ProcessRunning {
		t.Fatalf("detach changed process %+v %v", stillRunning, err)
	}

	second, err := BeginSumikaAttach(st, process.ID, process.Generation, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	third, err := BeginSumikaAttach(st, process.ID, process.Generation, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if third.Generation != second.Generation+1 {
		t.Fatalf("attach generations second=%d third=%d", second.Generation, third.Generation)
	}
	if _, err := EndSumikaAttach(st, process.ID, process.Generation, second.Generation, AttachDetached, now.Add(6*time.Second)); !errors.Is(err, ErrStaleAttach) {
		t.Fatalf("stale disconnect error %v", err)
	}
	process, err = ObserveSumikaProcess(st, process.ID, process.Generation, process.Revision, ProcessDead, now.Add(7*time.Second))
	if err != nil || process.State != ProcessDead {
		t.Fatalf("process death %+v %v", process, err)
	}
	processes, err := ListSessionProcesses(st, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != 1 || len(processes[0].Attaches) != 3 {
		t.Fatalf("process history %+v", processes)
	}
	if processes[0].Attaches[1].State != AttachStolen || processes[0].Attaches[2].State != AttachProcessExited {
		t.Fatalf("attach history %+v", processes[0].Attaches)
	}

	if _, err := StartSumikaProcess(st, sessionID, "test-process-identity", now.Add(8*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := ObserveSumikaProcess(st, process.ID, process.Generation, process.Revision-1, ProcessRunning, now.Add(9*time.Second)); !errors.Is(err, ErrStaleProcessObservation) {
		t.Fatalf("stale process observation error %v", err)
	}
	var sessionState string
	if err := st.DB.QueryRow(`SELECT state FROM sessions WHERE id=?`, sessionID).Scan(&sessionState); err != nil || sessionState != "open" {
		t.Fatalf("session state %q %v", sessionState, err)
	}
	var turns int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM turns WHERE session_id=?`, sessionID).Scan(&turns); err != nil || turns != 0 {
		t.Fatalf("local turn count %d %v", turns, err)
	}
	if _, err := st.DB.Exec(`UPDATE sessions SET state='cancelled' WHERE id=?`, sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := StartSumikaProcess(st, sessionID, "test-process-identity", now.Add(9*time.Second)); !errors.Is(err, ErrLocalSessionNotOpen) {
		t.Fatalf("cancelled session allowed process restart: %v", err)
	}
}

func TestUnknownRuntimeObservationsRemainActiveUntilReconciled(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "runtime-restart.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sessionID := insertLocalSession(t, st)
	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC)
	process, err := StartSumikaProcess(st, sessionID, "test-process-identity", now)
	if err != nil {
		t.Fatal(err)
	}
	process, err = ObserveSumikaProcess(st, process.ID, process.Generation, process.Revision, ProcessIdle, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	attach, err := BeginSumikaAttach(st, process.ID, process.Generation, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkRuntimeObservationsUnknown(st, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	process, err = GetSumikaProcess(st, process.ID)
	if err != nil || process.State != ProcessUnknown {
		t.Fatalf("process after restart %+v %v", process, err)
	}
	staleRevision := process.Revision
	if err := MarkRuntimeObservationsUnknown(st, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	process, err = GetSumikaProcess(st, process.ID)
	if err != nil || process.State != ProcessUnknown || process.Revision != staleRevision+1 {
		t.Fatalf("process after repeated restart %+v %v", process, err)
	}
	if _, err := ObserveSumikaProcess(st, process.ID, process.Generation, staleRevision, ProcessDead, now.Add(5*time.Second)); !errors.Is(err, ErrStaleProcessObservation) {
		t.Fatalf("stale death report after repeated restart error %v", err)
	}
	if _, err := StartSumikaProcess(st, sessionID, "test-process-identity", now.Add(4*time.Second)); !errors.Is(err, ErrProcessAlreadyActive) {
		t.Fatalf("unknown process allowed duplicate start: %v", err)
	}
	if _, err := EndSumikaAttach(st, process.ID, process.Generation, attach.Generation, AttachDetached, now.Add(5*time.Second)); !errors.Is(err, ErrStaleAttach) {
		t.Fatalf("disconnect after restart error %v", err)
	}
	process, err = ObserveSumikaProcess(st, process.ID, process.Generation, process.Revision, ProcessIdle, now.Add(6*time.Second))
	if err != nil || process.State != ProcessIdle {
		t.Fatalf("reconciled process %+v %v", process, err)
	}
	reattached, err := BeginSumikaAttach(st, process.ID, process.Generation, now.Add(7*time.Second))
	if err != nil || reattached.Generation != attach.Generation+1 {
		t.Fatalf("reattach after runtime restart %+v: %v", reattached, err)
	}
	process, err = GetSumikaProcess(st, process.ID)
	if err != nil || len(process.Attaches) != 2 || process.Attaches[0].State != AttachStolen || process.Attaches[1].State != AttachAttached {
		t.Fatalf("reattach did not resolve previous observation: %+v %v", process, err)
	}
	if _, err := EndSumikaAttach(st, process.ID, process.Generation, attach.Generation, AttachDetached, now.Add(8*time.Second)); !errors.Is(err, ErrStaleAttach) {
		t.Fatalf("disconnect from superseded attach error %v", err)
	}
}

func insertLocalSession(t *testing.T, st *Store) int64 {
	t.Helper()
	res, err := st.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at)
		VALUES (?, ?, '', ?, 'local', 'open', ?)`, DefaultEnvironmentID, SessionKindLocal, -1, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
