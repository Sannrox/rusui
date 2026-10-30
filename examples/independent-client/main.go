package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

// Report is the JSON the example prints after one bounded scenario.
type Report struct {
	ClientServer string          `json:"client_server"`
	Project      string          `json:"project"`
	SessionID    int64           `json:"session_id"`
	Followed     int             `json:"followed_events"`
	LastSeq      int64           `json:"last_seq"`
	Approvals    int             `json:"approvals"`
	Decided      string          `json:"decided,omitempty"`
	Session      json.RawMessage `json:"session"`
	Logs         json.RawMessage `json:"logs"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("independent-client", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL (loopback)")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator bearer")
	project := fs.String("project", "", "project slug")
	prompt := fs.String("prompt", "", "run prompt")
	idem := fs.String("idempotency-key", "", "Idempotency-Key for dispatch")
	followFor := fs.Duration("follow", 2*time.Second, "how long to follow before reconnecting once and stopping")
	decision := fs.String("decision", "", "if a pending approval exists, record allow or deny")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *project == "" || *prompt == "" {
		_, _ = fmt.Fprintln(stderr, "usage: independent-client -project SLUG -prompt TEXT [-url URL] [-token TOKEN] [-idempotency-key KEY] [-follow DURATION] [-decision allow|deny]")
		return 2
	}
	if *token == "" {
		_, _ = fmt.Fprintln(stderr, "independent-client: operator token required")
		return 2
	}
	if *decision != "" && *decision != "allow" && *decision != "deny" {
		_, _ = fmt.Fprintln(stderr, "independent-client: -decision must be allow or deny")
		return 2
	}
	c := &Client{Base: *url, Token: *token}
	ctx := context.Background()
	sid, err := c.Dispatch(ctx, *project, *prompt, *idem)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "dispatch:", err)
		return 1
	}
	followed, lastSeq := 0, int64(0)
	followCtx, cancel := context.WithTimeout(ctx, *followFor)
	fn := func(ev Event) error {
		if ev.Name != "comment" {
			followed++
		}
		return nil
	}
	seq, err := c.Follow(followCtx, sid, 0, fn)
	cancel()
	if seq > lastSeq {
		lastSeq = seq
	}
	if err != nil && !isDone(err) {
		_, _ = fmt.Fprintln(stderr, "follow:", err)
		return 1
	}
	// One reconnect with the last entry cursor (interrupted transport).
	reconnectCtx, reconnectCancel := context.WithTimeout(ctx, time.Second)
	seq, err = c.Follow(reconnectCtx, sid, lastSeq, fn)
	reconnectCancel()
	if seq > lastSeq {
		lastSeq = seq
	}
	if err != nil && !isDone(err) {
		_, _ = fmt.Fprintln(stderr, "reconnect:", err)
		return 1
	}
	approvals, err := c.Approvals(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "approvals:", err)
		return 1
	}
	decided := ""
	if *decision != "" {
		for _, a := range approvals {
			if a.SessionID != nil && *a.SessionID == sid {
				if err := c.Decide(ctx, a.ID, *decision); err != nil {
					_, _ = fmt.Fprintln(stderr, "decide:", err)
					return 1
				}
				decided = a.ID + ":" + *decision
				break
			}
		}
	}
	sess, err := c.Session(ctx, sid)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "session:", err)
		return 1
	}
	logs, err := c.Logs(ctx, sid)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "logs:", err)
		return 1
	}
	rep := Report{
		ClientServer: "same-commit http+sse operator API",
		Project:      *project,
		SessionID:    sid,
		Followed:     followed,
		LastSeq:      lastSeq,
		Approvals:    len(approvals),
		Decided:      decided,
		Session:      sess,
		Logs:         logs,
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func isDone(err error) bool {
	return err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
