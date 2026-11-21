package store

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

// shadowResult inserts a review result; comment adds its dry-run comment.
func shadowResult(t *testing.T, s *Store, item int, comment bool) int64 {
	t.Helper()
	id, err := insertID(s.DB, `INSERT INTO review_revisions (job_id, claimed_revision, item_hash, main_sha, payload) VALUES (?, 1, 'h', 'm', '{}') RETURNING id`, item)
	if err != nil {
		t.Fatal(err)
	}
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
	s := openTestStore(t)
	id := shadowResult(t, s, 1, true)
	if _, err := RecordDisposition(s, Disposition{ReviewRevisionID: id + 1, Value: DispositionUseful}); !errors.Is(err, ErrUnknownResult) {
		t.Fatalf("unknown result: %v", err)
	}
	if _, err := RecordDisposition(s, Disposition{ReviewRevisionID: id, Value: "great"}); !errors.Is(err, ErrInvalidDisposition) {
		t.Fatalf("invalid value: %v", err)
	}
	neg := int64(-1)
	if _, err := RecordDisposition(s, Disposition{ReviewRevisionID: id, Value: DispositionUseful, Seconds: &neg}); !errors.Is(err, ErrInvalidSeconds) {
		t.Fatalf("negative seconds: %v", err)
	}
}

func reviewResult(t *testing.T, s *Store, repo string, item int, tenant string) int64 {
	t.Helper()
	sid, err := insertID(s.DB, `INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1, 'review', ?, ?, 'issue', 'open', ?) RETURNING id`,
		repo, item, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if tenant != "" {
		if err := SetSessionTenant(s, sid, tenant); err != nil {
			t.Fatal(err)
		}
	}
	tid, err := insertID(s.DB, `INSERT INTO turns (session_id, lane, state) VALUES (?, 'review', 'completed') RETURNING id`, sid)
	if err != nil {
		t.Fatal(err)
	}
	id, err := insertID(s.DB, `INSERT INTO review_revisions (job_id, claimed_revision, item_hash, main_sha, payload) VALUES (?, 1, 'h', 'm', '{}') RETURNING id`, tid)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRecordDispositionSecondsRoundTripAndNullOldRows(t *testing.T) {
	s := openTestStore(t)
	id := reviewResult(t, s, "o/r", 1, "")
	got, err := RecordDisposition(s, Disposition{ReviewRevisionID: id, Value: DispositionUseful, Seconds: new(int64(90)), By: Attribution{Actor: "alice"}})
	if err != nil || got.Seconds == nil || *got.Seconds != 90 {
		t.Fatalf("record with seconds: %+v %v", got, err)
	}
	var stored sql.NullInt64
	if err := s.DB.QueryRow(`SELECT seconds FROM result_dispositions WHERE id=?`, got.ID).Scan(&stored); err != nil || !stored.Valid || stored.Int64 != 90 {
		t.Fatalf("stored seconds %v %v", stored, err)
	}

	omitted := reviewResult(t, s, "o/r", 2, "")
	plain, err := RecordDisposition(s, Disposition{ReviewRevisionID: omitted, Value: DispositionNeutral})
	if err != nil || plain.Seconds != nil {
		t.Fatalf("omit seconds: %+v %v", plain, err)
	}
	if err := s.DB.QueryRow(`SELECT seconds FROM result_dispositions WHERE id=?`, plain.ID).Scan(&stored); err != nil || stored.Valid {
		t.Fatalf("omitted stored as unknown, not zero: %v %v", stored, err)
	}

	old := reviewResult(t, s, "o/r", 3, "")
	if _, err := s.DB.Exec(`INSERT INTO result_dispositions (review_revision_id, disposition, wrong_finding, note, recorded_at) VALUES (?, 'useful', 0, '', ?)`,
		old, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	items, err := InboxItems(s, "")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]*Disposition{}
	for _, it := range items {
		byID[it.ResultID] = it.Disposition
	}
	if d := byID[id]; d == nil || d.Seconds == nil || *d.Seconds != 90 {
		t.Fatalf("inbox seconds %+v", d)
	}
	if d := byID[omitted]; d == nil || d.Seconds != nil {
		t.Fatalf("inbox omitted %+v", d)
	}
	if d := byID[old]; d == nil || d.Seconds != nil {
		t.Fatalf("inbox old row %+v", d)
	}
}

func TestCommentGateSumsDispositionSupervisionSecondsPerRepoAndActor(t *testing.T) {
	s := openTestStore(t)
	a1 := reviewResult(t, s, "acme/one", 1, "acme")
	a2 := reviewResult(t, s, "acme/one", 2, "acme")
	a3 := reviewResult(t, s, "acme/two", 3, "acme")
	a4 := reviewResult(t, s, "acme/one", 4, "acme")
	other := reviewResult(t, s, "globex/x", 5, "globex")
	if _, err := RecordDisposition(s, Disposition{ReviewRevisionID: a1, Value: DispositionUseful, Seconds: new(int64(60)), By: Attribution{Actor: "alice"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordDisposition(s, Disposition{ReviewRevisionID: a2, Value: DispositionUseful, Seconds: new(int64(30)), By: Attribution{Actor: "bob"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordDisposition(s, Disposition{ReviewRevisionID: a3, Value: DispositionNeutral, Seconds: new(int64(10)), By: Attribution{Actor: "alice"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordDisposition(s, Disposition{ReviewRevisionID: a4, Value: DispositionHarmful}); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordDisposition(s, Disposition{ReviewRevisionID: other, Value: DispositionUseful, Seconds: new(int64(500)), By: Attribution{Actor: "alice"}}); err != nil {
		t.Fatal(err)
	}

	g, err := CommentGateReport(s, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if g.Supervision.Seconds != 100 {
		t.Fatalf("tenant total %+v", g.Supervision)
	}
	if len(g.Supervision.ByRepo) != 2 || g.Supervision.ByRepo[0] != (SupervisionSum{Repo: "acme/one", Seconds: 90}) || g.Supervision.ByRepo[1] != (SupervisionSum{Repo: "acme/two", Seconds: 10}) {
		t.Fatalf("by repo %+v", g.Supervision.ByRepo)
	}
	if len(g.Supervision.ByActor) != 2 || g.Supervision.ByActor[0] != (SupervisionSum{DecidedBy: "alice", Seconds: 70}) || g.Supervision.ByActor[1] != (SupervisionSum{DecidedBy: "bob", Seconds: 30}) {
		t.Fatalf("by actor %+v", g.Supervision.ByActor)
	}

	all, err := CommentGateReport(s, "")
	if err != nil || all.Supervision.Seconds != 600 {
		t.Fatalf("all tenants %+v %v", all.Supervision, err)
	}
}

func TestCommentGateCountsLatestDispositionOfShadowResults(t *testing.T) {
	s := openTestStore(t)
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

	g, err := CommentGateReport(s, "")
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
	if g, _ = CommentGateReport(s, ""); g.Status != "shadow_pass" || g.Results != CommentGateWindow {
		t.Fatalf("full window: %+v", g)
	}
	for _, id := range []int64{last, last - 1, last - 2} {
		dispose(t, s, id, DispositionHarmful, true)
	}
	g, _ = CommentGateReport(s, "")
	if g.Status != "fail" || g.Harmful != 3 || g.FalseRecommendations != 3 || len(g.Failed) != 1 {
		t.Fatalf("threshold: %+v", g)
	}
	if len(g.NotMeasured) == 0 {
		t.Fatal("unmeasured criteria must be listed, not passed")
	}
}
