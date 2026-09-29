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
)

// Disposition is a maintainer's judgment of one review result. It is a
// record only: it never changes policy, jobs, or actions.
type Disposition struct {
	ID               int64     `json:"id"`
	ReviewRevisionID int64     `json:"result_id"`
	Value            string    `json:"disposition"`
	WrongFinding     bool      `json:"wrong_finding"`
	Note             string    `json:"note,omitempty"`
	RecordedAt       time.Time `json:"recorded_at"`
}

// RecordDisposition appends a disposition for an existing review result.
// Earlier dispositions stay as history; the latest one counts.
func RecordDisposition(s *Store, d Disposition) (Disposition, error) {
	switch d.Value {
	case DispositionUseful, DispositionNeutral, DispositionHarmful:
	default:
		return Disposition{}, ErrInvalidDisposition
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
	res, err := s.DB.Exec(`INSERT INTO result_dispositions (review_revision_id, disposition, wrong_finding, note, recorded_at) VALUES (?,?,?,?,?)`,
		d.ReviewRevisionID, d.Value, d.WrongFinding, d.Note, d.RecordedAt.Format(time.RFC3339Nano))
	if err != nil {
		return Disposition{}, err
	}
	if d.ID, err = res.LastInsertId(); err != nil {
		return Disposition{}, err
	}
	return d, nil
}

// CommentGate measures the trailing disposed live shadow results (review
// results that produced a dry-run comment) against the ADR 0038 D6
// comment thresholds. Criteria that shadow results cannot measure are
// listed in NotMeasured and never count as passing.
type CommentGate struct {
	Window               int      `json:"window"`
	Results              int      `json:"results"`
	Useful               int      `json:"useful"`
	Neutral              int      `json:"neutral"`
	Harmful              int      `json:"harmful"`
	FalseRecommendations int      `json:"false_recommendations"`
	Status               string   `json:"status"` // incomplete, shadow_pass, or fail
	Failed               []string `json:"failed,omitempty"`
	NotMeasured          []string `json:"not_measured"`
}

// CommentGateReport reads the latest disposition of each of the most
// recent CommentGateWindow disposed shadow results.
func CommentGateReport(s *Store) (CommentGate, error) {
	g := CommentGate{
		Window: CommentGateWindow,
		// Held-out cases and replay duplicates come from `rusui eval`;
		// private-context leaks need a separate check.
		NotMeasured: []string{"held_out_cases", "replay_duplicate_comments", "private_context_leaks"},
	}
	rows, err := s.DB.Query(`
SELECT d.disposition, d.wrong_finding FROM result_dispositions d
WHERE d.id = (SELECT MAX(x.id) FROM result_dispositions x WHERE x.review_revision_id = d.review_revision_id)
  AND EXISTS (SELECT 1 FROM actions a WHERE a.review_revision_id = d.review_revision_id AND a.action_type = 'comment')
ORDER BY d.review_revision_id DESC LIMIT ?`, CommentGateWindow)
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
