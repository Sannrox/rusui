package engine_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestListAndGetSession(t *testing.T) {
	h := setup(t)
	id, err := h.e.StartRun("test", "watch me", "")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", h.http.URL+"/sessions?project=test", nil)
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("list %d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), `"id":`+strconv.FormatInt(id, 10)) && !strings.Contains(string(b), `"ID":`+strconv.FormatInt(id, 10)) {
		t.Fatalf("list %s", b)
	}
	req, _ = http.NewRequest("GET", h.http.URL+"/sessions/"+strconv.FormatInt(id, 10), nil)
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "watch me") {
		t.Fatalf("get %d %s", res.StatusCode, b)
	}
	req, _ = http.NewRequest("GET", h.http.URL+"/sessions?project=test", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("unauth %d", res.StatusCode)
	}
}

func TestAttachSessionSSE(t *testing.T) {
	h := setup(t)
	id, err := h.e.StartRun("test", "stream", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", h.http.URL+"/sessions/"+strconv.FormatInt(id, 10)+"/attach", nil)
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("%d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	sc := bufio.NewScanner(res.Body)
	var gotSession, gotTurn bool
	for sc.Scan() {
		line := sc.Text()
		if line == "event: session" {
			gotSession = true
		}
		if line == "event: turn" {
			gotTurn = true
		}
		if gotSession && gotTurn {
			cancel()
			break
		}
	}
	if !gotSession {
		t.Fatal("missing session event")
	}
}
