package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// plane is the slice of the plane API the runner uses: admit a pinned
// run (worker secret), read its turn measurements, and read the
// publication the plane observed (operator token).
type plane struct {
	url, worker, operator string
	http                  *http.Client
	poll                  time.Duration
}

type measurement struct {
	TurnID             int64  `json:"turn_id"`
	SessionID          int64  `json:"session_id"`
	TerminalState      string `json:"terminal_state"`
	PermissionDecision string `json:"permission_decision"`
	TokensIn           *int64 `json:"tokens_in"`
	TokensOut          *int64 `json:"tokens_out"`
	Publication        string `json:"publication"`
}

type publication struct {
	PullRequest int    `json:"pull_request"`
	SHA         string `json:"sha"`
}

// admit starts the task at its own base. The effort key is stable for one
// candidate and corpus, so a rerun after a crash resumes the same session
// instead of admitting the task twice.
func (p *plane) admit(ctx context.Context, c *Corpus, t Task, guest, effort string) (int64, error) {
	body := map[string]any{
		"kind": "run", "prompt": t.Prompt, "effort_key": effort,
		"repo": c.Repo, "ref": c.Ref, "base_sha": t.BaseSHA, "allowed_paths": t.AllowedPaths,
	}
	if guest != "" {
		body["guest"] = guest
	}
	var out struct {
		SessionID int64 `json:"session_id"`
	}
	if err := p.do(ctx, p.worker, http.MethodPost, "/projects/"+c.Project+"/sessions", body, &out); err != nil {
		return 0, err
	}
	return out.SessionID, nil
}

// wait polls until the session has a finished turn and no queued or
// leased one, and returns its finished turns, oldest first. The plane
// lists only finished turns in measurements.
func (p *plane) wait(ctx context.Context, project string, session int64) ([]measurement, error) {
	for {
		var agg struct {
			Measurements []measurement `json:"measurements"`
		}
		if err := p.do(ctx, p.operator, http.MethodGet, "/projects/"+project+"/measurements", nil, &agg); err != nil {
			return nil, err
		}
		var mine []measurement
		for _, m := range agg.Measurements {
			if m.SessionID == session {
				mine = append(mine, m)
			}
		}
		if len(mine) > 0 {
			busy, err := p.busy(ctx, session)
			if err != nil {
				return nil, err
			}
			if !busy {
				return mine, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(p.poll):
		}
	}
}

// busy reports whether the session still has a turn to run.
func (p *plane) busy(ctx context.Context, session int64) (bool, error) {
	var out struct {
		Turns []struct {
			State string
		} `json:"turns"`
	}
	if err := p.do(ctx, p.operator, http.MethodGet, fmt.Sprintf("/sessions/%d", session), nil, &out); err != nil {
		return false, err
	}
	for _, t := range out.Turns {
		if t.State == "queued" || t.State == "leased" {
			return true, nil
		}
	}
	return false, nil
}

// published is the head the plane confirmed on GitHub, if any. A blocked
// or unconfirmed turn has none.
func (p *plane) published(ctx context.Context, session int64) (*publication, error) {
	var out struct {
		Publication *publication `json:"publication"`
	}
	if err := p.do(ctx, p.operator, http.MethodGet, fmt.Sprintf("/sessions/%d", session), nil, &out); err != nil {
		return nil, err
	}
	return out.Publication, nil
}

func (p *plane) do(ctx context.Context, token, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.url, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s %s", method, path, res.Status, bytes.TrimSpace(b))
	}
	return json.Unmarshal(b, out)
}
