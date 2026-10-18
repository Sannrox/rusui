package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/flight"
)

func InsertFlightRecordTx(tx *sql.Tx, rec flight.Record, now time.Time) error {
	rec = flight.Normalize(rec)
	body, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO flight_records (session_id, turn_id, receipt_id, body, created_at) VALUES (?,?,?,?,?)`,
		rec.SessionID, rec.TurnID, rec.ReceiptID, string(body), now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		if isUnique(err) {
			return flight.ErrOverwrite
		}
		return err
	}
	return nil
}

func InsertFlightRecord(s *Store, rec flight.Record, now time.Time) error {
	return s.Tx(func(tx *sql.Tx) error {
		return InsertFlightRecordTx(tx, rec, now)
	})
}

func GetFlightRecord(s *Store, turnID int64) (flight.Record, error) {
	var body string
	err := s.DB.QueryRow(`SELECT body FROM flight_records WHERE turn_id=? ORDER BY id DESC LIMIT 1`, turnID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return flight.Record{}, err
	}
	if err != nil {
		return flight.Record{}, err
	}
	var rec flight.Record
	if err := json.Unmarshal([]byte(body), &rec); err != nil {
		return flight.Record{}, err
	}
	return flight.Normalize(rec), nil
}

func DeleteFlightRecordsTx(tx *sql.Tx, sessionID int64) error {
	_, err := tx.Exec(`DELETE FROM flight_records WHERE session_id=?`, sessionID)
	return err
}

func ListActionsForTurnTx(tx *sql.Tx, turnID int64) ([]Action, error) {
	rows, err := tx.Query(`SELECT action_id, session_id, turn_id, review_revision_id, repo, item, action_type, reason_code, evidence_class, limit_sentence, body FROM actions WHERE turn_id=? ORDER BY rowid`, turnID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Action
	for rows.Next() {
		var a Action
		var sid, tid, rid sql.NullInt64
		if err := rows.Scan(&a.ID, &sid, &tid, &rid, &a.Repo, &a.Item, &a.Type, &a.ReasonCode, &a.EvidenceClass, &a.LimitSentence, &a.Body); err != nil {
			return nil, err
		}
		if sid.Valid {
			a.SessionID = &sid.Int64
		}
		if tid.Valid {
			a.TurnID = &tid.Int64
		}
		if rid.Valid {
			a.ReviewRevisionID = &rid.Int64
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func isUnique(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
