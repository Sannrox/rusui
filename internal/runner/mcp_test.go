package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/guest"
)

const testGateway = "https://gateway.example/mcp"

func gatewayAssignment(kind string) *Assignment {
	return &Assignment{
		Guest:      kind,
		GuestGrant: "grant-secret-value-0123456789",
		MCPGateway: testGateway,
		MCPServers: []MCPServer{{Name: "gateway", URL: testGateway}},
	}
}

func TestGatewayModeRefusesDirectServer(t *testing.T) {
	a := gatewayAssignment(guest.KindGrok)
	a.MCPServers = append(a.MCPServers, MCPServer{Name: "docs", URL: "https://tools.example/mcp"})
	// The refusal comes before the guest home or process exists.
	if _, _, err := GuestHost(&Client{})(a, t.TempDir()); err == nil || !strings.Contains(err.Error(), "refused in gateway mode") {
		t.Fatalf("direct server accepted: %v", err)
	}
}

func TestGatewayACPEntryCarriesTurnCredential(t *testing.T) {
	a := gatewayAssignment(guest.KindGrok)
	got := acpMCPServers(a, a.MCPServers)
	want := acp.MCPServer{Type: "http", Name: "gateway", URL: testGateway, Headers: []acp.HTTPHeader{{Name: "Authorization", Value: "Bearer " + a.GuestGrant}}}
	if len(got) != 1 || !slices.Equal(got[0].(acp.MCPServer).Headers, want.Headers) || got[0].(acp.MCPServer).URL != want.URL {
		t.Fatalf("entries %+v", got)
	}
	// A direct server outside gateway mode gets no plane credential.
	direct := &Assignment{GuestGrant: a.GuestGrant, MCPServers: []MCPServer{{Name: "docs", URL: "https://tools.example/mcp"}}}
	if h := acpMCPServers(direct, direct.MCPServers)[0].(acp.MCPServer).Headers; len(h) != 0 {
		t.Fatalf("direct headers %+v", h)
	}
}

func TestNativeGatewayArgvReferencesCredentialByName(t *testing.T) {
	for _, kind := range []string{guest.KindClaude, guest.KindCodex} {
		a := gatewayAssignment(kind)
		argv := strings.Join(mcpArgv(a, nil, a.MCPServers), " ")
		if strings.Contains(argv, a.GuestGrant) {
			t.Fatalf("%s argv leaks the credential: %s", kind, argv)
		}
		if !strings.Contains(argv, testGateway) || !strings.Contains(argv, turnTokenEnv) {
			t.Fatalf("%s argv %s", kind, argv)
		}
	}
}
