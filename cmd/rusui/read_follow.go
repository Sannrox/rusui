package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	// followIdle is how long a followed read waits without any byte from
	// the plane, heartbeats included, before it treats the connection as
	// lost and reconnects.
	followIdle       = 45 * time.Second
	followBackoffMin = 250 * time.Millisecond
	followBackoffMax = 10 * time.Second
)

type followSessionV struct {
	SessionID int64  `json:"session_id"`
	Kind      string `json:"kind"`
	Prompt    string `json:"prompt"`
}

type followEntryV struct {
	Seq  int64  `json:"seq"`
	Kind string `json:"kind"`
	Body string `json:"body"`
}

type followStateV struct {
	State    string `json:"state"`
	TurnID   int64  `json:"turn_id"`
	Revision int    `json:"revision"`
}

// followTurnV is a turn record: a turn is ready, waiting on an approval,
// or blocked (#491).
type followTurnV struct {
	SessionID   int64  `json:"session_id"`
	TurnID      int64  `json:"turn_id"`
	State       string `json:"state"`
	Revision    int    `json:"revision"`
	ApprovalSeq int64  `json:"approval_seq"`
}

// followRefused is a plane answer that reconnecting cannot change.
type followRefused struct{ msg string }

func (e followRefused) Error() string { return e.msg }

// followReader is one followed read across reconnects. It keeps the
// cursor of the last printed entry, so a reconnect resumes after it and
// the gap is printed once.
type followReader struct {
	client     *http.Client
	url, token string
	id         string
	stdout     io.Writer

	cursor  int64
	header  bool
	state   followStateV
	turn    followTurnV
	end     *followStateV
	receive bool
}

func followRead(url, token, id string, stdout, stderr io.Writer) int {
	client, err := planeHTTP(url)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	f := &followReader{client: client, url: strings.TrimRight(url, "/"), token: token, id: id, stdout: stdout}
	connected := false
	backoff := followBackoffMin
	for {
		f.receive = false
		err := f.once()
		if f.end != nil {
			if f.end.State == "completed" {
				return 0
			}
			return 1
		}
		var refused followRefused
		if errors.As(err, &refused) || !connected && !f.receive {
			_, _ = fmt.Fprintf(stderr, "read: %v\n", err)
			return 1
		}
		connected = true
		if f.receive {
			backoff = followBackoffMin
		}
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		_, _ = fmt.Fprintf(stderr, "read: stream interrupted (%v); reconnecting\n", err)
		time.Sleep(backoff)
		backoff = min(2*backoff, followBackoffMax)
	}
}

// once runs one connection until the plane ends the stream, the
// connection fails, or no byte arrives for followIdle.
func (f *followReader) once() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	idle := time.AfterFunc(followIdle, cancel)
	defer idle.Stop()
	path := "/sessions/" + f.id + "/read/follow?after=" + strconv.FormatInt(f.cursor, 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url+path, nil)
	if err != nil {
		return followRefused{err.Error()}
	}
	if f.token != "" {
		req.Header.Set("Authorization", "Bearer "+f.token)
	}
	res, err := f.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return errors.New("no response from the plane")
		}
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		msg := bytesTrim(b)
		if res.StatusCode == http.StatusNotFound && strings.Contains(msg, "page not found") {
			return followRefused{"plane does not support read -follow"}
		}
		err := fmt.Errorf("%s %s", res.Status, msg)
		if res.StatusCode >= 500 {
			return err
		}
		return followRefused{err.Error()}
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		return followRefused{"plane did not return a session stream"}
	}
	reader := bufio.NewReader(res.Body)
	event := ""
	var data []string
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			idle.Reset(followIdle)
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		switch {
		case line == "":
			if event != "" && len(data) > 0 {
				if derr := f.handle(event, strings.Join(data, "\n")); derr != nil {
					return derr
				}
				if f.end != nil {
					return nil
				}
			}
			event, data = "", nil
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("no data from the plane for %s", followIdle)
			}
			if errors.Is(err, io.EOF) {
				return io.ErrUnexpectedEOF
			}
			return err
		}
	}
}

func (f *followReader) handle(event, data string) error {
	switch event {
	case "session":
		var s followSessionV
		if err := json.Unmarshal([]byte(data), &s); err != nil {
			return fmt.Errorf("decode session: %w", err)
		}
		f.receive = true
		if !f.header {
			f.header = true
			_, _ = fmt.Fprintf(f.stdout, "session %d %s\nprompt: %s\n", s.SessionID, visibleText(s.Kind), visibleText(s.Prompt))
		}
	case "entry":
		var e followEntryV
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return fmt.Errorf("decode entry: %w", err)
		}
		f.receive = true
		if e.Seq <= f.cursor {
			return nil
		}
		f.cursor = e.Seq
		_, _ = fmt.Fprintf(f.stdout, "%s\n%s\n", visibleText(e.Kind), visibleText(e.Body))
	case "state", "end":
		var st followStateV
		if err := json.Unmarshal([]byte(data), &st); err != nil {
			return fmt.Errorf("decode %s: %w", event, err)
		}
		f.receive = true
		if st != f.state {
			f.state = st
			_, _ = fmt.Fprintln(f.stdout, formatFollowState(st))
		}
		if event == "end" {
			f.end = &st
		}
	case "turn":
		var tr followTurnV
		if err := json.Unmarshal([]byte(data), &tr); err != nil {
			return fmt.Errorf("decode turn: %w", err)
		}
		f.receive = true
		// A reconnect resends the current record; it is printed once.
		if tr != f.turn {
			f.turn = tr
			_, _ = fmt.Fprintln(f.stdout, formatFollowTurn(tr))
		}
	}
	return nil
}

// formatFollowTurn is the one line a client can match on without reading
// transcript text: "turn: STATE session=ID turn=ID revision=N", plus
// "approval=SEQ" for the approval a waiting turn needs answered.
func formatFollowTurn(tr followTurnV) string {
	line := fmt.Sprintf("turn: %s session=%d turn=%d revision=%d", visibleText(tr.State), tr.SessionID, tr.TurnID, tr.Revision)
	if tr.ApprovalSeq != 0 {
		line += fmt.Sprintf(" approval=%d", tr.ApprovalSeq)
	}
	return line
}

func formatFollowState(st followStateV) string {
	line := "state: " + visibleText(st.State)
	if st.TurnID != 0 {
		line += fmt.Sprintf(" (turn %d revision %d)", st.TurnID, st.Revision)
	}
	return line
}
