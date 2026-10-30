package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDispatchAuthRefused(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer op-tok" {
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"session_id":1,"kind":"run"}`))
	}))
	t.Cleanup(hs.Close)
	c := &Client{Base: hs.URL}
	if _, err := c.Dispatch(context.Background(), "test", "hi", ""); err == nil {
		t.Fatal("missing token accepted")
	} else {
		var he httpError
		if !errors.As(err, &he) || he.status != http.StatusUnauthorized {
			t.Fatalf("missing token: %v", err)
		}
	}
	c.Token = "nope"
	if _, err := c.Dispatch(context.Background(), "test", "hi", ""); err == nil {
		t.Fatal("bad token accepted")
	} else {
		var he httpError
		if !errors.As(err, &he) || he.status != http.StatusUnauthorized {
			t.Fatalf("bad token: %v", err)
		}
	}
	c.Token = "op-tok"
	id, err := c.Dispatch(context.Background(), "test", "hi", "")
	if err != nil || id != 1 {
		t.Fatalf("ok token %d %v", id, err)
	}
}

func TestDispatchExplicitIncompatibility(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer op-tok" {
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		var req struct {
			Kind string `json:"kind"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Kind != "run" {
			http.Error(w, "kind", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"session_id":2,"kind":"run"}`))
	}))
	t.Cleanup(hs.Close)
	c := &Client{Base: hs.URL, Token: "op-tok"}
	payload, _ := json.Marshal(map[string]string{"kind": "review", "prompt": "x"})
	req, _ := http.NewRequest(http.MethodPost, hs.URL+"/projects/test/sessions", strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer op-tok")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "kind") {
		t.Fatalf("incompatible kind %d %s", res.StatusCode, b)
	}
	id, err := c.Dispatch(context.Background(), "test", "x", "")
	if err != nil || id != 2 {
		t.Fatalf("run %d %v", id, err)
	}
}

func TestDispatchIdempotent(t *testing.T) {
	var first string
	nextID := int64(10)
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		id := nextID
		if first == "" {
			first = key
		} else if key == first {
			id = 10
		} else {
			nextID++
			id = nextID
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"session_id":%d,"kind":"run"}`, id)
	}))
	t.Cleanup(hs.Close)
	c := &Client{Base: hs.URL, Token: "op-tok"}
	a, err := c.Dispatch(context.Background(), "test", "hi", "k1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Dispatch(context.Background(), "test", "hi", "k1")
	if err != nil {
		t.Fatal(err)
	}
	if a != b || a != 10 {
		t.Fatalf("idempotent %d %d", a, b)
	}
}

func TestFollowReconnectAfterCursor(t *testing.T) {
	var afterSeen []string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer op-tok" {
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		after := r.URL.Query().Get("after")
		afterSeen = append(afterSeen, after)
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		switch after {
		case "0", "":
			_, _ = fmt.Fprintf(w, "event: session\ndata: {\"session_id\":3,\"kind\":\"run\",\"prompt\":\"p\"}\n\n")
			_, _ = fmt.Fprintf(w, "id: 1\nevent: entry\ndata: {\"seq\":1,\"kind\":\"prompt\",\"body\":\"p\"}\n\n")
			fl.Flush()
		default:
			_, _ = fmt.Fprintf(w, "event: session\ndata: {\"session_id\":3,\"kind\":\"run\",\"prompt\":\"p\"}\n\n")
			_, _ = fmt.Fprintf(w, "event: state\ndata: {\"state\":\"queued\"}\n\n")
			fl.Flush()
		}
	}))
	t.Cleanup(hs.Close)
	c := &Client{Base: hs.URL, Token: "op-tok"}
	var names []string
	seq, err := c.Follow(context.Background(), 3, 0, func(ev Event) error {
		names = append(names, ev.Name)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("seq %d", seq)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = c.Follow(ctx, 3, seq, func(ev Event) error {
		names = append(names, ev.Name)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(afterSeen, ",") != "0,1" {
		t.Fatalf("after %v", afterSeen)
	}
	if strings.Join(names, ",") != "session,entry,session,state" {
		t.Fatalf("events %v", names)
	}
}

func TestDecideAndLogs(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer op-tok" {
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/approvals":
			_, _ = w.Write([]byte(`[{"ID":"a1","SessionID":7,"Type":"approval"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/approvals/a1":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/7/logs":
			_, _ = w.Write([]byte(`[{"ID":"a1","Type":"approval"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/7":
			_, _ = w.Write([]byte(`{"session":{"ID":7,"Prompt":"p"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hs.Close)
	c := &Client{Base: hs.URL, Token: "op-tok"}
	list, err := c.Approvals(context.Background())
	if err != nil || len(list) != 1 || list[0].ID != "a1" {
		t.Fatalf("approvals %+v %v", list, err)
	}
	if err := c.Decide(context.Background(), "a1", "deny"); err != nil {
		t.Fatal(err)
	}
	logs, err := c.Logs(context.Background(), 7)
	if err != nil || !strings.Contains(string(logs), "a1") {
		t.Fatalf("logs %s %v", logs, err)
	}
	sess, err := c.Session(context.Background(), 7)
	if err != nil || !strings.Contains(string(sess), `"ID":7`) {
		t.Fatalf("session %s %v", sess, err)
	}
}
