package store

import (
	"database/sql"
	"sort"
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

// InboxActionableLimit is ADR 0038 D6 notification burden: more than this
// many actionable items per repository per UTC day moves routine advisory
// completions into a digest.
const InboxActionableLimit = 10

// InboxDigestStopDays is how many consecutive UTC days over the limit, with
// advisory_digest on, stop comment and repair for that repository.
const InboxDigestStopDays = 3

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
	CreatedAt       time.Time    `json:"created_at,omitzero"`
	Disposition     *Disposition `json:"disposition,omitempty"`
	Next            string       `json:"next,omitempty"`
}

// InboxDigest is one repository UTC day's routine advisory completions
// grouped after the actionable count exceeds InboxActionableLimit.
type InboxDigest struct {
	Repo      string  `json:"repo"`
	Day       string  `json:"day"`
	Count     int     `json:"count"`
	ResultIDs []int64 `json:"result_ids"`
}

// Actionable reports whether the maintainer still owes this item a decision.
func (i InboxItem) Actionable() bool { return i.Next != "" }

// Urgent reports a failure or a proposed action still waiting on a
// maintainer decision. Those stay listed when routine completions digest.
func (i InboxItem) Urgent() bool {
	if i.ProposedActions > 0 {
		return true
	}
	switch i.Verdict {
	case "blocked", "error", "fail", "failed":
		return true
	}
	return false
}

// InboxUTCDay is the UTC calendar day of t, or "" when t is unknown.
func InboxUTCDay(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

type inboxQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// InboxItems lists the latest result of each review session visible to
// tenant ("" lists every tenant). A review session is unique per item, so
// there is one row per item and repeated reviews never add entries.
func InboxItems(s *Store, tenant string) ([]InboxItem, error) {
	return inboxItems(s.DB, tenant)
}

// InboxItemsTx is InboxItems inside an open transaction.
func InboxItemsTx(tx *sql.Tx, tenant string) ([]InboxItem, error) {
	return inboxItems(tx, tenant)
}

func inboxItems(q inboxQueryer, tenant string) ([]InboxItem, error) {
	qry := `
SELECT r.id, s.id, s.repo, s.item, s.item_kind,
       COALESCE(json_extract(r.payload, '$.verdict'), ''),
       COALESCE(json_array_length(r.payload, '$.proposed_actions'), 0),
       r.item_hash,
       (SELECT n.item_hash FROM snapshots n WHERE n.repo=s.repo AND n.item=s.item ORDER BY n.revision DESC LIMIT 1),
       r.created_at,
       d.id, d.disposition, d.wrong_finding, d.note, d.recorded_at, d.seconds
FROM review_revisions r
JOIN turns t ON t.id=r.job_id
JOIN sessions s ON s.id=t.session_id
LEFT JOIN result_dispositions d ON d.id=(SELECT MAX(x.id) FROM result_dispositions x WHERE x.review_revision_id=r.id)
WHERE s.kind='review'
  AND r.id=(SELECT MAX(r2.id) FROM review_revisions r2 JOIN turns t2 ON t2.id=r2.job_id WHERE t2.session_id=s.id)`
	var args []any
	if tenant != "" {
		qry += ` AND COALESCE(s.tenant_id,'default')=?`
		args = append(args, NormalizeTenantID(tenant))
	}
	qry += ` ORDER BY r.id DESC`
	rows, err := q.Query(qry, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []InboxItem
	for rows.Next() {
		var it InboxItem
		var hash string
		var latest, created, dValue, dNote, dAt sql.NullString
		var dID, dSeconds sql.NullInt64
		var dWrong sql.NullBool
		if err := rows.Scan(&it.ResultID, &it.SessionID, &it.Repo, &it.Item, &it.ItemKind, &it.Verdict, &it.ProposedActions,
			&hash, &latest, &created, &dID, &dValue, &dWrong, &dNote, &dAt, &dSeconds); err != nil {
			return nil, err
		}
		if created.Valid {
			if at, err := time.Parse(time.RFC3339Nano, created.String); err == nil {
				it.CreatedAt = at
			}
		}
		it.State = InboxCurrent
		if latest.Valid && latest.String != hash {
			it.State = InboxSuperseded
		}
		if dID.Valid {
			at, _ := time.Parse(time.RFC3339Nano, dAt.String)
			d := &Disposition{ID: dID.Int64, ReviewRevisionID: it.ResultID, Value: dValue.String,
				WrongFinding: dWrong.Bool, Note: dNote.String, RecordedAt: at}
			if dSeconds.Valid {
				sec := dSeconds.Int64
				d.Seconds = &sec
			}
			it.Disposition = d
		}
		if it.State == InboxCurrent && it.Disposition == nil {
			it.Next = InboxNextDispose
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func actionableOnDay(items []InboxItem, repo, day string) int {
	n := 0
	for _, it := range items {
		if it.Repo == repo && it.Actionable() && InboxUTCDay(it.CreatedAt) == day {
			n++
		}
	}
	return n
}

// DigestStopsWrites reports three consecutive UTC days, ending at now,
// each over InboxActionableLimit for repo.
func DigestStopsWrites(items []InboxItem, repo string, now time.Time) bool {
	now = now.UTC()
	for i := range InboxDigestStopDays {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		if actionableOnDay(items, repo, day) <= InboxActionableLimit {
			return false
		}
	}
	return true
}

type inboxDay struct {
	repo, day string
}

// GroupInbox replaces routine actionable completions with one digest per
// repository UTC day when digestOn[repo] is set and that day's actionable
// count exceeds InboxActionableLimit. Urgent items stay listed. Days at
// or under the limit, and rows without created_at, stay listed.
func GroupInbox(items []InboxItem, digestOn map[string]bool) (listed []InboxItem, digests []InboxDigest) {
	listed = []InboxItem{}
	actionable := map[inboxDay]int{}
	for _, it := range items {
		day := InboxUTCDay(it.CreatedAt)
		if !it.Actionable() || day == "" {
			continue
		}
		actionable[inboxDay{it.Repo, day}]++
	}
	byDay := map[inboxDay]*InboxDigest{}
	for _, it := range items {
		day := InboxUTCDay(it.CreatedAt)
		k := inboxDay{it.Repo, day}
		if !digestOn[it.Repo] || day == "" || actionable[k] <= InboxActionableLimit || !it.Actionable() || it.Urgent() {
			listed = append(listed, it)
			continue
		}
		d := byDay[k]
		if d == nil {
			d = &InboxDigest{Repo: it.Repo, Day: day, ResultIDs: []int64{}}
			byDay[k] = d
		}
		d.Count++
		d.ResultIDs = append(d.ResultIDs, it.ResultID)
	}
	if len(byDay) == 0 {
		return listed, nil
	}
	digests = make([]InboxDigest, 0, len(byDay))
	for _, d := range byDay {
		digests = append(digests, *d)
	}
	sort.Slice(digests, func(i, j int) bool {
		if digests[i].Repo != digests[j].Repo {
			return digests[i].Repo < digests[j].Repo
		}
		return digests[i].Day > digests[j].Day
	})
	return listed, digests
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
