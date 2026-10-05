package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestClaimResendsSameTokenAfterLostResponse(t *testing.T) {
	claimRetryDelay = time.Millisecond
	t.Cleanup(func() { claimRetryDelay = time.Second })
	var mu sync.Mutex
	var tokens []string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ClaimToken string `json:"claim_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		tokens = append(tokens, req.ClaimToken)
		n := len(tokens)
		mu.Unlock()
		if n == 1 {
			// The plane granted the lease but the response never arrives.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(hs.Close)
	cli := &Client{Base: hs.URL, Bootstrap: "wsec", Repo: "example/test-repo"}
	if _, err := cli.Claim(); err != nil {
		t.Fatalf("claim after a lost response: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(tokens) != 2 || tokens[0] == "" || tokens[0] != tokens[1] {
		t.Fatalf("claim tokens %q, want the same non-empty token twice", tokens)
	}
}

func TestClaimGivesUpAfterBoundedResends(t *testing.T) {
	claimRetryDelay = time.Millisecond
	t.Cleanup(func() { claimRetryDelay = time.Second })
	var mu sync.Mutex
	n := 0
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(hs.Close)
	cli := &Client{Base: hs.URL, Bootstrap: "wsec", Repo: "example/test-repo"}
	if _, err := cli.Claim(); err == nil {
		t.Fatal("claim succeeded although every response was lost")
	}
	mu.Lock()
	defer mu.Unlock()
	if n != claimAttempts {
		t.Fatalf("claim sent %d times, want %d", n, claimAttempts)
	}
}
