package engine_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func TestPinnedEnvironmentSkipsIdleSleepThenUnpinSleeps(t *testing.T) {
	h := setup(t)
	rt := &env.FakeRuntime{DefaultFiles: map[string]bool{env.ResumePath: true}}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	h.e.EnvIdleSleep = 5 * time.Minute
	h.putRefresh(issue(1))
	c := h.claim()
	turn, err := store.GetTurn(h.st, c.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, turn.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(h.st, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
	// Complete leaves no lease, preview grant, or queued turn.
	if err := h.e.PinSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	pinned, err := store.GetSession(h.st, sess.ID)
	if err != nil || !pinned.Pinned {
		t.Fatalf("session pin %+v err=%v", pinned, err)
	}
	h.clk.Advance(6 * time.Minute)
	if err := h.e.SleepIdleEnvironments(); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetEnvironment(h.st, envRow.ID)
	if err != nil || got.State != store.EnvReady || !got.Pinned || len(rt.Stopped) != 0 {
		t.Fatalf("pinned idle sleep state=%+v stopped=%v err=%v", got, rt.Stopped, err)
	}
	if err := h.e.UnpinSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.e.SleepIdleEnvironments(); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetEnvironment(h.st, envRow.ID)
	if err != nil || got.State != store.EnvSleeping || got.Pinned || len(rt.Stopped) != 1 || rt.Stopped[0] != envRow.Handle {
		t.Fatalf("unpinned idle sleep state=%+v stopped=%v err=%v", got, rt.Stopped, err)
	}
	receipts, err := store.ListEnvironmentReceipts(h.st, sess.ID)
	if err != nil || len(receipts) < 3 || receipts[0].Kind != "pin" || receipts[0].State != "succeeded" || receipts[1].Kind != "unpin" || receipts[1].State != "succeeded" || receipts[2].Kind != "sleep" {
		t.Fatalf("pin receipts %+v err=%v", receipts, err)
	}
}

func TestArchiveClearsPinAndSleeps(t *testing.T) {
	h := setup(t)
	rt := &env.FakeRuntime{DefaultFiles: map[string]bool{env.ResumePath: true}}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	h.e.EnvIdleSleep = 5 * time.Minute
	h.putRefresh(issue(1))
	c := h.claim()
	turn, err := store.GetTurn(h.st, c.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, turn.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(h.st, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
	if err := h.e.PinSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.e.ArchiveSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	gotSess, err := store.GetSession(h.st, sess.ID)
	if err != nil || !gotSess.Archived || gotSess.Pinned {
		t.Fatalf("archived session %+v err=%v", gotSess, err)
	}
	got, err := store.GetEnvironment(h.st, envRow.ID)
	if err != nil || got.State != store.EnvSleeping || got.Pinned || len(rt.Stopped) != 1 {
		t.Fatalf("archive pin %+v stopped=%v err=%v", got, rt.Stopped, err)
	}
	if err := h.e.PinSession(sess.ID); !errors.Is(err, engine.ErrArchived) {
		t.Fatalf("pin archived: %v", err)
	}
}

func TestPinHTTPRoundTrip(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(sid, 10)
	req, err := http.NewRequest(http.MethodPost, h.http.URL+"/sessions/"+id+"/pin", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := ioRead(res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("pin %d %s", res.StatusCode, body)
	}
	var got struct {
		OK      bool `json:"ok"`
		Session struct {
			Pinned bool `json:"pinned"`
		} `json:"session"`
	}
	if err := json.Unmarshal(body, &got); err != nil || !got.OK || !got.Session.Pinned {
		t.Fatalf("pin body %s err=%v", body, err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil || !sess.Pinned {
		t.Fatalf("session %+v err=%v", sess, err)
	}

	req, err = http.NewRequest(http.MethodPost, h.http.URL+"/sessions/"+id+"/unpin", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = ioRead(res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unpin %d %s", res.StatusCode, body)
	}
	if err := json.Unmarshal(body, &got); err != nil || !got.OK || got.Session.Pinned {
		t.Fatalf("unpin body %s err=%v", body, err)
	}

	req, err = http.NewRequest(http.MethodPost, h.http.URL+"/sessions/"+id+"/archive", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = ioRead(res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("archive %d %s", res.StatusCode, body)
	}
	req, err = http.NewRequest(http.MethodPost, h.http.URL+"/sessions/"+id+"/pin", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = ioRead(res)
	if res.StatusCode != http.StatusConflict || !strings.Contains(string(body), "archived") {
		t.Fatalf("pin archived %d %s", res.StatusCode, body)
	}
}
