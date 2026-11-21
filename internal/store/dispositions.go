package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Maintainer dispositions of a review result (ADR 0038 D6/D8).
const (
	DispositionUseful  = "useful"
	DispositionNeutral = "neutral"
	DispositionHarmful = "harmful"
)

// CommentGateWindow is how many trailing live shadow results the comment
// gate reads, and the minimum it needs before it can pass.
const CommentGateWindow = 20

var (
	ErrUnknownResult      = errors.New("unknown review result")
	ErrInvalidDisposition = errors.New("disposition must be useful, neutral, or harmful")
	ErrInvalidSeconds     = errors.New("seconds must be a non-negative integer")
)

// Disposition is a maintainer's judgment of one review result. It is a
// record only: it never changes policy, jobs, or actions.
type Disposition struct {
	ID               int64       `json:"id"`
	ReviewRevisionID int64       `json:"result_id"`
	Value            string      `json:"disposition"`
	WrongFinding     bool        `json:"wrong_finding"`
	Note             string      `json:"note,omitempty"`
	Seconds          *int64      `json:"seconds,omitempty"`
	RecordedAt       time.Time   `json:"recorded_at"`
	By               Attribution `json:"by,omitzero"`
}

// ReviewRevisionSession returns the session that produced a review result.
func ReviewRevisionSession(s *Store, revisionID int64) (*Session, error) {
	var sid int64
	err := s.DB.QueryRow(`SELECT t.session_id FROM review_revisions r JOIN turns t ON t.id=r.job_id WHERE r.id=?`, revisionID).Scan(&sid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnknownResult
	}
	if err != nil {
		return nil, err
	}
	return GetSession(s, sid)
}

// RecordDisposition appends a disposition for an existing review result.
// Earlier dispositions stay as history; the latest one counts.
func RecordDisposition(s *Store, d Disposition) (Disposition, error) {
	switch d.Value {
	case DispositionUseful, DispositionNeutral, DispositionHarmful:
	default:
		return Disposition{}, ErrInvalidDisposition
	}
	if d.Seconds != nil && *d.Seconds < 0 {
		return Disposition{}, ErrInvalidSeconds
	}
	var one int
	err := s.DB.QueryRow(`SELECT 1 FROM review_revisions WHERE id=?`, d.ReviewRevisionID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return Disposition{}, ErrUnknownResult
	}
	if err != nil {
		return Disposition{}, err
	}
	d.RecordedAt = time.Now().UTC()
	newID, err := scanID(s.DB.QueryRow(`INSERT INTO result_dispositions (review_revision_id, disposition, wrong_finding, note, recorded_at, decided_by, delegated_by, seconds) VALUES (?,?,?,?,?,?,?,?) RETURNING id`,
		d.ReviewRevisionID, d.Value, d.WrongFinding, d.Note, d.RecordedAt.Format(time.RFC3339Nano), nullable(d.By.Actor), nullable(d.By.Delegate), nullableSeconds(d.Seconds)))
	if err != nil {
		return Disposition{}, err
	}
	d.ID = newID
	return d, nil
}

// SupervisionSum is known supervision seconds for one repository or actor.
// NULL durations are omitted from the sum, not counted as zero.
type SupervisionSum struct {
	Repo      string `json:"repo,omitempty"`
	DecidedBy string `json:"decided_by,omitempty"`
	Seconds   int64  `json:"seconds"`
}

// SupervisionReport sums recorded supervision time across dispositions.
// Seconds is the known total; omitted durations do not contribute.
type SupervisionReport struct {
	Seconds int64            `json:"seconds"`
	ByRepo  []SupervisionSum `json:"by_repo,omitempty"`
	ByActor []SupervisionSum `json:"by_actor,omitempty"`
}

// CommentGate measures the trailing disposed live shadow results (review
// results that produced a dry-run comment) against the ADR 0038 D6
// comment thresholds. Criteria that shadow results cannot measure are
// listed in NotMeasured and never count as passing. Supervision sums every
// known duration for the tenant, not only the trailing window.
type CommentGate struct {
	Window               int               `json:"window"`
	Results              int               `json:"results"`
	Useful               int               `json:"useful"`
	Neutral              int               `json:"neutral"`
	Harmful              int               `json:"harmful"`
	FalseRecommendations int               `json:"false_recommendations"`
	Status               string            `json:"status"` // incomplete, shadow_pass, or fail
	Failed               []string          `json:"failed,omitempty"`
	NotMeasured          []string          `json:"not_measured"`
	Supervision          SupervisionReport `json:"supervision"`
}

// CommentGateReport reads the latest disposition of each of the most
// recent CommentGateWindow disposed shadow results.
func CommentGateReport(s *Store, tenant string) (CommentGate, error) {
	g := CommentGate{
		Window: CommentGateWindow,
		// Held-out cases and replay duplicates come from `rusui eval`;
		// private-context leaks need a separate check.
		NotMeasured: []string{"held_out_cases", "replay_duplicate_comments", "private_context_leaks"},
	}
	q := `
SELECT d.disposition, d.wrong_finding FROM result_dispositions d
WHERE d.id = (SELECT MAX(x.id) FROM result_dispositions x WHERE x.review_revision_id = d.review_revision_id)
  AND EXISTS (SELECT 1 FROM actions a WHERE a.review_revision_id = d.review_revision_id AND a.action_type = 'comment')`
	args := []any{}
	if tenant != "" {
		q += `
  AND EXISTS (
    SELECT 1 FROM review_revisions r
    JOIN turns t ON t.id=r.job_id
    JOIN sessions s ON s.id=t.session_id
    WHERE r.id=d.review_revision_id AND COALESCE(s.tenant_id,'default')=?
  )`
		args = append(args, NormalizeTenantID(tenant))
	}
	q += `
ORDER BY d.review_revision_id DESC LIMIT ?`
	args = append(args, CommentGateWindow)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return g, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v string
		var wrong bool
		if err := rows.Scan(&v, &wrong); err != nil {
			return g, err
		}
		g.Results++
		switch v {
		case DispositionUseful:
			g.Useful++
		case DispositionNeutral:
			g.Neutral++
		case DispositionHarmful:
			g.Harmful++
		}
		if wrong {
			g.FalseRecommendations++
		}
	}
	if err := rows.Err(); err != nil {
		return g, err
	}
	sup, err := supervisionReport(s, tenant)
	if err != nil {
		return g, err
	}
	g.Supervision = sup
	if g.Results < CommentGateWindow {
		g.Status = "incomplete"
		return g, nil
	}
	if g.Useful < 2*g.Harmful {
		g.Failed = append(g.Failed, fmt.Sprintf("useful %d < 2x harmful %d", g.Useful, g.Harmful))
	}
	if g.FalseRecommendations*10 > g.Results {
		g.Failed = append(g.Failed, fmt.Sprintf("false recommendations %d/%d > 10%%", g.FalseRecommendations, g.Results))
	}
	// shadow_pass covers only the shadow thresholds; NotMeasured still
	// has to be met before promotion.
	g.Status = "shadow_pass"
	if len(g.Failed) > 0 {
		g.Status = "fail"
	}
	return g, nil
}

func nullableSeconds(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func supervisionReport(s *Store, tenant string) (SupervisionReport, error) {
	from := `
FROM result_dispositions d
JOIN review_revisions r ON r.id=d.review_revision_id
JOIN turns t ON t.id=r.job_id
JOIN sessions sess ON sess.id=t.session_id`
	var cond string
	var args []any
	if tenant != "" {
		cond = ` WHERE COALESCE(sess.tenant_id,'default')=?`
		args = []any{NormalizeTenantID(tenant)}
	}
	var total sql.NullInt64
	if err := s.DB.QueryRow(`SELECT CAST(SUM(d.seconds) AS INTEGER) `+from+cond, args...).Scan(&total); err != nil {
		return SupervisionReport{}, err
	}
	out := SupervisionReport{}
	if total.Valid {
		out.Seconds = total.Int64
	}
	repos, err := s.DB.Query(`SELECT sess.repo, CAST(SUM(d.seconds) AS INTEGER) `+from+cond+`
GROUP BY sess.repo HAVING SUM(d.seconds) IS NOT NULL ORDER BY sess.repo`, args...)
	if err != nil {
		return SupervisionReport{}, err
	}
	for repos.Next() {
		var sum SupervisionSum
		if err := repos.Scan(&sum.Repo, &sum.Seconds); err != nil {
			_ = repos.Close()
			return SupervisionReport{}, err
		}
		out.ByRepo = append(out.ByRepo, sum)
	}
	err = repos.Err()
	_ = repos.Close()
	if err != nil {
		return SupervisionReport{}, err
	}
	actorCond := cond
	if actorCond == "" {
		actorCond = ` WHERE d.decided_by IS NOT NULL`
	} else {
		actorCond += ` AND d.decided_by IS NOT NULL`
	}
	actors, err := s.DB.Query(`SELECT d.decided_by, CAST(SUM(d.seconds) AS INTEGER) `+from+actorCond+`
GROUP BY d.decided_by HAVING SUM(d.seconds) IS NOT NULL ORDER BY d.decided_by`, args...)
	if err != nil {
		return SupervisionReport{}, err
	}
	defer func() { _ = actors.Close() }()
	for actors.Next() {
		var sum SupervisionSum
		if err := actors.Scan(&sum.DecidedBy, &sum.Seconds); err != nil {
			return SupervisionReport{}, err
		}
		out.ByActor = append(out.ByActor, sum)
	}
	return out, actors.Err()
}
