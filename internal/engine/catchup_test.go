package engine_test

import (
	"testing"

	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

func runItem(t *testing.T, st *store.Store) int {
	t.Helper()
	var item int
	if err := st.DB.QueryRow(`SELECT item FROM sessions WHERE kind=?`, store.SessionKindRun).Scan(&item); err != nil {
		t.Fatal(err)
	}
	if item >= 0 {
		t.Fatalf("run item %d", item)
	}
	return item
}

func refreshCount(t *testing.T, st *store.Store, repo string, item int) int {
	t.Helper()
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM refresh_requests WHERE repo=? AND item=?`, repo, item).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCatchUpSkipsLocalRunJobs(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("test", "do the thing", ""); err != nil {
		t.Fatal(err)
	}
	item := runItem(t, h.st)
	if err := h.e.CatchUpOpenAndLocal(nil); err != nil {
		t.Fatal(err)
	}
	if n := refreshCount(t, h.st, "example/test-repo", item); n != 0 {
		t.Fatalf("run item %d queued %d refreshes", item, n)
	}

	it := issue(7)
	h.f.Put(it)
	if err := h.e.CatchUpOpenAndLocal([]snapshot.Item{it}); err != nil {
		t.Fatal(err)
	}
	if n := refreshCount(t, h.st, it.Repo, it.Item); n != 1 {
		t.Fatalf("open issue refresh count %d", n)
	}
	if n := refreshCount(t, h.st, "example/test-repo", item); n != 0 {
		t.Fatalf("run item %d queued after github catch-up: %d", item, n)
	}
}

func TestCatchUpLeavesFailedRunRefreshIdle(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("test", "do the thing", ""); err != nil {
		t.Fatal(err)
	}
	item := runItem(t, h.st)
	if _, err := h.st.DB.Exec(`INSERT INTO refresh_requests (repo,item,item_kind,generation,owner,state,retry_count) VALUES (?,?,?,?,0,'failed',5)`,
		"example/test-repo", item, "run", 1); err != nil {
		t.Fatal(err)
	}
	if err := h.e.CatchUpOpenAndLocal(nil); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := h.st.DB.QueryRow(`SELECT state FROM refresh_requests WHERE repo=? AND item=?`, "example/test-repo", item).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "failed" {
		t.Fatalf("state %s", state)
	}
}
