// Independent HTTP+SSE client for the 1.0 operator API (issue #144).
// It uses only the Go standard library and the published endpoints in
// docs/configuration.md. It does not import rusui internal packages and
// does not open the plane database.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Client talks to one plane over HTTP+SSE with a single operator bearer.
type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) url(path string) string {
	return strings.TrimRight(c.Base, "/") + path
}

type httpError struct {
	status int
	body   string
}

func (e httpError) Error() string {
	return fmt.Sprintf("plane %d: %s", e.status, strings.TrimSpace(e.body))
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return c.http().Do(req)
}

func (c *Client) readJSON(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), rdr)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, res.StatusCode, err
	}
	return b, res.StatusCode, nil
}

// Dispatch creates a run session. An empty idempotency key is omitted.
// Repeating the same key returns the same session id.
func (c *Client) Dispatch(ctx context.Context, project, prompt, idem string) (int64, error) {
	payload, err := json.Marshal(map[string]string{"kind": "run", "prompt": prompt})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/projects/"+project+"/sessions"), bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	res, err := c.do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, err
	}
	if res.StatusCode >= 300 {
		return 0, httpError{res.StatusCode, string(b)}
	}
	var out struct {
		SessionID int64  `json:"session_id"`
		Kind      string `json:"kind"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return 0, fmt.Errorf("dispatch body: %w", err)
	}
	if out.SessionID == 0 {
		return 0, fmt.Errorf("dispatch: missing session_id")
	}
	return out.SessionID, nil
}

// Session returns GET /sessions/{id} as JSON.
func (c *Client) Session(ctx context.Context, id int64) (json.RawMessage, error) {
	b, status, err := c.readJSON(ctx, http.MethodGet, "/sessions/"+strconv.FormatInt(id, 10), nil)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, httpError{status, string(b)}
	}
	return b, nil
}

// Logs returns GET /sessions/{id}/logs as JSON (action receipts).
func (c *Client) Logs(ctx context.Context, id int64) (json.RawMessage, error) {
	b, status, err := c.readJSON(ctx, http.MethodGet, "/sessions/"+strconv.FormatInt(id, 10)+"/logs", nil)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, httpError{status, string(b)}
	}
	return b, nil
}

// Approval is one pending approval row as the plane encodes it.
type Approval struct {
	ID        string `json:"ID"`
	SessionID *int64 `json:"SessionID"`
	Type      string `json:"Type"`
}

// Approvals lists pending approvals.
func (c *Client) Approvals(ctx context.Context) ([]Approval, error) {
	b, status, err := c.readJSON(ctx, http.MethodGet, "/approvals", nil)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, httpError{status, string(b)}
	}
	var list []Approval
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("approvals body: %w", err)
	}
	return list, nil
}

// Decide records allow or deny for one approval. The plane answers 204.
func (c *Client) Decide(ctx context.Context, actionID, decision string) error {
	payload, err := json.Marshal(map[string]string{"decision": decision})
	if err != nil {
		return err
	}
	b, status, err := c.readJSON(ctx, http.MethodPost, "/approvals/"+actionID, payload)
	if err != nil {
		return err
	}
	if status >= 300 {
		return httpError{status, string(b)}
	}
	return nil
}

// Event is one SSE frame from GET /sessions/{id}/read/follow.
type Event struct {
	ID   string
	Name string
	Data string
}

// Follow reads one follow stream until ctx is done, the plane ends it, or
// the connection drops. lastSeq is the last entry id, for reconnect.
func (c *Client) Follow(ctx context.Context, id, after int64, fn func(Event) error) (lastSeq int64, err error) {
	lastSeq = after
	path := "/sessions/" + strconv.FormatInt(id, 10) + "/read/follow?after=" + strconv.FormatInt(after, 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(path), nil)
	if err != nil {
		return lastSeq, err
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := c.do(req)
	if err != nil {
		return lastSeq, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return lastSeq, httpError{res.StatusCode, string(b)}
	}
	r := bufio.NewReader(res.Body)
	var ev Event
	for {
		line, readErr := r.ReadString('\n')
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		switch {
		case line == "":
			if ev.Name != "" || ev.Data != "" {
				if ev.Name == "entry" {
					var entry struct {
						Seq int64 `json:"seq"`
					}
					if json.Unmarshal([]byte(ev.Data), &entry) == nil && entry.Seq > lastSeq {
						lastSeq = entry.Seq
					}
				}
				if fn != nil {
					if err := fn(ev); err != nil {
						return lastSeq, err
					}
				}
				if ev.Name == "end" {
					return lastSeq, io.EOF
				}
			}
			ev = Event{}
		case strings.HasPrefix(line, ": "):
			if fn != nil {
				if err := fn(Event{Name: "comment", Data: strings.TrimPrefix(line, ": ")}); err != nil {
					return lastSeq, err
				}
			}
		case strings.HasPrefix(line, "id: "):
			ev.ID = strings.TrimPrefix(line, "id: ")
			if n, convErr := strconv.ParseInt(ev.ID, 10, 64); convErr == nil && n > lastSeq {
				lastSeq = n
			}
		case strings.HasPrefix(line, "event: "):
			ev.Name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			ev.Data = strings.TrimPrefix(line, "data: ")
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return lastSeq, nil
			}
			return lastSeq, readErr
		}
	}
}
