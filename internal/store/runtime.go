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
)

type scanner interface {
	Scan(...any) error
}

func scanProcess(row scanner) (*Process, error) {
	p := &Process{}
	var created string
	var observed sql.NullString
	if err := row.Scan(&p.ID, &p.SessionID, &p.Generation, &p.Runtime, &p.Name, &p.State, &p.Revision, &created, &observed); err != nil {
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

const processColumns = `id, session_id, generation, runtime, name, state, revision, created_at, observed_at`
const attachColumns = `id, process_id, process_generation, generation, revision, state, created_at, observed_at`

// StartSumikaProcess reserves the next process identity for a local Session.
// An unknown process remains active until Sumika confirms that it is gone.
func StartSumikaProcess(s *Store, sessionID int64, now time.Time) (*Process, error) {
	var processID int64
	err := s.Tx(func(tx *sql.Tx) error {
		var kind string
		if err := tx.QueryRow(`SELECT kind FROM sessions WHERE id=?`, sessionID).Scan(&kind); err != nil {
			return err
		}
		if kind != SessionKindLocal {
			return fmt.Errorf("process runtime requires a local session")
		}
		var turns int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM turns WHERE session_id=?`, sessionID).Scan(&turns); err != nil {
			return err
		}
		if turns != 0 {
			return ErrLocalSessionHasTurn
		}
		var active int
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
		res, err := tx.Exec(`INSERT INTO session_processes (session_id, generation, runtime, name, state, revision, created_at)
			VALUES (?, ?, ?, ?, ?, 1, ?)`, sessionID, generation, ProcessRuntimeSumika, fmt.Sprintf("rusui-%d", sessionID), ProcessStarting, formatRuntimeTime(now))
		if err != nil {
			return err
		}
		processID, err = res.LastInsertId()
		return err
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
		if state == ProcessDead || state == ProcessLost {
			_, err = tx.Exec(`UPDATE process_attaches SET state=?, revision=revision+1, observed_at=?
				WHERE process_id=? AND process_generation=? AND state IN ('attached', 'unknown')`, AttachProcessExited, at, processID, generation)
		}
		return err
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
		var currentID int64
		err = tx.QueryRow(`SELECT id FROM process_attaches WHERE process_id=? AND state IN ('attached', 'unknown')
			ORDER BY generation DESC LIMIT 1`, processID).Scan(&currentID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		at := formatRuntimeTime(now)
		if err == nil {
			if _, err := tx.Exec(`UPDATE process_attaches SET state=?, revision=revision+1, observed_at=? WHERE id=? AND state IN ('attached', 'unknown')`, AttachStolen, at, currentID); err != nil {
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
		return err
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
		return nil
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
		if _, err := tx.Exec(`UPDATE session_processes SET state=?, revision=revision+1, observed_at=?
			WHERE state IN ('starting', 'running', 'idle', 'blocked', 'unknown')`, ProcessUnknown, at); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE process_attaches SET state=?, revision=revision+1, observed_at=? WHERE state IN ('attached', 'unknown')`, AttachUnknown, at)
		return err
	})
}

func formatRuntimeTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
