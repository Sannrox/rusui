package acp

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"testing"
	"time"
)

func TestSessionNewSendsMCPServers(t *testing.T) {
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = clientOut.Close()
		_ = agentIn.Close()
		_ = agentOut.Close()
	})
	got := make(chan []any, 1)
	go func() {
		_ = (&FakeAgent{In: agentIn, Out: agentOut, SessionMCPServers: func(s []any) { got <- s }}).Run()
	}()
	server := MCPServer{Type: "http", Name: "gateway", URL: "https://gateway.example/mcp", Headers: []HTTPHeader{{Name: "Authorization", Value: "Bearer grant"}}}
	c := &Client{In: clientIn, Out: clientOut, Perm: DenyUnmatched{}, MCPServers: []any{server}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SessionNew(ctx, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(<-got)
	var sent []MCPServer
	if err := json.Unmarshal(raw, &sent); err != nil || len(sent) != 1 || !reflect.DeepEqual(sent[0], server) {
		t.Fatalf("mcpServers %s %v", raw, err)
	}
}
