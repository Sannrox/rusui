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
)

func TestGatewayModeAssignsOnlyTheGateway(t *testing.T) {
	t.Parallel()
	const gateway = "https://gateway.example/mcp"
	st := openStore(t, filepath.Join(t.TempDir(), "t.db"))
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

// A custom egress project's destinations reach the runner on the turn
// assignment, which is the only place the guest link learns them (#681).
func TestCustomEgressAssignsListedDestinations(t *testing.T) {
	t.Parallel()
	st := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	p, err := policy.Parse([]byte(strings.Replace(slackPol, "  test:\n", "  test:\n    egress: custom\n    egress_allow: [registry.example.com]\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(st, p, gh.NewFake(), &clock.Fake{T: time.Unix(1_700_000_000, 0).UTC()})
	e.Env = env.Process{Root: t.TempDir()}
	e.ReloadPolicy(p)
	hs := httptest.NewServer((&Server{Eng: e, WorkerSec: "wsec"}).Handler())
	t.Cleanup(hs.Close)

	if a := claim(t, hs, e, false); len(a.EgressAllow) != 1 || a.EgressAllow[0] != "registry.example.com:443" {
		t.Fatalf("egress_allow %v", a.EgressAllow)
	}
}
