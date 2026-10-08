package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/guest"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/runner"
	"github.com/sannrox/rusui/internal/store"
	"gopkg.in/yaml.v3"
)

// This opt-in proof exercises the real CLI, guest, gateway, and durable cursor.
// It never uses the operator's workspace or guest state.
func TestLiveGuestDisconnectAndResume(t *testing.T) {
	gateway, binary, cli := os.Getenv("RUSUI_LIVE_GATEWAY_URL"), os.Getenv("RUSUI_LIVE_GUEST"), os.Getenv("RUSUI_LIVE_CLI")
	if gateway == "" || binary == "" || cli == "" {
		t.Skip("requires an explicitly configured live gateway, Shikigami binary, and rusui CLI")
	}
	origin, err := url.Parse(gateway)
	if err != nil {
		t.Fatal(err)
	}
	s, _, e := consoleEnv(t)
	e.Clock = clock.Real{}
	s.ModelOrigin, s.ModelKey, s.GuestModel = origin, os.Getenv("RUSUI_LIVE_GATEWAY_KEY"), os.Getenv("RUSUI_LIVE_MODEL")
	s.ModelProvider = ProviderAnthropic
	var config policy.File
	if err := yaml.Unmarshal(e.PolicySnapshot().Raw, &config); err != nil {
		t.Fatal(err)
	}
	config.Guests = guest.Builtin()
	cfg := filepath.Join(t.TempDir(), "guest.toml")
	text := `version = 1
[profile]
name = "local"
[governance]
adapter = "local"
fail_closed = false
[workspace]
adapter = "directory"
root = "."
[network]
egress = "allowlist"
allow_hosts = ["127.0.0.1"]
[tools]
mode = "workspace_exec"
bash_timeout_secs = 60
[run]
max_turns = 8
[events]
adapter = "none"
[model]
adapter = "http"
model = "` + s.GuestModel + `"
api_key_env = "OPENAI_API_KEY"
`
	if err := os.WriteFile(cfg, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	entry := config.Guests[guest.KindShikigami]
	entry.Argv, entry.Probe, entry.Pin = []string{binary, "--config", cfg, "--state", filepath.Join(t.TempDir(), "state"), "acp"}, []string{binary, "--version"}, "1.1.1"
	config.Guests[guest.KindShikigami] = entry
	p := config.Projects["test"]
	p.Repos = nil
	p.Guests = &policy.ProjectGuests{Default: guest.KindShikigami, Allowed: []string{guest.KindShikigami}}
	// The isolated fixture permits its result-writing tool. No GitHub token exists.
	p.Permissions = []policy.AllowRule{{Kind: "other"}}
	config.Projects["test"] = p
	raw, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ReloadPolicyBytes(raw); err != nil {
		t.Fatal(err)
	}
	var grant string
	started, release, attached := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var modelOnce, attachOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	handler := s.Handler()
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			modelOnce.Do(func() {
				grant = r.Header.Get("Authorization")
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
				}
			})
		}
		if strings.HasSuffix(r.URL.Path, "/read/follow") {
			attachOnce.Do(func() { close(attached) })
		}
		handler.ServeHTTP(w, r)
	}))
	defer hs.Close()
	defer unblock()
	// This guest pin reads the URL from its explicit config, not OPENAI_BASE_URL.
	text = strings.Replace(text, `[model]`, `[model]`+"\nbase_url = "+strconv.Quote(hs.URL+"/model-proxy/v1"), 1)
	if err := os.WriteFile(cfg, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	prompt := `This is an isolated transport verification. Do not inspect files or repositories. Use bash once to write {"blocked_reason":"live transport verification"} followed by a newline to the file named by $RUSUI_RESULT. Then reply VERIFIED. No other work.`
	cmd := exec.CommandContext(ctx, cli, "run", "-url", hs.URL, "-token", "wsec", "-project", "test", prompt)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("CLI run: %v", err)
	}
	var created struct {
		SessionID int64 `json:"session_id"`
	}
	if err := json.Unmarshal(out, &created); err != nil || created.SessionID == 0 {
		t.Fatalf("CLI response invalid: %v", err)
	}
	sid := created.SessionID
	read := exec.CommandContext(ctx, cli, "read", "-follow", "-url", hs.URL, "-token", "op-tok", strconv.FormatInt(sid, 10))
	if err := read.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Process.Kill(); _ = read.Wait() }()
	select {
	case <-attached:
	case <-ctx.Done():
		t.Fatal("CLI did not attach")
	}
	t.Log("CLI reader attached")
	client := &runner.Client{Base: hs.URL, Bootstrap: "wsec", Repo: "project:test"}
	done := make(chan error, 1)
	go func() { done <- runner.OneACPTurn(ctx, client, runner.GuestHost(client)) }()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("guest ended before model request: %v", err)
	case <-ctx.Done():
		t.Fatal("model did not start")
	}
	t.Log("live model request started")
	_ = read.Process.Kill()
	_ = read.Wait()
	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/sessions/%d/attach", hs.URL, sid), nil)
	req.Header.Set("Authorization", "Bearer wsec")
	second, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Body.Close() }()
	go func() { _, _ = io.Copy(io.Discard, second.Body) }()
	if second.StatusCode != 200 {
		t.Fatalf("second attach: %d", second.StatusCode)
	}
	cancelled, err := store.SessionCancelled(e.Store, sid)
	if err != nil || cancelled {
		t.Fatal("disconnect cancelled session")
	}
	// A second dialect uses the same plane origin while this turn is leased.
	anthropic := `{"model":"claude-haiku-4-5-20251001","max_tokens":5,"messages":[{"role":"user","content":"Say OK."}]}`
	probe, _ := http.NewRequestWithContext(ctx, "POST", hs.URL+"/model-proxy/v1/messages", strings.NewReader(anthropic))
	probe.Header.Set("Authorization", grant)
	probe.Header.Set("Content-Type", "application/json")
	probe.Header.Set("Anthropic-Version", "2023-06-01")
	probeRes, err := http.DefaultClient.Do(probe)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, probeRes.Body)
	_ = probeRes.Body.Close()
	if probeRes.StatusCode != 200 {
		t.Fatalf("live Anthropic dialect: %d", probeRes.StatusCode)
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("first real turn: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("first turn timeout")
	}
	t.Log("first real turn completed")
	first, err := store.GetSession(e.Store, sid)
	if err != nil || first.GuestSessionID == "" {
		t.Fatalf("guest cursor missing: error=%v sessionID=%d", err, sid)
	}
	payload, _ := json.Marshal(map[string]string{"prompt": prompt})
	req, _ = http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/sessions/%d/turns", hs.URL, sid), strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("next turn: %d", res.StatusCode)
	}
	if err := runner.OneACPTurn(ctx, client, runner.GuestHost(client)); err != nil {
		t.Fatalf("second real turn: %v", err)
	}
	final, err := store.GetSession(e.Store, sid)
	if err != nil || final.GuestSessionID != first.GuestSessionID || final.GuestName != guest.KindShikigami || final.GuestPin != "1.1.1" {
		t.Fatal("guest pin or cursor changed")
	}
	turns, err := store.ListTurnsForSession(e.Store, sid)
	if err != nil || len(turns) != 1 || turns[0].State != "completed" || turns[0].LeaseGeneration != 2 {
		t.Fatal("both turns did not complete")
	}
	if codex := os.Getenv("RUSUI_LIVE_CODEX"); codex != "" {
		native := config.Guests[guest.KindCodex]
		native.Argv = []string{codex, "app-server", "--listen", "stdio://"}
		config.Guests[guest.KindCodex] = native
		p.Guests.Allowed = append(p.Guests.Allowed, guest.KindCodex)
		p.Permissions = append(p.Permissions, policy.AllowRule{Kind: "execute"}, policy.AllowRule{Kind: "edit"})
		config.Projects["test"] = p
		raw, err := yaml.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.ReloadPolicyBytes(raw); err != nil {
			t.Fatal(err)
		}
		nativeCmd := exec.CommandContext(ctx, cli, "run", "-url", hs.URL, "-token", "wsec", "-project", "test", "-guest", "codex", prompt)
		output, err := nativeCmd.Output()
		if err != nil {
			t.Fatalf("native CLI run: %v", err)
		}
		var nativeSession struct {
			SessionID int64 `json:"session_id"`
		}
		if err := json.Unmarshal(output, &nativeSession); err != nil {
			t.Fatal(err)
		}
		if err := runner.OneACPTurn(ctx, client, runner.GuestHost(client)); err != nil {
			actions, _ := store.ListActionsForSession(e.Store, nativeSession.SessionID)
			for _, a := range actions {
				if a.Type == "model.proxy" {
					t.Logf("native gateway receipt: %s", a.Body)
				}
			}
			t.Fatalf("live native Codex: %v", err)
		}
		stored, err := store.GetSession(e.Store, nativeSession.SessionID)
		if err != nil || stored.GuestSessionID == "" {
			t.Fatalf("native cursor missing: %v", err)
		}
		nativeTurns, err := store.ListTurnsForSession(e.Store, nativeSession.SessionID)
		if err != nil || len(nativeTurns) != 1 || nativeTurns[0].State != "completed" {
			t.Fatal("native turn did not complete")
		}
		nativeActions, err := store.ListActionsForSession(e.Store, nativeSession.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		nativeReceipt := false
		for _, action := range nativeActions {
			if action.Type != "model.proxy" {
				continue
			}
			var receipt struct {
				Origin string `json:"origin"`
				Path   string `json:"path"`
				Status int    `json:"status"`
			}
			if err := json.Unmarshal([]byte(action.Body), &receipt); err != nil {
				t.Fatal(err)
			}
			t.Logf("native Responses receipt: origin=%s path=%s status=%d", receipt.Origin, receipt.Path, receipt.Status)
			if receipt.Origin == gateway && receipt.Path == "/v1/responses" && (receipt.Status == 200 || receipt.Status == 101) {
				nativeReceipt = true
			}
		}
		if !nativeReceipt {
			t.Fatal("native Responses gateway receipt missing")
		}
		t.Log("live native Codex turn completed through the plane gateway with a Responses receipt")
	}
	actions, err := store.ListActionsForSession(e.Store, sid)
	if err != nil {
		t.Fatal(err)
	}
	receipts := 0
	dialects := map[string]bool{}
	for _, a := range actions {
		if a.Type == "model.proxy" {
			var r struct {
				Origin string `json:"origin"`
				Status int    `json:"status"`
				Path   string `json:"path"`
			}
			if err := json.Unmarshal([]byte(a.Body), &r); err != nil || r.Origin != gateway || r.Status != 200 {
				t.Fatal("unexpected model destination receipt")
			}
			dialects[r.Path] = true
			receipts++
		}
	}
	if receipts < 3 || !dialects["/v1/messages"] || !dialects["/v1/chat/completions"] {
		t.Fatal("missing gateway receipts")
	}
	t.Logf("CLI-started session; disconnect during live model request; second client attached; two completed turns; stable guest cursor and pin; %d gateway receipts at %s", receipts, gateway)
}
