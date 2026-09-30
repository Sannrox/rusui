package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	ErrProcessAlreadyActive    = errors.New("runtime process already active")
	ErrLocalSessionHasTurn     = errors.New("local session cannot have turns")
	ErrStaleProcessObservation = errors.New("stale process observation")
	ErrProcessNotAttachable    = errors.New("process is not attachable")
	ErrStaleAttach             = errors.New("stale attach generation")
	ErrInvalidProcessState     = errors.New("invalid process state")
	ErrLocalRuntimePaused      = errors.New("local runtime start is paused")
	ErrLocalSessionNotOpen     = errors.New("local session is not open")
)

type scanner interface {
	Scan(...any) error
}

func scanProcess(row scanner) (*Process, error) {
	p := &Process{}
	var created string
	var observed, cancelRequested sql.NullString
	if err := row.Scan(&p.ID, &p.SessionID, &p.Generation, &p.Runtime, &p.Name, &p.IdentityHash, &p.State, &p.Revision, &created, &observed, &cancelRequested); err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, err
	}
	p.CreatedAt = t
	if observed.Valid {
		t, err := time.Parse(time.RFC3339Nano, observed.String)
		if err != nil {
			return nil, err
		}
		p.ObservedAt = &t
	}
	if cancelRequested.Valid {
		t, err := time.Parse(time.RFC3339Nano, cancelRequested.String)
		if err != nil {
			return nil, err
		}
		p.CancelRequestedAt = &t
	}
	p.Attaches = []Attach{}
	return p, nil
}

func scanAttach(row scanner) (*Attach, error) {
	a := &Attach{}
	var created string
	var observed sql.NullString
	if err := row.Scan(&a.ID, &a.ProcessID, &a.ProcessGeneration, &a.Generation, &a.Revision, &a.State, &created, &observed); err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, err
	}
	a.CreatedAt = t
	if observed.Valid {
		t, err := time.Parse(time.RFC3339Nano, observed.String)
		if err != nil {
			return nil, err
		}
		a.ObservedAt = &t
	}
	return a, nil
}

const processColumns = `id, session_id, generation, runtime, name, identity_hash, state, revision, created_at, observed_at, cancel_requested_at`
const attachColumns = `id, process_id, process_generation, generation, revision, state, created_at, observed_at`

// StartSumikaProcess reserves the next process identity for a local Session.
// An unknown process remains active until Sumika confirms that it is gone.
func StartSumikaProcess(s *Store, sessionID int64, identityHash string, now time.Time) (*Process, error) {
	if identityHash == "" {
		return nil, fmt.Errorf("process runtime identity is empty")
	}
	var processID int64
	err := s.Tx(func(tx *sql.Tx) error {
		var kind, project, state string
		if err := tx.QueryRow(`SELECT kind, project, state FROM sessions WHERE id=?`, sessionID).Scan(&kind, &project, &state); err != nil {
			return err
		}
		if kind != SessionKindLocal {
			return fmt.Errorf("process runtime requires a local session")
		}
		if state != "open" {
			return ErrLocalSessionNotOpen
		}
		paused, err := Paused(tx, project)
		if err != nil {
			return err
		}
		if paused {
			return ErrLocalRuntimePaused
		}
		var turns int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM turns WHERE session_id=?`, sessionID).Scan(&turns); err != nil {
			return err
		}
		if turns != 0 {
			return ErrLocalSessionHasTurn
		}
		var active int
		// A lost generation permits an explicit restart without confirming its pending cancellation.
		if err := tx.QueryRow(`SELECT COUNT(*) FROM session_processes WHERE session_id=? AND state IN ('starting', 'running', 'idle', 'blocked', 'unknown')`, sessionID).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return ErrProcessAlreadyActive
		}
		var generation int64
		if err := tx.QueryRow(`SELECT COALESCE(MAX(generation), 0) + 1 FROM session_processes WHERE session_id=?`, sessionID).Scan(&generation); err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO session_processes (session_id, generation, runtime, name, identity_hash, state, revision, created_at)
			VALUES (?, ?, ?, ?, ?, ?, 1, ?)`, sessionID, generation, ProcessRuntimeSumika, fmt.Sprintf("rusui-%d", sessionID), identityHash, ProcessStarting, formatRuntimeTime(now))
		if err != nil {
			return err
		}
		processID, err = res.LastInsertId()
		if err != nil {
			return err
		}
		return processReceiptTx(tx, processID, "start", ProcessStarting, formatRuntimeTime(now))
	})
	if err != nil {
		return nil, err
	}
	return GetSumikaProcess(s, processID)
}

// ObserveSumikaProcess applies one optimistic, generation-fenced runtime observation.
func ObserveSumikaProcess(s *Store, processID, generation, revision int64, state string, observedAt time.Time) (*Process, error) {
	if !validObservedProcessState(state) {
		return nil, ErrInvalidProcessState
	}
	err := s.Tx(func(tx *sql.Tx) error {
		p, err := scanProcess(tx.QueryRow(`SELECT `+processColumns+` FROM session_processes WHERE id=?`, processID))
		if err != nil {
			return err
		}
		if p.Generation != generation || p.Revision != revision {
			return ErrStaleProcessObservation
		}
		if (p.State == ProcessDead || p.State == ProcessLost) && state != p.State {
			return ErrInvalidProcessState
		}
		at := formatRuntimeTime(observedAt)
		res, err := tx.Exec(`UPDATE session_processes SET state=?, revision=revision+1, observed_at=?
			WHERE id=? AND generation=? AND revision=?`, state, at, processID, generation, revision)
		if err != nil {
			return err
		}
		changed, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrStaleProcessObservation
		}
		if state != p.State {
			if err := processReceiptTx(tx, processID, "observe", state, at); err != nil {
				return err
			}
		}
		if state == ProcessDead || state == ProcessLost {
			if err := attachReceiptsTx(tx, "exit", AttachProcessExited, at,
				`a.process_id=? AND a.process_generation=? AND a.state IN ('attached', 'unknown')`, processID, generation); err != nil {
				return err
			}
			_, err = tx.Exec(`UPDATE process_attaches SET state=?, revision=revision+1, observed_at=?
				WHERE process_id=? AND process_generation=? AND state IN ('attached', 'unknown')`, AttachProcessExited, at, processID, generation)
			if err != nil {
				return err
			}
		}
		if state == ProcessDead {
			res, err := tx.Exec(`UPDATE sessions SET state='cancelled' WHERE id=? AND state!='cancelled'
				AND EXISTS (SELECT 1 FROM session_processes WHERE id=? AND cancel_requested_at IS NOT NULL)`, p.SessionID, processID)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil || n == 0 {
				return err
			}
			return processReceiptTx(tx, processID, "cancel", "cancelled", at)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return GetSumikaProcess(s, processID)
}

func validObservedProcessState(state string) bool {
	switch state {
	case ProcessRunning, ProcessIdle, ProcessBlocked, ProcessDead, ProcessLost, ProcessUnknown:
		return true
	default:
		return false
	}
}

// CancelLocalSessionWithoutProcess cancels a local session only while it has
// no recorded process generation.
func CancelLocalSessionWithoutProcess(s *Store, sessionID int64, now time.Time) error {
	return s.Tx(func(tx *sql.Tx) error {
		var kind, state string
		if err := tx.QueryRow(`SELECT kind, state FROM sessions WHERE id=?`, sessionID).Scan(&kind, &state); err != nil {
			return err
		}
		if kind != SessionKindLocal {
			return fmt.Errorf("process runtime requires a local session")
		}
		var processID int64
		err := tx.QueryRow(`SELECT id FROM session_processes WHERE session_id=? ORDER BY generation DESC LIMIT 1`, sessionID).Scan(&processID)
		if err == nil {
			return ErrStaleProcessObservation
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if state == "cancelled" {
			return nil // a repeated cancel is not a transition
		}
		if _, err = tx.Exec(`UPDATE sessions SET state='cancelled' WHERE id=? AND kind=?`, sessionID, SessionKindLocal); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO process_receipts (session_id, kind, state, created_at) VALUES (?, 'cancel', 'cancelled', ?)`,
			sessionID, formatRuntimeTime(now))
		return err
	})
}

// CancelLocalSessionAfterDeath cancels only if the exact latest process
// generation is still dead at the revision observed by the caller.
func CancelLocalSessionAfterDeath(s *Store, sessionID, processID, generation, revision int64, now time.Time) error {
	return s.Tx(func(tx *sql.Tx) error {
		var processSessionID, currentGeneration, currentRevision int64
		var state string
		if err := tx.QueryRow(`SELECT session_id, generation, revision, state FROM session_processes WHERE id=?`, processID).Scan(&processSessionID, &currentGeneration, &currentRevision, &state); err != nil {
			return err
		}
		if processSessionID != sessionID || currentGeneration != generation || currentRevision != revision || state != ProcessDead {
			return ErrStaleProcessObservation
		}
		var latestID int64
		if err := tx.QueryRow(`SELECT id FROM session_processes WHERE session_id=? ORDER BY generation DESC LIMIT 1`, sessionID).Scan(&latestID); err != nil {
			return err
		}
		if latestID != processID {
			return ErrStaleProcessObservation
		}
		var sessionState string
		if err := tx.QueryRow(`SELECT state FROM sessions WHERE id=?`, sessionID).Scan(&sessionState); err != nil {
			return err
		}
		if sessionState == "cancelled" {
			return nil // a repeated cancel is not a transition
		}
		res, err := tx.Exec(`UPDATE sessions SET state='cancelled' WHERE id=? AND kind=?`, sessionID, SessionKindLocal)
		if err != nil {
			return err
		}
		changed, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return sql.ErrNoRows
		}
		return processReceiptTx(tx, processID, "cancel", "cancelled", formatRuntimeTime(now))
	})
}

// BeginSumikaAttach allocates a distinct Attach generation. The new generation
// marks the previously observed writer as stolen without changing Process state.
func BeginSumikaAttach(s *Store, processID, processGeneration int64, now time.Time) (*Attach, error) {
	var attachID int64
	err := s.Tx(func(tx *sql.Tx) error {
		p, err := scanProcess(tx.QueryRow(`SELECT `+processColumns+` FROM session_processes WHERE id=?`, processID))
		if err != nil {
			return err
		}
		if p.Generation != processGeneration || (p.State != ProcessRunning && p.State != ProcessIdle && p.State != ProcessBlocked) {
			return ErrProcessNotAttachable
		}
		var currentID, currentGeneration int64
		err = tx.QueryRow(`SELECT id, generation FROM process_attaches WHERE process_id=? AND state IN ('attached', 'unknown')
			ORDER BY generation DESC LIMIT 1`, processID).Scan(&currentID, &currentGeneration)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		at := formatRuntimeTime(now)
		if err == nil {
			if _, err := tx.Exec(`UPDATE process_attaches SET state=?, revision=revision+1, observed_at=? WHERE id=? AND state IN ('attached', 'unknown')`, AttachStolen, at, currentID); err != nil {
				return err
			}
			if err := attachReceiptsTx(tx, "steal", AttachStolen, at, `a.id=?`, currentID); err != nil {
				return err
			}
		}
		var generation int64
		if err := tx.QueryRow(`SELECT COALESCE(MAX(generation), 0) + 1 FROM process_attaches WHERE process_id=?`, processID).Scan(&generation); err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO process_attaches (process_id, process_generation, generation, revision, state, created_at)
			VALUES (?, ?, ?, 1, ?, ?)`, processID, processGeneration, generation, AttachAttached, at)
		if err != nil {
			return err
		}
		attachID, err = res.LastInsertId()
		if err != nil {
			return err
		}
		return attachReceiptsTx(tx, "attach", AttachAttached, at, `a.id=?`, attachID)
	})
	if err != nil {
		return nil, err
	}
	return GetSumikaAttach(s, attachID)
}

// EndSumikaAttach records a disconnect only for the matching live generation.
// A repeated disconnect after the same generation ended is idempotent.
func EndSumikaAttach(s *Store, processID, processGeneration, attachGeneration int64, state string, observedAt time.Time) (*Attach, error) {
	if state != AttachDetached {
		return nil, ErrStaleAttach
	}
	var attachID int64
	err := s.Tx(func(tx *sql.Tx) error {
		a, err := scanAttach(tx.QueryRow(`SELECT `+attachColumns+` FROM process_attaches WHERE process_id=? AND generation=?`, processID, attachGeneration))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrStaleAttach
			}
			return err
		}
		attachID = a.ID
		if a.ProcessGeneration != processGeneration || (a.State != AttachAttached && a.State != AttachDetached) {
			return ErrStaleAttach
		}
		if a.State == AttachDetached {
			return nil
		}
		res, err := tx.Exec(`UPDATE process_attaches SET state=?, revision=revision+1, observed_at=?
			WHERE id=? AND process_generation=? AND generation=? AND state='attached'`, state, formatRuntimeTime(observedAt), attachID, processGeneration, attachGeneration)
		if err != nil {
			return err
		}
		changed, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrStaleAttach
		}
		return attachReceiptsTx(tx, "detach", state, formatRuntimeTime(observedAt), `a.id=?`, attachID)
	})
	if err != nil {
		return nil, err
	}
	return GetSumikaAttach(s, attachID)
}

func GetSumikaProcess(s *Store, id int64) (*Process, error) {
	p, err := scanProcess(s.DB.QueryRow(`SELECT `+processColumns+` FROM session_processes WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	p.Attaches, err = ListSumikaAttaches(s, p.ID)
	return p, err
}

func LatestSumikaProcess(s *Store, sessionID int64) (*Process, error) {
	return scanProcess(s.DB.QueryRow(`SELECT `+processColumns+` FROM session_processes WHERE session_id=? ORDER BY generation DESC LIMIT 1`, sessionID))
}

func ListLocalSessions(s *Store) ([]Session, error) {
	rows, err := s.DB.Query(`SELECT id, environment_id, kind, repo, item, item_kind, state, project, prompt, created_at
		FROM sessions WHERE kind=? ORDER BY id`, SessionKindLocal)
	if err != nil {
		return nil, err
	}
	var out []Session
	for rows.Next() {
		var sess Session
		var created string
		if err := rows.Scan(&sess.ID, &sess.EnvironmentID, &sess.Kind, &sess.Repo, &sess.Item, &sess.ItemKind, &sess.State, &sess.Project, &sess.Prompt, &created); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		if at, err := time.Parse(time.RFC3339Nano, created); err == nil {
			sess.CreatedAt = at
		}
		out = append(out, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return out, nil
}

func InsertLocalSessionTx(tx *sql.Tx, project string, now time.Time) (int64, error) {
	// Store.Open's single connection serializes the transaction choosing this local-only item ID.
	var item int
	if err := tx.QueryRow(`SELECT COALESCE(MIN(item), 0) - 1 FROM sessions WHERE kind=? AND repo=''`, SessionKindLocal).Scan(&item); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, project, created_at)
		VALUES (?, ?, '', ?, 'local', 'open', ?, ?)`, DefaultEnvironmentID, SessionKindLocal, item, project, formatRuntimeTime(now))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RequestSumikaCancel durably records intent before the external Kill request.
func RequestSumikaCancel(s *Store, processID, generation, revision int64, requestedAt time.Time) (*Process, error) {
	err := s.Tx(func(tx *sql.Tx) error {
		p, err := scanProcess(tx.QueryRow(`SELECT `+processColumns+` FROM session_processes WHERE id=?`, processID))
		if err != nil {
			return err
		}
		if p.Generation != generation || p.Revision != revision {
			return ErrStaleProcessObservation
		}
		at := formatRuntimeTime(requestedAt)
		if p.State == ProcessDead {
			res, err := tx.Exec(`UPDATE sessions SET state='cancelled' WHERE id=? AND state!='cancelled'`, p.SessionID)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil || n == 0 {
				return err
			}
			return processReceiptTx(tx, processID, "cancel", "cancelled", at)
		}
		if p.CancelRequestedAt != nil {
			return nil
		}
		res, err := tx.Exec(`UPDATE session_processes SET cancel_requested_at=?, revision=revision+1
			WHERE id=? AND generation=? AND revision=? AND cancel_requested_at IS NULL`, at, processID, generation, revision)
		if err != nil {
			return err
		}
		changed, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrStaleProcessObservation
		}
		return processReceiptTx(tx, processID, "cancel_requested", p.State, at)
	})
	if err != nil {
		return nil, err
	}
	return GetSumikaProcess(s, processID)
}

func GetSumikaAttach(s *Store, id int64) (*Attach, error) {
	return scanAttach(s.DB.QueryRow(`SELECT `+attachColumns+` FROM process_attaches WHERE id=?`, id))
}

func ListSessionProcesses(s *Store, sessionID int64) ([]Process, error) {
	rows, err := s.DB.Query(`SELECT `+processColumns+` FROM session_processes WHERE session_id=? ORDER BY generation`, sessionID)
	if err != nil {
		return nil, err
	}
	var out []Process
	for rows.Next() {
		p, err := scanProcess(rows)
		if err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Process{}
	}
	for i := range out {
		out[i].Attaches, err = ListSumikaAttaches(s, out[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func ListSumikaAttaches(s *Store, processID int64) ([]Attach, error) {
	rows, err := s.DB.Query(`SELECT `+attachColumns+` FROM process_attaches WHERE process_id=? ORDER BY generation`, processID)
	if err != nil {
		return nil, err
	}
	var out []Attach
	for rows.Next() {
		a, err := scanAttach(rows)
		if err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Attach{}
	}
	return out, nil
}

// MarkRuntimeObservationsUnknown invalidates live observations after a plane
// restart or runtime connection loss without inferring process death.
func MarkRuntimeObservationsUnknown(s *Store, observedAt time.Time) error {
	return s.Tx(func(tx *sql.Tx) error {
		at := formatRuntimeTime(observedAt)
		if _, err := tx.Exec(`INSERT INTO process_receipts (session_id, process_id, process_generation, kind, state, created_at)
			SELECT session_id, id, generation, 'observe', ?, ? FROM session_processes WHERE state IN ('starting', 'running', 'idle', 'blocked')`,
			ProcessUnknown, at); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE session_processes SET state=?, revision=revision+1, observed_at=?
			WHERE state IN ('starting', 'running', 'idle', 'blocked', 'unknown')`, ProcessUnknown, at); err != nil {
			return err
		}
		if err := attachReceiptsTx(tx, "observe", AttachUnknown, at, `a.state='attached'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE process_attaches SET state=?, revision=revision+1, observed_at=? WHERE state IN ('attached', 'unknown')`, AttachUnknown, at)
		return err
	})
}

// processReceiptTx appends a Process-level lifecycle receipt.
func processReceiptTx(tx *sql.Tx, processID int64, kind, state, at string) error {
	_, err := tx.Exec(`INSERT INTO process_receipts (session_id, process_id, process_generation, kind, state, created_at)
		SELECT session_id, id, generation, ?, ?, ? FROM session_processes WHERE id=?`, kind, state, at, processID)
	return err
}

// attachReceiptsTx appends one Attach-level receipt per attach matching
// where (over process_attaches a), before the caller changes their state.
func attachReceiptsTx(tx *sql.Tx, kind, state, at, where string, args ...any) error {
	_, err := tx.Exec(`INSERT INTO process_receipts (session_id, process_id, process_generation, attach_generation, kind, state, created_at)
		SELECT p.session_id, p.id, p.generation, a.generation, ?, ?, ?
		FROM process_attaches a JOIN session_processes p ON p.id=a.process_id WHERE `+where+` ORDER BY a.id`,
		append([]any{kind, state, at}, args...)...)
	return err
}

// ListProcessReceipts returns a session's local lifecycle history, oldest first.
func ListProcessReceipts(s *Store, sessionID int64) ([]ProcessReceipt, error) {
	rows, err := s.DB.Query(`SELECT id, session_id, process_id, process_generation, attach_generation, kind, state, created_at
		FROM process_receipts WHERE session_id=? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	return scanProcessReceipts(rows)
}

// ListProcessReceiptPage returns up to limit receipts after afterID and
// whether more follow.
func ListProcessReceiptPage(s *Store, sessionID, afterID int64, limit int) ([]ProcessReceipt, bool, error) {
	if afterID < 0 || limit <= 0 {
		return nil, false, errors.New("invalid process receipt page")
	}
	rows, err := s.DB.Query(`SELECT id, session_id, process_id, process_generation, attach_generation, kind, state, created_at
		FROM process_receipts WHERE session_id=? AND id>? ORDER BY id LIMIT ?`, sessionID, afterID, limit+1)
	if err != nil {
		return nil, false, err
	}
	receipts, err := scanProcessReceipts(rows)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(receipts) > limit
	if hasMore {
		receipts = receipts[:limit]
	}
	return receipts, hasMore, nil
}

func scanProcessReceipts(rows *sql.Rows) ([]ProcessReceipt, error) {
	defer func() { _ = rows.Close() }()
	out := []ProcessReceipt{}
	for rows.Next() {
		var r ProcessReceipt
		var created string
		if err := rows.Scan(&r.ID, &r.SessionID, &r.ProcessID, &r.ProcessGeneration, &r.AttachGeneration, &r.Kind, &r.State, &created); err != nil {
			return nil, err
		}
		var err error
		if r.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func formatRuntimeTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
