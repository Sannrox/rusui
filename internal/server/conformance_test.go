package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/guest"
)

func TestQualifyGuestHTTPRequiresWorkerAndReturnsSignedReceipt(t *testing.T) {
	e, _, _ := eventEnv(t)
	key := []byte(strings.Repeat("k", 32))
	e.ConformanceKey = key
	e.Container = env.Container{RT: &env.FakeRuntime{StdioHook: func(string, []string, []string) (io.WriteCloser, io.ReadCloser, func(), error) {
		clientIn, agentOut := io.Pipe()
		agentIn, clientOut := io.Pipe()
		go func() { _ = (&acp.FakeAgent{In: agentIn, Out: agentOut}).Run() }()
		return clientOut, clientIn, func() { _ = clientOut.Close(); _ = agentOut.Close() }, nil
	}}}
	hs := httptest.NewServer((&Server{Eng: e, WorkerSec: "wsec"}).Handler())
	t.Cleanup(hs.Close)
	entry := guest.Entry{Protocol: guest.ProtocolACP, Argv: []string{"second-agent", "acp"}, Probe: []string{"second-agent", "--version"}, Pin: "2", Image: "example/second@sha256:" + strings.Repeat("a", 64)}
	body, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	post := func(token string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("POST", hs.URL+"/guests/conformance", bytes.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = res.Body.Close() })
		return res
	}
	if res := post(""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth %d", res.StatusCode)
	}
	res := post("wsec")
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d %s", res.StatusCode, b)
	}
	var receipt guest.Receipt
	if err := json.NewDecoder(res.Body).Decode(&receipt); err != nil {
		t.Fatal(err)
	}
	if err := receipt.Verify(key, entry); err != nil {
		t.Fatalf("receipt does not verify: %v", err)
	}
}
