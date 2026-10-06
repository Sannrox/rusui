package engine_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func TestSecretsFromEnv(t *testing.T) {
	got := engine.SecretsFromEnv([]string{
		"RUSUI_SECRET_NPM_TOKEN=sekrit",
		"RUSUI_SECRET_1BAD=no",
		"RUSUI_WORKER_SECRET=other",
		"PATH=/bin",
	})
	if got["npm_token"] != "sekrit" {
		t.Fatalf("%v", got)
	}
	if _, ok := got["1bad"]; ok {
		t.Fatalf("invalid id accepted: %v", got)
	}
}

func TestInjectsSecretAtWakeAndRemovesOnSleep(t *testing.T) {
	h := setup(t)
	const value = "sekrit-wake-value"
	p, err := policy.Parse([]byte(fixture + "    secrets: [npm_token]\n"))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(p)
	h.e.Secrets = map[string]string{"npm_token": value}
	rt := &env.FakeRuntime{}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
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
	got, ok := rt.LookupSecret(envRow.Handle, "npm_token")
	if !ok || got != value {
		t.Fatalf("guest missing secret %q ok=%v", got, ok)
	}
	for path, data := range rt.Contents[envRow.Handle] {
		if strings.Contains(string(data), value) {
			t.Fatalf("value in workspace %s", path)
		}
	}
	receipts, err := store.ListEnvironmentReceipts(h.st, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range receipts {
		if r.Kind != "secret" || r.State != "succeeded" {
			continue
		}
		found = true
		if !strings.Contains(r.Detail, "npm_token") || !strings.Contains(r.Detail, "turn") {
			t.Fatalf("detail %q", r.Detail)
		}
		if strings.Contains(r.Detail, value) {
			t.Fatalf("value in receipt %q", r.Detail)
		}
	}
	if !found {
		t.Fatalf("missing secret receipt %+v", receipts)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.SleepEnvironment(envRow.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.LookupSecret(envRow.Handle, "npm_token"); ok {
		t.Fatal("secret survived sleep")
	}
	woke, err := h.e.WakeEnvironment(envRow.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, ok = rt.LookupSecret(woke.Handle, "npm_token")
	if !ok || got != value {
		t.Fatalf("guest missing secret after wake %q ok=%v", got, ok)
	}
}

func TestDisallowedSecretRefusedBeforeTurn(t *testing.T) {
	h := setup(t)
	p, err := policy.Parse([]byte(fixture + "    secrets: [npm_token]\n"))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(p)
	h.e.RequestedSecrets = []string{"aws_key"}
	h.putRefresh(issue(1))
	c, err := h.e.Claim("example/test-repo")
	if c != nil || !errors.Is(err, engine.ErrSecret) {
		t.Fatalf("claim %v %v", c, err)
	}
	h.e.RequestedSecrets = nil
	c = h.claim()
	if c == nil {
		t.Fatal("allowed claim failed")
	}
}

func TestClaimJSONIncludesAllowedSecrets(t *testing.T) {
	h := setup(t)
	const value = "sekrit-claim-value"
	p, err := policy.Parse([]byte(fixture + "    secrets: [npm_token]\n"))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(p)
	h.e.Secrets = map[string]string{"npm_token": value}
	if _, err := h.e.StartRun("test", "hi", ""); err != nil {
		t.Fatal(err)
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
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	secrets, _ := out["secrets"].(map[string]any)
	if secrets["npm_token"] != value {
		t.Fatalf("secrets %v", out["secrets"])
	}
}
