package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// HTTPPlane talks to the rusui session/approval HTTP API. It is not a
// second session store.
type HTTPPlane struct {
	Base  string
	Token string
	HTTP  *http.Client
}

func (p *HTTPPlane) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return http.DefaultClient
}

func (p *HTTPPlane) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.Base, "/")+path, body)
	if err != nil {
		return nil, err
	}
	if p.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := p.client().Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusUnauthorized {
		_ = res.Body.Close()
		return nil, fmt.Errorf("auth")
	}
	return res, nil
}

func (p *HTTPPlane) GetSession(ctx context.Context, id int64) (*EditorSession, error) {
	res, err := p.do(ctx, http.MethodGet, "/sessions/"+strconv.FormatInt(id, 10), nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("not found")
	}
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("session: %s", bytes.TrimSpace(b))
	}
	var out struct {
		Session struct {
			ID     int64
			State  string
			Prompt string
		} `json:"session"`
		Env string `json:"environment_state"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &EditorSession{ID: out.Session.ID, State: out.Session.State, Prompt: out.Session.Prompt, Env: out.Env}, nil
}

func (p *HTTPPlane) FollowUp(ctx context.Context, id int64, prompt string) error {
	b, _ := json.Marshal(map[string]string{"prompt": prompt})
	res, err := p.do(ctx, http.MethodPost, "/sessions/"+strconv.FormatInt(id, 10)+"/turns", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 300 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("follow-up: %s", bytes.TrimSpace(raw))
	}
	return nil
}

func (p *HTTPPlane) Cancel(ctx context.Context, id int64) error {
	res, err := p.do(ctx, http.MethodPost, "/sessions/"+strconv.FormatInt(id, 10)+"/cancel", nil)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 300 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("cancel: %s", bytes.TrimSpace(raw))
	}
	return nil
}

func (p *HTTPPlane) Attach(ctx context.Context, id int64) (<-chan EditorUpdate, error) {
	res, err := p.do(ctx, http.MethodGet, "/sessions/"+strconv.FormatInt(id, 10)+"/attach", nil)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		return nil, fmt.Errorf("attach: %s", bytes.TrimSpace(b))
	}
	ch := make(chan EditorUpdate, 8)
	go func() {
		defer close(ch)
		defer func() { _ = res.Body.Close() }()
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		var event string
		for sc.Scan() {
			line := sc.Text()
			if rest, ok := strings.CutPrefix(line, "event: "); ok {
				event = rest
				continue
			}
			if rest, ok := strings.CutPrefix(line, "data: "); ok {
				u := EditorUpdate{Kind: event, Body: json.RawMessage(rest)}
				select {
				case <-ctx.Done():
					return
				case ch <- u:
				}
			}
		}
	}()
	return ch, nil
}

func (p *HTTPPlane) PendingApprovals(ctx context.Context, sessionID int64) ([]EditorApproval, error) {
	res, err := p.do(ctx, http.MethodGet, "/approvals", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("approvals")
	}
	var list []struct {
		ID         string
		SessionID  *int64
		Body       string
		ReasonCode string
	}
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		return nil, err
	}
	var out []EditorApproval
	for _, a := range list {
		if a.SessionID != nil && *a.SessionID == sessionID {
			out = append(out, EditorApproval{ID: a.ID, Body: a.Body, Reason: a.ReasonCode})
		}
	}
	return out, nil
}

func (p *HTTPPlane) Decide(ctx context.Context, actionID, decision string) error {
	b, _ := json.Marshal(map[string]string{"decision": decision})
	res, err := p.do(ctx, http.MethodPost, "/approvals/"+actionID, bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 300 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("decide: %s", bytes.TrimSpace(raw))
	}
	return nil
}
