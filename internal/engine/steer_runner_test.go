package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/runner"
	"github.com/sannrox/rusui/internal/store"
)

func TestSteerInterruptsLiveRunnerAndKeepsGuestSession(t *testing.T) {
	h := setup(t)
	h.clk.T = time.Now().UTC()
	sid, err := h.e.StartRun("test", "bug: original prompt", "")
	if err != nil {
		t.Fatal(err)
	}

	promptStarted := make(chan acp.PromptParams, 4)
	permissionRequested := make(chan struct{}, 1)
	permissionWaiting := make(chan struct{}, 1)
	permissionResult := make(chan acp.PermissionOutcome, 1)
	cancelReceived := make(chan acp.SessionCancelParams, 2)
	var resultPath string
	agent := &acp.FakeAgent{
		PromptStarted:       promptStarted,
		PermissionRequested: permissionRequested,
		PermissionResult:    func(out acp.PermissionOutcome) { permissionResult <- out },
		CancelReceived:      cancelReceived,
		PromptHandler: func(p acp.PromptParams, cancel <-chan struct{}, request func(string, any) error) (acp.PromptResult, error) {
			text := p.Prompt[0].Text
			if strings.Contains(text, "bug: original prompt") {
				if err := request(acp.MethodRequestPermission, acp.PermissionParams{
					SessionID: p.SessionID,
					ToolCall:  json.RawMessage(`{"toolCallId":"write-1","title":"write_file"}`),
					Options: []acp.PermOption{
						{OptionID: "allow-once", Name: "Allow", Kind: "allow_once"},
						{OptionID: "reject-once", Name: "Reject", Kind: "reject_once"},
					},
				}); err != nil {
					return acp.PromptResult{}, err
				}
				return acp.PromptResult{StopReason: "cancelled"}, nil
			}
			if strings.HasPrefix(text, "Use the narrow fix") {
				<-cancel
				return acp.PromptResult{StopReason: "cancelled"}, nil
			}
			if err := os.WriteFile(resultPath, []byte(`{"blocked_reason":"steer integration fixture"}`), 0o600); err != nil {
				return acp.PromptResult{}, err
			}
			return acp.PromptResult{StopReason: "end_turn"}, nil
		},
	}
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	agent.In = agentIn
	agent.Out = agentOut
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	go func() { _ = agent.Run() }()
	host := &acp.Client{
		In: clientIn, Out: clientOut, Perm: acp.DenyUnmatched{},
		Wait: func(ctx context.Context, _ acp.PermissionParams) acp.Decision {
			permissionWaiting <- struct{}{}
			<-ctx.Done()
			return acp.Decision{}
		},
	}
	client := &runner.Client{Base: h.http.URL, Bootstrap: "wsec", Repo: "example/test-repo"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runner.OneACPTurn(ctx, client, func(a *runner.Assignment, _ string) (*acp.Client, func(), error) {
			resultPath = a.ResultPath
			return host, func() {
				_ = clientIn.Close()
				_ = clientOut.Close()
			}, nil
		})
	}()
	var original acp.PromptParams
	select {
	case original = <-promptStarted:
	case err := <-done:
		t.Fatalf("runner ended before original ACP prompt: %v", err)
	case <-ctx.Done():
		t.Fatal("original ACP prompt did not start")
	}
	select {
	case <-permissionRequested:
	case <-ctx.Done():
		t.Fatal("fake guest did not request permission")
	}
	select {
	case <-permissionWaiting:
	case <-ctx.Done():
		t.Fatal("host did not wait on the pending permission")
	}

	body, _ := json.Marshal(map[string]any{"prompt": "Use the narrow fix", "steer": true})
	endpoint := h.http.URL + "/sessions/" + strconv.FormatInt(sid, 10) + "/turns"
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer wsec")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(response), `"delivery":"steer"`) {
		t.Fatalf("steer response %d %s", res.StatusCode, response)
	}
	var corrected acp.PromptParams
	select {
	case corrected = <-promptStarted:
	case <-ctx.Done():
		t.Fatal("steered ACP prompt did not start")
	}
	if original.SessionID != corrected.SessionID || corrected.SessionID != "sess-fake" || !strings.HasPrefix(corrected.Prompt[0].Text, "Use the narrow fix") {
		t.Fatalf("prompts original=%+v corrected=%+v", original, corrected)
	}
	select {
	case got := <-cancelReceived:
		if got.SessionID != original.SessionID {
			t.Fatalf("cancel session %q, want %q", got.SessionID, original.SessionID)
		}
	case <-ctx.Done():
		t.Fatal("session/cancel did not reach fake guest")
	}
	secondBody, _ := json.Marshal(map[string]any{"prompt": "Also cover the edge case", "steer": true})
	secondReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(secondBody))
	secondReq.Header.Set("Authorization", "Bearer wsec")
	secondReq.Header.Set("Content-Type", "application/json")
	secondRes, err := http.DefaultClient.Do(secondReq)
	if err != nil {
		t.Fatal(err)
	}
	secondResponse, _ := io.ReadAll(secondRes.Body)
	_ = secondRes.Body.Close()
	if secondRes.StatusCode != http.StatusOK || !strings.Contains(string(secondResponse), `"delivery":"steer"`) {
		t.Fatalf("second steer response %d %s", secondRes.StatusCode, secondResponse)
	}
	var secondCorrected acp.PromptParams
	select {
	case secondCorrected = <-promptStarted:
	case <-ctx.Done():
		t.Fatal("second steered ACP prompt did not start")
	}
	if secondCorrected.SessionID != original.SessionID || !strings.HasPrefix(secondCorrected.Prompt[0].Text, "Also cover the edge case") {
		t.Fatalf("second corrected prompt %+v", secondCorrected)
	}
	select {
	case got := <-cancelReceived:
		if got.SessionID != original.SessionID {
			t.Fatalf("second cancel session %q, want %q", got.SessionID, original.SessionID)
		}
	case <-ctx.Done():
		t.Fatal("second session/cancel did not reach fake guest")
	}
	select {
	case outcome := <-permissionResult:
		if outcome.Outcome.Outcome != "cancelled" || outcome.Outcome.OptionID != "" {
			t.Fatalf("steer approved or selected a pending permission: %+v", outcome)
		}
	case <-ctx.Done():
		t.Fatal("pending permission had no cancellation outcome")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runner turn: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("runner turn did not complete")
	}

	turns, err := store.ListTurnsForSession(h.st, sid)
	if err != nil || len(turns) != 1 || turns[0].State != "completed" || turns[0].LeaseGeneration != 1 {
		t.Fatalf("turn fencing %+v err %v", turns, err)
	}
	var acknowledged, promoted int
	if err := h.st.DB.QueryRow(`SELECT SUM(acknowledged), SUM(promoted) FROM turn_steers WHERE turn_id=?`, turns[0].ID).Scan(&acknowledged, &promoted); err != nil || acknowledged != 2 || promoted != 0 {
		t.Fatalf("delivered steer status acknowledged=%d promoted=%d err=%v", acknowledged, promoted, err)
	}
	actions, err := store.ListActionsForSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	var firstSteerAction, secondSteerAction bool
	for i := range actions {
		if actions[i].Type == "operator.steer" && strings.Contains(actions[i].Body, "Use the narrow fix") {
			firstSteerAction = true
		}
		if actions[i].Type == "operator.steer" && strings.Contains(actions[i].Body, "Also cover the edge case") {
			secondSteerAction = true
		}
	}
	if !firstSteerAction || !secondSteerAction {
		t.Fatalf("operator steer actions missing first=%t second=%t: %+v", firstSteerAction, secondSteerAction, actions)
	}

	h.srv.OperatorTok = "op-tok"
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	operator := &http.Client{Jar: jar}
	login, err := operator.PostForm(h.http.URL+"/console/signin", url.Values{"token": {"op-tok"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = login.Body.Close()
	page, err := operator.Get(h.http.URL + "/console/sessions/" + strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatal(err)
	}
	transcript, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if page.StatusCode != http.StatusOK || !strings.Contains(string(transcript), "Use the narrow fix") || !strings.Contains(string(transcript), "Also cover the edge case") || !strings.Contains(string(transcript), "operator.steer") {
		t.Fatalf("transcript %d %s", page.StatusCode, transcript)
	}
	if _, err := h.e.HeartbeatSteer(turns[0].ID, turns[0].LeaseGeneration, turns[0].ClaimedRevision); err == nil {
		t.Fatal("completed turn accepted a heartbeat")
	}
}
