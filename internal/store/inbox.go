package store

import (
	"database/sql"
	"time"
)

// Inbox states of a review result (ADR 0038 D4).
const (
	InboxCurrent    = "current"    // the item hash still matches the latest snapshot
	InboxSuperseded = "superseded" // the item changed after the result; never published
)

// InboxNextDispose is the only next step the inbox suggests: record a
// disposition with `POST /results/{id}/dispositions`.
const InboxNextDispose = "dispose"

// InboxItem is the latest review result of one repository item, with the
// maintainer's latest disposition. Reading it grants nothing.
type InboxItem struct {
	ResultID        int64        `json:"result_id"`
	SessionID       int64        `json:"session_id"`
	Repo            string       `json:"repo"`
	Item            int          `json:"item"`
	ItemKind        string       `json:"item_kind"`
	Verdict         string       `json:"verdict,omitempty"`
	ProposedActions int          `json:"proposed_actions"`
	State           string       `json:"state"`
	Disposition     *Disposition `json:"disposition,omitempty"`
	Next            string       `json:"next,omitempty"`
}

// Actionable reports whether the maintainer still owes this item a decision.
func (i InboxItem) Actionable() bool { return i.Next != "" }

// InboxItems lists the latest result of each review session visible to
// tenant ("" lists every tenant). A review session is unique per item, so
// there is one row per item and repeated reviews never add entries.
func InboxItems(s *Store, tenant string) ([]InboxItem, error) {
	q := `
SELECT r.id, s.id, s.repo, s.item, s.item_kind,
       COALESCE(json_extract(r.payload, '$.verdict'), ''),
       COALESCE(json_array_length(r.payload, '$.proposed_actions'), 0),
       r.item_hash,
       (SELECT n.item_hash FROM snapshots n WHERE n.repo=s.repo AND n.item=s.item ORDER BY n.revision DESC LIMIT 1),
       d.id, d.disposition, d.wrong_finding, d.note, d.recorded_at
FROM review_revisions r
JOIN turns t ON t.id=r.job_id
JOIN sessions s ON s.id=t.session_id
LEFT JOIN result_dispositions d ON d.id=(SELECT MAX(x.id) FROM result_dispositions x WHERE x.review_revision_id=r.id)
WHERE s.kind='review'
  AND r.id=(SELECT MAX(r2.id) FROM review_revisions r2 JOIN turns t2 ON t2.id=r2.job_id WHERE t2.session_id=s.id)`
	var args []any
	if tenant != "" {
		q += ` AND COALESCE(s.tenant_id,'default')=?`
		args = append(args, NormalizeTenantID(tenant))
	}
	q += ` ORDER BY r.id DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []InboxItem
	for rows.Next() {
		var it InboxItem
		var hash string
		var latest, dValue, dNote, dAt sql.NullString
		var dID sql.NullInt64
		var dWrong sql.NullBool
		if err := rows.Scan(&it.ResultID, &it.SessionID, &it.Repo, &it.Item, &it.ItemKind, &it.Verdict, &it.ProposedActions,
			&hash, &latest, &dID, &dValue, &dWrong, &dNote, &dAt); err != nil {
			return nil, err
		}
		it.State = InboxCurrent
		if latest.Valid && latest.String != hash {
			it.State = InboxSuperseded
		}
		if dID.Valid {
			at, _ := time.Parse(time.RFC3339Nano, dAt.String)
			it.Disposition = &Disposition{ID: dID.Int64, ReviewRevisionID: it.ResultID, Value: dValue.String,
				WrongFinding: dWrong.Bool, Note: dNote.String, RecordedAt: at}
		}
		if it.State == InboxCurrent && it.Disposition == nil {
			it.Next = InboxNextDispose
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ReviewsOnDay reads the admitted review count of repo on a UTC day
// (YYYY-MM-DD).
func ReviewsOnDay(s *Store, repo, day string) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT count FROM daily_review_counts WHERE repo=? AND day=?`, repo, day).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n, err
}
