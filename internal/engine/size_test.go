package engine_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func TestTwoSizesProduceDifferentContainerLimits(t *testing.T) {
	h := setup(t)
	pol, err := policy.Parse([]byte(strings.Replace(fixture, "test:\n", "test:\n    budgets: {max_concurrent_leases: 2}\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(pol)
	rt := &env.FakeRuntime{}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}

	smallID, err := h.e.StartRunSize("test", "small work", "", engine.SizeSmall)
	if err != nil {
		t.Fatal(err)
	}
	largeID, err := h.e.StartRunSize("test", "large work", "", engine.SizeLarge)
	if err != nil {
		t.Fatal(err)
	}
	c1, err := h.e.Claim("example/test-repo")
	if err != nil || c1 == nil {
		t.Fatalf("first claim %v %v", c1, err)
	}
	c2, err := h.e.Claim("example/test-repo")
	if err != nil || c2 == nil {
		t.Fatalf("second claim %v %v", c2, err)
	}
	if len(rt.Created) != 2 {
		t.Fatalf("created %d", len(rt.Created))
	}
	got := map[int]int64{}
	for _, spec := range rt.Created {
		got[spec.CPUMillis] = spec.MemoryBytes
	}
	smallCPU, smallMem := engine.SizeLimits(engine.SizeSmall)
	largeCPU, largeMem := engine.SizeLimits(engine.SizeLarge)
	if got[smallCPU] != smallMem || got[largeCPU] != largeMem || smallCPU == largeCPU {
		t.Fatalf("limits %#v want small %d/%d large %d/%d", got, smallCPU, smallMem, largeCPU, largeMem)
	}
	smallSess, err := store.GetSession(h.st, smallID)
	if err != nil {
		t.Fatal(err)
	}
	largeSess, err := store.GetSession(h.st, largeID)
	if err != nil {
		t.Fatal(err)
	}
	smallEnv, err := store.GetEnvironment(h.st, smallSess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	largeEnv, err := store.GetEnvironment(h.st, largeSess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if smallEnv.CPUMillis != smallCPU || largeEnv.CPUMillis != largeCPU {
		t.Fatalf("stored cpu small=%d large=%d", smallEnv.CPUMillis, largeEnv.CPUMillis)
	}
}

func TestQueuedSessionWaitsWithout409AndCancelCreatesNoEnvironment(t *testing.T) {
	h := setup(t)
	rt := &env.FakeRuntime{}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}

	first, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := h.e.StartRun("test", "wait", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatalf("claim %v %v", c, err)
	}
	idle, err := h.e.Claim("example/test-repo")
	if err != nil || idle != nil {
		t.Fatalf("queued claim %v %v", idle, err)
	}
	req, err := http.NewRequest(http.MethodPost, h.http.URL+"/jobs/claim", strings.NewReader(`{"repo":"example/test-repo"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("claim http %d", res.StatusCode)
	}

	listReq, err := http.NewRequest(http.MethodGet, h.http.URL+"/sessions?project=test", nil)
	if err != nil {
		t.Fatal(err)
	}
	listReq.Header.Set("Authorization", "Bearer wsec")
	listRes, err := http.DefaultClient.Do(listReq)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listRes.Body.Close() }()
	var listed []store.Session
	if err := json.NewDecoder(listRes.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	foundWait := false
	for _, sess := range listed {
		if sess.ID == waiting && sess.Wait != "lease" {
			t.Fatalf("waiting session %+v", sess)
		}
		if sess.ID == waiting {
			foundWait = true
		}
	}
	if !foundWait {
		t.Fatal("waiting session missing from list")
	}

	waitingSess, err := store.GetSession(h.st, waiting)
	if err != nil {
		t.Fatal(err)
	}
	waitingEnv, err := store.GetEnvironment(h.st, waitingSess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if waitingEnv.Handle != "" {
		t.Fatalf("waiting session already has handle %q", waitingEnv.Handle)
	}
	if err := h.e.CancelSession(waiting); err != nil {
		t.Fatal(err)
	}
	waitingEnv, err = store.GetEnvironment(h.st, waitingSess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if waitingEnv.Handle != "" {
		t.Fatalf("cancel created handle %q", waitingEnv.Handle)
	}

	if err := h.e.CancelSession(first); err != nil {
		t.Fatal(err)
	}
	again, err := h.e.Claim("example/test-repo")
	if err != nil || again != nil {
		t.Fatalf("cancelled waiter claimed %+v %v", again, err)
	}
}
