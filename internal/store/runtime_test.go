package store

import (
	"database/sql"
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

func TestDeadProcessObservationRollsBackWhenAttachClosureFails(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "runtime-attach-failure.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sessionID := insertLocalSession(t, st)
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	process, err := StartSumikaProcess(st, sessionID, "test-process-identity", now)
	if err != nil {
		t.Fatal(err)
	}
	process, err = ObserveSumikaProcess(st, process.ID, process.Generation, process.Revision, ProcessRunning, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	attach, err := BeginSumikaAttach(st, process.ID, process.Generation, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	process, err = RequestSumikaCancel(st, process.ID, process.Generation, process.Revision, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`CREATE TRIGGER reject_process_exit_attach BEFORE UPDATE ON process_attaches
		WHEN NEW.state='process_exited' BEGIN SELECT RAISE(ABORT, 'attach update rejected'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err := ObserveSumikaProcess(st, process.ID, process.Generation, process.Revision, ProcessDead, now.Add(4*time.Second)); err == nil {
		t.Fatal("dead observation succeeded despite attach update failure")
	}
	process, err = GetSumikaProcess(st, process.ID)
	if err != nil || process.State != ProcessRunning {
		t.Fatalf("process after rejected observation %+v: %v", process, err)
	}
	attaches, err := ListSumikaAttaches(st, process.ID)
	if err != nil || len(attaches) != 1 || attaches[0].Generation != attach.Generation || attaches[0].State != AttachAttached {
		t.Fatalf("attach after rejected observation %+v: %v", attaches, err)
	}
	sess, err := GetSession(st, sessionID)
	if err != nil || sess.State != "open" {
		t.Fatalf("session after rejected observation %+v: %v", sess, err)
	}
}

func TestLocalSessionCancellationFencesProcessGeneration(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "runtime-cancel-fence.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	noProcessSession := insertLocalSession(t, st)
	if err := CancelLocalSessionWithoutProcess(st, noProcessSession, now); err != nil {
		t.Fatal(err)
	}
	if sess, err := GetSession(st, noProcessSession); err != nil || sess.State != "cancelled" {
		t.Fatalf("session without process %+v: %v", sess, err)
	}

	var sessionID int64
	if err := st.Tx(func(tx *sql.Tx) error {
		var insertErr error
		sessionID, insertErr = InsertLocalSessionTx(tx, "test", now)
		return insertErr
	}); err != nil {
		t.Fatal(err)
	}
	first, err := StartSumikaProcess(st, sessionID, "first-process-identity", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := CancelLocalSessionWithoutProcess(st, sessionID, now); !errors.Is(err, ErrStaleProcessObservation) {
		t.Fatalf("cancel with a recorded generation error %v", err)
	}
	first, err = ObserveSumikaProcess(st, first.ID, first.Generation, first.Revision, ProcessDead, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	second, err := StartSumikaProcess(st, sessionID, "second-process-identity", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := CancelLocalSessionAfterDeath(st, sessionID, first.ID, first.Generation, first.Revision, now); !errors.Is(err, ErrStaleProcessObservation) {
		t.Fatalf("cancel from an older generation error %v", err)
	}
	second, err = ObserveSumikaProcess(st, second.ID, second.Generation, second.Revision, ProcessDead, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := CancelLocalSessionAfterDeath(st, sessionID, second.ID, second.Generation, second.Revision, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if sess, err := GetSession(st, sessionID); err != nil || sess.State != "cancelled" {
		t.Fatalf("session after current dead generation %+v: %v", sess, err)
	}
	// Repeating either cancel is idempotent and records one cancellation.
	if err := CancelLocalSessionWithoutProcess(st, noProcessSession, now); err != nil {
		t.Fatal(err)
	}
	if err := CancelLocalSessionAfterDeath(st, sessionID, second.ID, second.Generation, second.Revision, now); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{noProcessSession, sessionID} {
		receipts, err := ListProcessReceipts(st, id)
		if err != nil {
			t.Fatal(err)
		}
		cancels := 0
		for _, r := range receipts {
			if r.Kind == "cancel" {
				cancels++
			}
		}
		if cancels != 1 {
			t.Fatalf("session %d cancel receipts %d: %+v", id, cancels, receipts)
		}
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

// Every local lifecycle transition leaves an append-only receipt, so an
// operator can audit starts, steals, deaths, restarts, and cancels later.
func TestLocalLifecycleWritesReceipts(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sid := insertLocalSession(t, st)
	now := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	tick := func() time.Time { now = now.Add(time.Second); return now }

	p, err := StartSumikaProcess(st, sid, "id-1", tick())
	if err != nil {
		t.Fatal(err)
	}
	if p, err = ObserveSumikaProcess(st, p.ID, p.Generation, p.Revision, ProcessRunning, tick()); err != nil {
		t.Fatal(err)
	}
	// An unchanged observation is not a transition.
	if p, err = ObserveSumikaProcess(st, p.ID, p.Generation, p.Revision, ProcessRunning, tick()); err != nil {
		t.Fatal(err)
	}
	first, err := BeginSumikaAttach(st, p.ID, p.Generation, tick())
	if err != nil {
		t.Fatal(err)
	}
	second, err := BeginSumikaAttach(st, p.ID, p.Generation, tick())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EndSumikaAttach(st, p.ID, p.Generation, first.Generation, AttachDetached, tick()); !errors.Is(err, ErrStaleAttach) {
		t.Fatalf("stolen attach detach: %v", err)
	}
	if _, err := EndSumikaAttach(st, p.ID, p.Generation, second.Generation, AttachDetached, tick()); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginSumikaAttach(st, p.ID, p.Generation, tick()); err != nil {
		t.Fatal(err)
	}
	if _, err = ObserveSumikaProcess(st, p.ID, p.Generation, p.Revision, ProcessLost, tick()); err != nil {
		t.Fatal(err)
	}
	p2, err := StartSumikaProcess(st, sid, "id-2", tick())
	if err != nil {
		t.Fatal(err)
	}
	if p2, err = ObserveSumikaProcess(st, p2.ID, p2.Generation, p2.Revision, ProcessRunning, tick()); err != nil {
		t.Fatal(err)
	}
	if err := MarkRuntimeObservationsUnknown(st, tick()); err != nil {
		t.Fatal(err)
	}
	if p2, err = GetSumikaProcess(st, p2.ID); err != nil {
		t.Fatal(err)
	}
	if p2, err = ObserveSumikaProcess(st, p2.ID, p2.Generation, p2.Revision, ProcessRunning, tick()); err != nil {
		t.Fatal(err)
	}
	if p2, err = RequestSumikaCancel(st, p2.ID, p2.Generation, p2.Revision, tick()); err != nil {
		t.Fatal(err)
	}
	if _, err = ObserveSumikaProcess(st, p2.ID, p2.Generation, p2.Revision, ProcessDead, tick()); err != nil {
		t.Fatal(err)
	}

	receipts, err := ListProcessReceipts(st, sid)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range receipts {
		line := fmt.Sprintf("g%d %s %s", *r.ProcessGeneration, r.Kind, r.State)
		if r.AttachGeneration != nil {
			line += fmt.Sprintf(" a%d", *r.AttachGeneration)
		}
		got = append(got, line)
	}
	want := []string{
		"g1 start starting", "g1 observe running",
		"g1 attach attached a1", "g1 steal stolen a1", "g1 attach attached a2", "g1 detach detached a2",
		"g1 attach attached a3", "g1 observe lost", "g1 exit process_exited a3",
		"g2 start starting", "g2 observe running", "g2 observe unknown", "g2 observe running",
		"g2 cancel_requested running", "g2 observe dead", "g2 cancel cancelled",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("receipts\n got %q\nwant %q", got, want)
	}
}
