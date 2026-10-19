package server

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/runner"
	"github.com/sannrox/rusui/internal/store"
)

func TestGatewayModeAssignsOnlyTheGateway(t *testing.T) {
	const gateway = "https://gateway.example/mcp"
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	p, err := policy.Parse([]byte(strings.Replace(slackPol, "defaults:\n", "defaults:\n  mcp_gateway: '"+gateway+"'\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(st, p, gh.NewFake(), &clock.Fake{T: time.Unix(1_700_000_000, 0).UTC()})
	e.Env = env.Process{Root: t.TempDir()}
	e.ReloadPolicy(p)
	hs := httptest.NewServer((&Server{Eng: e, WorkerSec: "wsec"}).Handler())
	t.Cleanup(hs.Close)

	a := claim(t, hs, e, false)
	if a.MCPGateway != gateway || len(a.MCPServers) != 1 || a.MCPServers[0] != (runner.MCPServer{Name: policy.MCPGatewayName, URL: gateway}) {
		t.Fatalf("gateway %q servers %+v", a.MCPGateway, a.MCPServers)
	}
	// The session holds only its turn credential; no MCP server secret.
	for _, kv := range runner.DriverEnv(a, t.TempDir(), "/usr/bin") {
		if strings.Contains(strings.ToLower(kv), "mcp") {
			t.Fatalf("session env carries mcp state: %s", kv)
		}
	}
}
