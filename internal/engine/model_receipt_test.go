package engine

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/store"
)

func TestModelSummaryBoundsDetailAndRetainsStatusCounts(t *testing.T) {
	e := New(nil, nil, nil, clock.Real{})
	e.Log = log.New(io.Discard, "", 0)
	for i := range 80 {
		status := 200
		if i%2 != 0 {
			status = 429
		}
		e.ModelResponseRecorder(store.Grant{TurnID: 1, LeaseGeneration: 1, ExpiresAt: e.now().Add(GrantTTL)}, "http://127.0.0.1", fmt.Sprintf("/v1/%d", i))(status)
	}
	summary := e.modelSummaries[1]
	if summary.Count != 80 || summary.StatusCounts[200] != 40 || summary.StatusCounts[429] != 40 || len(summary.Calls) != 64 || summary.OtherCalls != 16 {
		t.Fatalf("bounded summary %+v", summary)
	}
	e.ModelResponseRecorder(store.Grant{TurnID: 2, LeaseGeneration: 1, ExpiresAt: e.now().Add(GrantTTL)}, "http://127.0.0.1", strings.Repeat("x", 2048))(200)
	call := e.modelSummaries[2].Calls[0]
	if len(call.Path) != 1024 || !call.PathTruncated {
		t.Fatalf("unbounded path: %d bytes, truncated=%v", len(call.Path), call.PathTruncated)
	}
}

func TestModelSummarySurvivesRollbackAndReclaimsFinishedCapacity(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "proof.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	e := New(st, nil, nil, clock.Real{})
	e.Log = log.New(io.Discard, "", 0)
	res, err := st.DB.Exec(`INSERT INTO sessions (environment_id,kind,repo,item,item_kind,state,created_at) VALUES (1,'review','example/repo',1,'issue','open','2026-10-08T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	sid, _ := res.LastInsertId()
	res, err = st.DB.Exec(`INSERT INTO turns(session_id,lane,state) VALUES (?,'review','leased')`, sid)
	if err != nil {
		t.Fatal(err)
	}
	tid, _ := res.LastInsertId()
	for i := range 256 {
		e.ModelResponseRecorder(store.Grant{TurnID: tid + int64(i), LeaseGeneration: 1, ExpiresAt: e.now().Add(GrantTTL)}, "http://127.0.0.1", "/v1/chat/completions")(200)
	}
	e.ModelResponseRecorder(store.Grant{TurnID: tid + 256, LeaseGeneration: 1, ExpiresAt: e.now().Add(GrantTTL)}, "http://127.0.0.1", "/v1/chat/completions")(200)
	if len(e.modelSummaries) != 256 || e.modelSummaries[tid+256] != nil {
		t.Fatal("active turn capacity is not bounded")
	}
	aborted := errors.New("abort terminal transaction")
	err = st.Tx(func(tx *sql.Tx) error {
		if err := e.finishMeasurementTx(tx, tid, "failed", Artifact{}, e.now()); err != nil {
			return err
		}
		return aborted
	})
	if !errors.Is(err, aborted) {
		t.Fatal(err)
	}
	actions, err := store.ListActionsForSession(st, sid)
	if err != nil || len(actions) != 0 {
		t.Fatalf("rollback left actions: %+v %v", actions, err)
	}
	for range 2 {
		if err := st.Tx(func(tx *sql.Tx) error { return e.finishMeasurementTx(tx, tid, "failed", Artifact{}, e.now()) }); err != nil {
			t.Fatal(err)
		}
	}
	actions, err = store.ListActionsForSession(st, sid)
	if err != nil || len(actions) != 1 {
		t.Fatalf("retry produced actions: %+v %v", actions, err)
	}
	var receipt modelProxySummary
	if err := json.Unmarshal([]byte(actions[0].Body), &receipt); err != nil || receipt.Count != 1 {
		t.Fatalf("lost rollback summary: %+v %v", receipt, err)
	}
	e.ModelResponseRecorder(store.Grant{TurnID: tid + 256, LeaseGeneration: 1, ExpiresAt: e.now().Add(GrantTTL)}, "http://127.0.0.1", "/v1/chat/completions")(200)
	if len(e.modelSummaries) != 256 || e.modelSummaries[tid+256] == nil {
		t.Fatal("finished summary did not release capacity")
	}
}

func TestLateModelResponsesCannotExhaustTurnCapacity(t *testing.T) {
	e := New(nil, nil, nil, clock.Real{})
	e.Log = log.New(io.Discard, "", 0)
	late := make([]func(int), 0, 512)
	for i := range 512 {
		id := int64(i + 1)
		record := e.ModelResponseRecorder(store.Grant{TurnID: id, LeaseGeneration: 1, ExpiresAt: e.now().Add(GrantTTL)}, "http://127.0.0.1", "/v1/chat/completions")
		// A cancelled turn has no observed responses when it finalizes.
		if err := e.recordModelSummaryTx(nil, id); err != nil {
			t.Fatal(err)
		}
		late = append(late, record)
	}
	for _, record := range late {
		record(200)
	}
	next := e.ModelResponseRecorder(store.Grant{TurnID: 513, LeaseGeneration: 1, ExpiresAt: e.now().Add(GrantTTL)}, "http://127.0.0.1", "/v1/chat/completions")
	next(200)
	summary := e.modelSummaries[513]
	if len(e.modelSummaries) > 256 || summary == nil || summary.Count != 1 {
		t.Fatal("late responses exhausted diagnostic capacity")
	}
	for id, summary := range e.modelSummaries {
		if id != 513 && (!summary.finished || summary.Count != 0) {
			t.Fatalf("late response revived turn %d: %+v", id, summary)
		}
	}
}

func TestModelRecorderRejectsPriorLeaseResponsesAfterReactivation(t *testing.T) {
	e := New(nil, nil, nil, clock.Real{})
	old := e.ModelResponseRecorder(store.Grant{TurnID: 1, LeaseGeneration: 1, ExpiresAt: e.now().Add(GrantTTL)}, "http://127.0.0.1", "/v1/chat/completions")
	if err := e.recordModelSummaryTx(nil, 1); err != nil {
		t.Fatal(err)
	}
	current := e.ModelResponseRecorder(store.Grant{TurnID: 1, LeaseGeneration: 2, ExpiresAt: e.now().Add(GrantTTL)}, "http://127.0.0.1", "/v1/chat/completions")
	old(200)
	current(429)
	stale := e.ModelResponseRecorder(store.Grant{TurnID: 1, LeaseGeneration: 1, ExpiresAt: e.now().Add(GrantTTL)}, "http://127.0.0.1", "/v1/chat/completions")
	stale(200)
	summary := e.modelSummaries[1]
	if summary.Generation != 2 || summary.Count != 1 || summary.StatusCounts[429] != 1 || summary.StatusCounts[200] != 0 {
		t.Fatalf("stale responses reached reused turn: %+v", summary)
	}
}

func TestModelRecorderCarriesUnfinishedAttemptCounts(t *testing.T) {
	e := New(nil, nil, nil, clock.Real{})
	old := e.ModelResponseRecorder(store.Grant{TurnID: 1, LeaseGeneration: 1, ExpiresAt: e.now().Add(GrantTTL)}, "http://127.0.0.1", "/v1/chat/completions")
	old(200)
	current := e.ModelResponseRecorder(store.Grant{TurnID: 1, LeaseGeneration: 2, ExpiresAt: e.now().Add(GrantTTL)}, "http://127.0.0.1", "/v1/chat/completions")
	current(429)
	old(503)
	summary := e.modelSummaries[1]
	if summary.Generation != 2 || summary.Count != 2 || summary.StatusCounts[200] != 1 || summary.StatusCounts[429] != 1 || summary.StatusCounts[503] != 0 {
		t.Fatalf("requeued attempt lost counts or accepted stale response: %+v", summary)
	}
}
