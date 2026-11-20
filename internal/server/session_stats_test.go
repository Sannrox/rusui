package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func TestSessionListStatsSamplesReadyContainer(t *testing.T) {
	t.Parallel()
	hs, rt, readyID, sleepID, archivedID, processID := sessionStatsFixture(t)

	plain := authedGet(t, hs, "/sessions", "op-tok")
	if plain.code != http.StatusOK {
		t.Fatalf("list %d %s", plain.code, plain.body)
	}
	if len(rt.SampleCalls) != 0 {
		t.Fatalf("list sampled %v", rt.SampleCalls)
	}

	got := authedGet(t, hs, "/sessions?include=stats", "op-tok")
	if got.code != http.StatusOK {
		t.Fatalf("stats %d %s", got.code, got.body)
	}
	var rows []sessionListItem
	if err := json.Unmarshal([]byte(got.body), &rows); err != nil {
		t.Fatal(err)
	}
	byID := map[int64]sessionListItem{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	ready := byID[readyID]
	if ready.EnvironmentState != store.EnvReady || ready.Size != engine.SizeSmall {
		t.Fatalf("ready session %+v", ready.Session)
	}
	if ready.Usage == nil || ready.Usage.CPUMillis != 250 || ready.Usage.MemoryBytes != 32<<20 {
		t.Fatalf("ready usage %+v", ready.Usage)
	}
	if sleep := byID[sleepID]; sleep.EnvironmentState != store.EnvSleeping || sleep.Usage != nil {
		t.Fatalf("sleeping %+v usage=%v", sleep.Session, sleep.Usage)
	}
	if archived := byID[archivedID]; !archived.Archived || archived.Usage != nil {
		t.Fatalf("archived %+v usage=%v", archived.Session, archived.Usage)
	}
	if process := byID[processID]; process.Usage != nil {
		t.Fatalf("process usage %+v", process.Usage)
	}
	if strings.Contains(got.body, `"cpu_millis":0`) {
		t.Fatalf("fake zeros %s", got.body)
	}

	worker := authedGet(t, hs, "/sessions?include=stats", "wsec")
	if worker.code != http.StatusOK {
		t.Fatalf("worker %d %s", worker.code, worker.body)
	}
	denied := authedGet(t, hs, "/sessions?include=stats", "nope")
	if denied.code != http.StatusUnauthorized {
		t.Fatalf("bad token %d %s", denied.code, denied.body)
	}
	listDenied := authedGet(t, hs, "/sessions", "nope")
	if listDenied.code != denied.code {
		t.Fatalf("auth mismatch list=%d stats=%d", listDenied.code, denied.code)
	}

	detail := authedGet(t, hs, "/sessions/"+strconv.FormatInt(readyID, 10), "op-tok")
	if detail.code != http.StatusOK {
		t.Fatalf("detail %d %s", detail.code, detail.body)
	}
	var body struct {
		Services []store.EnvironmentService `json:"services"`
		Usage    *env.Sample                `json:"usage"`
	}
	if err := json.Unmarshal([]byte(detail.body), &body); err != nil {
		t.Fatal(err)
	}
	if body.Services == nil {
		t.Fatal("detail omitted services")
	}
	if body.Usage != nil {
		t.Fatalf("detail grew usage %s", detail.body)
	}
}

func sessionStatsFixture(t *testing.T) (*httptest.Server, *env.FakeRuntime, int64, int64, int64, int64) {
	t.Helper()
	_, hs, e := consoleEnv(t)
	rt := &env.FakeRuntime{Samples: map[string]env.Sample{}}
	e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}

	processID := createRunSession(t, hs, "process")
	readyID := createRunSession(t, hs, "ready")
	sleepID := createRunSession(t, hs, "sleep")
	archivedID := createRunSession(t, hs, "archived")

	bind := func(sid int64, name string, sample env.Sample) *store.Environment {
		t.Helper()
		created, err := e.ProvisionEnvironment(engine.EnvSpec{
			Name: name, Kind: env.KindContainer, CPUMillis: 1000, MemoryBytes: 2 << 30,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetSessionEnvironment(e.Store, sid, created.ID); err != nil {
			t.Fatal(err)
		}
		if err := e.Store.Tx(func(tx *store.Tx) error {
			return store.SetSessionSizeTx(tx, sid, engine.SizeSmall)
		}); err != nil {
			t.Fatal(err)
		}
		rt.Samples[created.Handle] = sample
		return created
	}
	endTurns := func(sid int64) {
		t.Helper()
		if _, err := e.Store.DB.Exec(`UPDATE turns SET state='failed' WHERE session_id=?`, sid); err != nil {
			t.Fatal(err)
		}
	}
	_ = bind(readyID, "box-ready", env.Sample{CPUMillis: 250, MemoryBytes: 32 << 20})
	sleepEnv := bind(sleepID, "box-sleep", env.Sample{CPUMillis: 9, MemoryBytes: 1})
	endTurns(sleepID)
	if _, err := e.SleepEnvironment(sleepEnv.ID); err != nil {
		t.Fatal(err)
	}
	_ = bind(archivedID, "box-archived", env.Sample{CPUMillis: 8, MemoryBytes: 2})
	endTurns(archivedID)
	if err := e.ArchiveSession(archivedID); err != nil {
		t.Fatal(err)
	}
	return hs, rt, readyID, sleepID, archivedID, processID
}
