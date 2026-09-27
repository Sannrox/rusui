package store

import (
	"database/sql"
	"errors"
	"slices"
	"time"
)

// Measurement is the local record of one Turn. Empty strings and nil
// numbers are unknown. The row has no prompt, tool argument, diff,
// terminal text, file content, or credential.
type Measurement struct {
	TurnID             int64  `json:"turn_id"`
	SessionID          int64  `json:"session_id"`
	Project            string `json:"project"`
	Provider           string `json:"provider"`
	ProviderVersion    string `json:"provider_version"`
	TerminalState      string `json:"terminal_state"`
	DurationMS         *int64 `json:"duration_ms"`
	FirstEventMS       *int64 `json:"first_event_ms"`
	WakeMS             *int64 `json:"wake_ms"`
	Resume             string `json:"resume"`
	PermissionDecision string `json:"permission_decision"`
	TokensIn           *int64 `json:"tokens_in"`
	TokensOut          *int64 `json:"tokens_out"`
	Publication        string `json:"publication"`
}

// Aggregates is the operator read for one project.
type Aggregates struct {
	Turns            int            `json:"turns"`
	TerminalStates   map[string]int `json:"terminal_states"`
	MedianDurationMS *int64         `json:"median_duration_ms"`
	Denies           int            `json:"denies"`
	OmittedTokens    int            `json:"omitted_tokens"`
	Measurements     []Measurement  `json:"measurements"`
}

func BeginMeasurementTx(tx *sql.Tx, turnID int64, started time.Time) error {
	var sessionID int64
	var project string
	err := tx.QueryRow(`SELECT t.session_id, COALESCE(s.project, '') FROM turns t JOIN sessions s ON s.id=t.session_id WHERE t.id=?`, turnID).Scan(&sessionID, &project)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT OR IGNORE INTO turn_measurements (turn_id, session_id, project, started_at) VALUES (?,?,?,?)`,
		turnID, sessionID, project, started.UTC().Format(time.RFC3339Nano))
	return err
}

func NoteFirstEvent(s *Store, turnID int64, at time.Time) error {
	m, err := GetMeasurement(s, turnID)
	if err != nil || m == nil || m.FirstEventMS != nil {
		return err
	}
	ms, ok := millisSince(mStarted(s, turnID), at)
	if !ok {
		return nil
	}
	_, err = s.DB.Exec(`UPDATE turn_measurements SET first_event_ms=? WHERE turn_id=? AND first_event_ms IS NULL`, ms, turnID)
	return err
}

func NoteWake(s *Store, turnID int64, d time.Duration) error {
	ms := max(d.Milliseconds(), 0)
	_, err := s.DB.Exec(`UPDATE turn_measurements SET wake_ms=? WHERE turn_id=? AND wake_ms IS NULL`, ms, turnID)
	return err
}

func NoteResume(s *Store, turnID int64, outcome string) error {
	if outcome != "succeeded" && outcome != "refused" {
		return nil
	}
	_, err := s.DB.Exec(`UPDATE turn_measurements SET resume=? WHERE turn_id=? AND resume=''`, outcome, turnID)
	return err
}

func NotePermission(s *Store, turnID int64, decision string) error {
	if decision != "allow" && decision != "deny" {
		return nil
	}
	_, err := s.DB.Exec(`UPDATE turn_measurements SET permission_decision=?, deny_count=deny_count+CASE WHEN ?='deny' THEN 1 ELSE 0 END WHERE turn_id=?`, decision, decision, turnID)
	return err
}

func NoteProvider(s *Store, turnID int64, provider, version string) error {
	_, err := s.DB.Exec(`UPDATE turn_measurements SET provider=?, provider_version=? WHERE turn_id=?`, provider, version, turnID)
	return err
}

func FinishMeasurementTx(tx *sql.Tx, turnID int64, terminal string, ended time.Time, publication string, tokensIn, tokensOut *int) error {
	var started sql.NullString
	err := tx.QueryRow(`SELECT started_at FROM turn_measurements WHERE turn_id=?`, turnID).Scan(&started)
	if errors.Is(err, sql.ErrNoRows) {
		var sessionID int64
		var project string
		if err := tx.QueryRow(`SELECT t.session_id, COALESCE(s.project, '') FROM turns t JOIN sessions s ON s.id=t.session_id WHERE t.id=?`, turnID).Scan(&sessionID, &project); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO turn_measurements (turn_id, session_id, project, terminal_state, ended_at, publication, tokens_in, tokens_out) VALUES (?,?,?,?,?,?,?,?)`,
			turnID, sessionID, project, terminal, ended.UTC().Format(time.RFC3339Nano), publication, intPtr(tokensIn), intPtr(tokensOut))
		return err
	}
	if err != nil {
		return err
	}
	var duration any
	if started.Valid {
		if start, err := time.Parse(time.RFC3339Nano, started.String); err == nil {
			duration = max(ended.Sub(start).Milliseconds(), 0)
		}
	}
	_, err = tx.Exec(`UPDATE turn_measurements SET terminal_state=?, ended_at=?, duration_ms=?, publication=?, tokens_in=?, tokens_out=?, exported=0 WHERE turn_id=?`,
		terminal, ended.UTC().Format(time.RFC3339Nano), duration, publication, intPtr(tokensIn), intPtr(tokensOut), turnID)
	return err
}

// ClaimMeasurementExport marks one terminal measurement for a single export.
// A later finish clears the mark so the new outcome can be exported once.
func ClaimMeasurementExport(s *Store, turnID int64) (*Measurement, bool, error) {
	res, err := s.DB.Exec(`UPDATE turn_measurements SET exported=1 WHERE turn_id=? AND terminal_state<>'' AND exported=0`, turnID)
	if err != nil {
		return nil, false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return nil, false, err
	}
	m, err := GetMeasurement(s, turnID)
	return m, m != nil, err
}

func GetMeasurement(s *Store, turnID int64) (*Measurement, error) {
	row := s.DB.QueryRow(`SELECT turn_id, session_id, project, provider, provider_version, terminal_state, duration_ms, first_event_ms, wake_ms, resume, permission_decision, tokens_in, tokens_out, publication FROM turn_measurements WHERE turn_id=?`, turnID)
	m, err := scanMeasurement(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

func ProjectAggregates(s *Store, project string) (Aggregates, error) {
	rows, err := s.DB.Query(`SELECT turn_id, session_id, project, provider, provider_version, terminal_state, duration_ms, first_event_ms, wake_ms, resume, permission_decision, tokens_in, tokens_out, publication FROM turn_measurements WHERE project=? AND terminal_state<>'' ORDER BY turn_id`, project)
	if err != nil {
		return Aggregates{}, err
	}
	defer func() { _ = rows.Close() }()
	out := Aggregates{TerminalStates: map[string]int{}, Measurements: []Measurement{}}
	var durations []int64
	for rows.Next() {
		m, err := scanMeasurement(rows)
		if err != nil {
			return Aggregates{}, err
		}
		out.Measurements = append(out.Measurements, *m)
		out.Turns++
		out.TerminalStates[m.TerminalState]++
		if m.TokensIn == nil && m.TokensOut == nil {
			out.OmittedTokens++
		}
		if m.DurationMS != nil {
			durations = append(durations, *m.DurationMS)
		}
	}
	if err := rows.Err(); err != nil {
		return Aggregates{}, err
	}
	if err := s.DB.QueryRow(`SELECT COALESCE(SUM(deny_count),0) FROM turn_measurements WHERE project=? AND terminal_state<>''`, project).Scan(&out.Denies); err != nil {
		return Aggregates{}, err
	}
	out.MedianDurationMS = median(durations)
	return out, nil
}

func scanMeasurement(row interface{ Scan(...any) error }) (*Measurement, error) {
	var m Measurement
	var duration, first, wake, tin, tout sql.NullInt64
	err := row.Scan(&m.TurnID, &m.SessionID, &m.Project, &m.Provider, &m.ProviderVersion, &m.TerminalState, &duration, &first, &wake, &m.Resume, &m.PermissionDecision, &tin, &tout, &m.Publication)
	if err != nil {
		return nil, err
	}
	m.DurationMS = nullInt(duration)
	m.FirstEventMS = nullInt(first)
	m.WakeMS = nullInt(wake)
	m.TokensIn = nullInt(tin)
	m.TokensOut = nullInt(tout)
	return &m, nil
}

func nullInt(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func intPtr(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func mStarted(s *Store, turnID int64) time.Time {
	var raw sql.NullString
	_ = s.DB.QueryRow(`SELECT started_at FROM turn_measurements WHERE turn_id=?`, turnID).Scan(&raw)
	if !raw.Valid {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, raw.String)
	if err != nil {
		return time.Time{}
	}
	return t
}

func millisSince(start, at time.Time) (int64, bool) {
	if start.IsZero() {
		return 0, false
	}
	return max(at.Sub(start).Milliseconds(), 0), true
}

func median(xs []int64) *int64 {
	if len(xs) == 0 {
		return nil
	}
	slices.Sort(xs)
	mid := len(xs) / 2
	if len(xs)%2 == 1 {
		v := xs[mid]
		return &v
	}
	v := (xs[mid-1] + xs[mid]) / 2
	return &v
}
