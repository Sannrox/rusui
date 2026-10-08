package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func TestGatewayBothDialectsUseOneOriginAndRecordDestination(t *testing.T) {
	headers := make(chan http.Header, 2)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		if r.URL.Query().Get("retry") == "1" {
			w.WriteHeader(429)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer up.Close()
	origin, err := url.Parse(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	e, clk, token := leasedTurn(t)
	hs := httptest.NewServer((&Server{Eng: e, ModelOrigin: origin, ModelProvider: ProviderAnthropic, ModelKey: "gateway-key"}).Handler())
	defer hs.Close()
	for i := range 40 {
		path := []string{"/v1/chat/completions", "/v1/messages"}[i%2]
		req, err := http.NewRequest(http.MethodPost, hs.URL+"/model-proxy"+path, strings.NewReader(`{"secret":"not a receipt"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("proxy %d", res.StatusCode)
		}
		h := <-headers
		if path == "/v1/messages" {
			if h.Get("X-Api-Key") != "gateway-key" || h.Get("Authorization") != "" {
				t.Fatalf("Anthropic authentication %+v", h)
			}
		} else {
			if h.Get("Authorization") != "Bearer gateway-key" || h.Get("X-Api-Key") != "" {
				t.Fatalf("OpenAI authentication %+v", h)
			}
		}
	}
	actions, err := store.ListActions(e.Store, "example/test-repo", 8)
	if err != nil || len(actions) != 0 {
		t.Fatalf("model calls wrote actions before terminal: %+v %v", actions, err)
	}
	var sessionID int64
	if err := e.Store.DB.QueryRow(`SELECT session_id FROM turns ORDER BY id DESC LIMIT 1`).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	if err := e.CancelSession(sessionID); err != nil {
		t.Fatal(err)
	}
	actions, err = store.ListActions(e.Store, "example/test-repo", 8)
	if err != nil || len(actions) != 1 {
		t.Fatalf("model receipts %+v %v", actions, err)
	}
	var receipt struct {
		Count        int         `json:"count"`
		StatusCounts map[int]int `json:"status_counts"`
		Calls        []struct {
			Origin string `json:"origin"`
			Path   string `json:"path"`
			Status int    `json:"status"`
			Count  int    `json:"count"`
		} `json:"calls"`
	}
	action := actions[0]
	if err := json.Unmarshal([]byte(action.Body), &receipt); err != nil {
		t.Fatal(err)
	}
	if action.Type != "model.proxy" || receipt.Count != 40 || receipt.StatusCounts[200] != 40 || len(receipt.Calls) != 2 || strings.Contains(action.Body, "secret") || strings.Contains(action.Body, "gateway-key") {
		t.Fatalf("receipt %+v", action)
	}
	for _, call := range receipt.Calls {
		if call.Origin != up.URL || call.Status != 200 || call.Count != 20 {
			t.Fatalf("call %+v", call)
		}
	}
	if receipt.Calls[0].Path != "/v1/chat/completions" || receipt.Calls[1].Path != "/v1/messages" {
		t.Fatalf("paths %+v", receipt.Calls)
	}
	var turnID int64
	if err := e.Store.DB.QueryRow(`SELECT id FROM turns WHERE session_id=?`, sessionID).Scan(&turnID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Store.DB.Exec(`UPDATE turns SET state='leased',lease_generation=2 WHERE id=?`, turnID); err != nil {
		t.Fatal(err)
	}
	token, _, err = issueTurnToken(e.Store, turnID, 2, clk.T)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		req, _ := http.NewRequest(http.MethodPost, hs.URL+"/model-proxy/v1/messages?retry=1", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		<-headers
		if res.StatusCode != 429 {
			t.Fatalf("reused turn model status %d", res.StatusCode)
		}
	}
	if err := e.CancelSession(sessionID); err != nil {
		t.Fatal(err)
	}
	actions, err = store.ListActions(e.Store, "example/test-repo", 8)
	if err != nil || len(actions) != 1 {
		t.Fatalf("reused turn grew receipts: %+v %v", actions, err)
	}
	if err := json.Unmarshal([]byte(actions[0].Body), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Count != 42 || receipt.StatusCounts[200] != 40 || receipt.StatusCounts[429] != 2 || len(receipt.Calls) != 3 || strings.Contains(actions[0].Body, "retry") {
		t.Fatalf("reused turn lost model summaries: %s", actions[0].Body)
	}

}

func TestModelProxyResponseDoesNotWaitForStoreWriter(t *testing.T) {
	reached, release := make(chan struct{}), make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(reached)
		<-release
		_, _ = w.Write([]byte("response"))
	}))
	defer up.Close()
	origin, _ := url.Parse(up.URL)
	e, _, token := leasedTurn(t)
	hs := httptest.NewServer((&Server{Eng: e, ModelOrigin: origin}).Handler())
	defer hs.Close()
	response := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, hs.URL+"/model-proxy/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(res.Body)
			_ = res.Body.Close()
			err = readErr
			if err == nil && string(body) != "response" {
				err = fmt.Errorf("response %q", body)
			}
		}
		response <- err
	}()
	<-reached // Authentication has completed before the writer is acquired.
	tx, err := e.Store.DB.Begin()
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	close(release)
	select {
	case err := <-response:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("model response waited for the store writer")
	}
}
