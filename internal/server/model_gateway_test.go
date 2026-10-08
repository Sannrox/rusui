package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/store"
)

func TestGatewayBothDialectsUseOneOriginAndRecordDestination(t *testing.T) {
	headers := make(chan http.Header, 2)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer up.Close()
	origin, err := url.Parse(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	e, _, token := leasedTurn(t)
	hs := httptest.NewServer((&Server{Eng: e, ModelOrigin: origin, ModelProvider: ProviderAnthropic, ModelKey: "gateway-key"}).Handler())
	defer hs.Close()
	for _, path := range []string{"/v1/chat/completions", "/v1/messages"} {
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
	if err != nil || len(actions) != 2 {
		t.Fatalf("model receipts %+v %v", actions, err)
	}
	for _, action := range actions {
		var receipt struct {
			Origin string `json:"origin"`
			Status int    `json:"status"`
		}
		if err := json.Unmarshal([]byte(action.Body), &receipt); err != nil {
			t.Fatal(err)
		}
		if action.Type != "model.proxy" || receipt.Origin != up.URL || receipt.Status != 200 || strings.Contains(action.Body, "secret") || strings.Contains(action.Body, "gateway-key") {
			t.Fatalf("receipt %+v", action)
		}
	}
}
