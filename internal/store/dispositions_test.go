package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

// shadowResult inserts a review result; comment adds its dry-run comment.
func shadowResult(t *testing.T, s *Store, item int, comment bool) int64 {
	t.Helper()
	res, err := s.DB.Exec(`INSERT INTO review_revisions (job_id, claimed_revision, item_hash, main_sha, payload) VALUES (?, 1, 'h', 'm', '{}')`, item)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if comment {
		if _, err := s.DB.Exec(`INSERT INTO actions (action_id, review_revision_id, repo, item, action_type, reason_code, evidence_class, limit_sentence, body) VALUES (?, ?, 'o/r', ?, 'comment', 'r', 'e', 'l', 'b')`,
			fmt.Sprintf("a%d", id), id, item); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func dispose(t *testing.T, s *Store, id int64, v string, wrong bool) {
	t.Helper()
	if _, err := RecordDisposition(s, Disposition{ReviewRevisionID: id, Value: v, WrongFinding: wrong}); err != nil {
		t.Fatal(err)
	}
}

func TestRecordDispositionRefusesUnknownResultAndValue(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	id := shadowResult(t, s, 1, true)
	if _, err := RecordDisposition(s, Disposition{ReviewRevisionID: id + 1, Value: DispositionUseful}); !errors.Is(err, ErrUnknownResult) {
		t.Fatalf("unknown result: %v", err)
	}
	if _, err := RecordDisposition(s, Disposition{ReviewRevisionID: id, Value: "great"}); !errors.Is(err, ErrInvalidDisposition) {
		t.Fatalf("invalid value: %v", err)
	}
}

func TestCommentGateCountsLatestDispositionOfShadowResults(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	// A result without a dry-run comment is not a shadow result.
	dispose(t, s, shadowResult(t, s, 100, false), DispositionHarmful, true)
	// The first result is superseded by a newer revision of the same item;
	// it can still be disposed, and re-disposing keeps history.
	first := shadowResult(t, s, 0, true)
	shadowResult(t, s, 0, false)
	dispose(t, s, first, DispositionHarmful, true)
	dispose(t, s, first, DispositionUseful, false)
	for i := 1; i < CommentGateWindow-1; i++ {
		dispose(t, s, shadowResult(t, s, i, true), DispositionUseful, false)
	}
	var rows int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM result_dispositions WHERE review_revision_id=?`, first).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("history rows %d %v", rows, err)
	}

	g, err := CommentGateReport(s)
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != "incomplete" || g.Results != CommentGateWindow-1 || g.Harmful != 0 || g.FalseRecommendations != 0 {
		t.Fatalf("below window: %+v", g)
	}

	// The 20th result completes the window; 3 harmful of 20 still leaves
	// useful ≥ 2× harmful, but 3 wrong findings exceed 10%.
	last := shadowResult(t, s, 20, true)
	dispose(t, s, last, DispositionUseful, false)
	if g, _ = CommentGateReport(s); g.Status != "shadow_pass" || g.Results != CommentGateWindow {
		t.Fatalf("full window: %+v", g)
	}
	for _, id := range []int64{last, last - 1, last - 2} {
		dispose(t, s, id, DispositionHarmful, true)
	}
	g, _ = CommentGateReport(s)
	if g.Status != "fail" || g.Harmful != 3 || g.FalseRecommendations != 3 || len(g.Failed) != 1 {
		t.Fatalf("threshold: %+v", g)
	}
	if len(g.NotMeasured) == 0 {
		t.Fatal("unmeasured criteria must be listed, not passed")
	}
}
