package engine_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/guest"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/runner"
	"github.com/sannrox/rusui/internal/store"
	"gopkg.in/yaml.v3"
)

func TestGuestSessionSurvivesClientDisconnectAndNextTurn(t *testing.T) {
	h := setup(t)
	h.clk.T = time.Now().UTC()
	var config policy.File
	if err := yaml.Unmarshal(h.e.PolicySnapshot().Raw, &config); err != nil {
		t.Fatal(err)
	}
	p := config.Projects["test"]
	p.Guests = &policy.ProjectGuests{Default: "shikigami", Allowed: []string{"shikigami"}}
	config.Projects["test"] = p
	raw, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.e.ReloadPolicyBytes(raw); err != nil {
		t.Fatal(err)
	}
	sid, err := h.e.StartRun("test", "first client", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	attach := func() (*http.Response, context.CancelFunc) {
		t.Helper()
		readCtx, stop := context.WithCancel(ctx)
		req, err := http.NewRequestWithContext(readCtx, http.MethodGet, fmt.Sprintf("%s/sessions/%d/attach", h.http.URL, sid), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer wsec")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 {
			t.Fatalf("attach %d", res.StatusCode)
		}
		return res, stop
	}
	first, disconnect := attach()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	done := make(chan error, 1)
	client := &runner.Client{Base: h.http.URL, Bootstrap: "wsec", Repo: "example/test-repo"}
	host := func(a *runner.Assignment, _ string) (*acp.Client, func(), error) {
		if a.Guest != "shikigami" || a.GuestSpec.Protocol != "acp" || a.GuestSpec.Argv[0] != "shikigami" {
			return nil, nil, fmt.Errorf("wrong assignment guest: %s %+v", a.Guest, a.GuestSpec)
		}
		clientIn, agentOut := io.Pipe()
		agentIn, clientOut := io.Pipe()
		stop := func() { _ = clientIn.Close(); _ = agentOut.Close(); _ = agentIn.Close(); _ = clientOut.Close() }
		go func() {
			_ = (&acp.FakeAgent{In: agentIn, Out: agentOut, PromptHandler: func(p acp.PromptParams, cancelled <-chan struct{}, _ func(string, any) error) (acp.PromptResult, error) {
				if strings.Contains(p.Prompt[0].Text, "first client") {
					started <- struct{}{}
					select {
					case <-release:
					case <-cancelled:
						return acp.PromptResult{}, fmt.Errorf("client disconnect cancelled guest")
					}
				}
				if err := os.WriteFile(a.ResultPath, []byte(`{"blocked_reason":"attachment lifecycle fixture"}`), 0o600); err != nil {
					return acp.PromptResult{}, err
				}
				return acp.PromptResult{StopReason: "end_turn"}, nil
			}}).Run()
		}()
		return &acp.Client{In: clientIn, Out: clientOut, Rec: &runner.HTTPRecorder{Base: h.http.URL, Token: a.TurnToken, TurnID: a.TurnID}, Perm: acp.DenyUnmatched{}}, stop, nil
	}
	go func() { done <- runner.OneACPTurn(ctx, client, host) }()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("runner ended before prompt: %v", err)
	case <-ctx.Done():
		t.Fatal("guest did not start")
	}
	disconnect()
	_ = first.Body.Close()
	second, detach := attach()
	defer detach()
	defer func() { _ = second.Body.Close() }()
	cancelled, err := store.SessionCancelled(h.st, sid)
	if err != nil || cancelled {
		t.Fatalf("disconnected session cancelled: %v %v", cancelled, err)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("first turn did not finish")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/sessions/%d/turns", h.http.URL, sid), strings.NewReader(`{"prompt":"second client"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("next turn %d", res.StatusCode)
	}
	if err := runner.OneACPTurn(ctx, client, host); err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil || sess.GuestName != "shikigami" || sess.GuestSessionID != "sess-fake" {
		t.Fatalf("resumed session %+v %v", sess, err)
	}
	turns, err := store.ListTurnsForSession(h.st, sid)
	if err != nil || len(turns) != 1 || turns[0].State != "completed" || turns[0].LeaseGeneration != 2 || turns[0].ClaimedRevision != 2 {
		t.Fatalf("turns %+v %v", turns, err)
	}
}

func TestChildSelectedGuestRunsThroughHTTP(t *testing.T) {
	h := setup(t)
	h.clk.T = time.Now().UTC()
	var config policy.File
	if err := yaml.Unmarshal(h.e.PolicySnapshot().Raw, &config); err != nil {
		t.Fatal(err)
	}
	config.Guests = guest.Builtin()
	p := config.Projects["test"]
	p.Guests = &policy.ProjectGuests{Default: "shikigami", Allowed: []string{"shikigami", "grok"}}
	config.Projects["test"] = p
	raw, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.e.ReloadPolicyBytes(raw); err != nil {
		t.Fatal(err)
	}
	parent, err := h.e.StartRun("test", "parent", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/sessions/%d/children", h.http.URL, parent), strings.NewReader(`{"prompt":"child","guest":"grok"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil || res.StatusCode != http.StatusCreated {
		t.Fatalf("child create %d %s %v", res.StatusCode, body, err)
	}
	client := &runner.Client{Base: h.http.URL, Bootstrap: "wsec", Repo: "example/test-repo"}
	for _, want := range []string{"shikigami", "grok"} {
		host := func(a *runner.Assignment, _ string) (*acp.Client, func(), error) {
			if a.Guest != want || a.GuestSpec.Pin != config.Guests[want].Pin {
				return nil, nil, fmt.Errorf("guest assignment %s %+v, want %s", a.Guest, a.GuestSpec, want)
			}
			clientIn, agentOut := io.Pipe()
			agentIn, clientOut := io.Pipe()
			stop := func() { _ = clientIn.Close(); _ = agentOut.Close(); _ = agentIn.Close(); _ = clientOut.Close() }
			go func() {
				_ = (&acp.FakeAgent{In: agentIn, Out: agentOut, PromptHandler: func(_ acp.PromptParams, _ <-chan struct{}, _ func(string, any) error) (acp.PromptResult, error) {
					if err := os.WriteFile(a.ResultPath, []byte(`{"blocked_reason":"two-guest fixture"}`), 0o600); err != nil {
						return acp.PromptResult{}, err
					}
					return acp.PromptResult{StopReason: "end_turn"}, nil
				}}).Run()
			}()
			return &acp.Client{In: clientIn, Out: clientOut, Rec: &runner.HTTPRecorder{Base: h.http.URL, Token: a.TurnToken, TurnID: a.TurnID}, Perm: acp.DenyUnmatched{}}, stop, nil
		}
		if err := runner.OneACPTurn(ctx, client, host); err != nil {
			t.Fatal(err)
		}
	}
	actions, err := store.ListActionsForSession(h.st, parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range actions {
		if a.Type == "child.result" && strings.Contains(a.Body, `"outcome":"completed"`) {
			return
		}
	}
	t.Fatalf("missing child completion receipt: %+v", actions)
}
