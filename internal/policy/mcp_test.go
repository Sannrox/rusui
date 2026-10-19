package policy

import (
	"slices"
	"strings"
	"testing"
)

const mcpBase = `
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
projects:
  rusui:
    repos:
      Sannrox/rusui:
        visibility: private
`

func TestMCPServersDirectWithoutGateway(t *testing.T) {
	raw := strings.Replace(mcpBase, "rusui:\n", "rusui:\n    mcp_servers: [{name: docs, url: 'https://tools.example/mcp'}]\n", 1)
	e, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := e.Project("rusui")
	want := []MCPServer{{Name: "docs", URL: "https://tools.example/mcp"}}
	if got := e.SessionMCPServers(p); !slices.Equal(got, want) {
		t.Fatalf("servers %+v", got)
	}
}

func TestMCPGatewayIsTheOnlyServer(t *testing.T) {
	raw := strings.Replace(mcpBase, "defaults:\n", "defaults:\n  mcp_gateway: 'https://gateway.example/mcp'\n", 1)
	e, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	want := []MCPServer{{Name: MCPGatewayName, URL: "https://gateway.example/mcp"}}
	p, _ := e.Project("rusui")
	if got := e.SessionMCPServers(p); !slices.Equal(got, want) {
		t.Fatalf("servers %+v", got)
	}
	// Sessions outside any project are governed too.
	if got := e.SessionMCPServers(Project{}); !slices.Equal(got, want) {
		t.Fatalf("projectless servers %+v", got)
	}
}

func TestMCPParseRejects(t *testing.T) {
	gateway := strings.Replace(mcpBase, "defaults:\n", "defaults:\n  mcp_gateway: 'https://gateway.example/mcp'\n", 1)
	server := func(base, entry string) string {
		return strings.Replace(base, "rusui:\n", "rusui:\n    mcp_servers: ["+entry+"]\n", 1)
	}
	cases := map[string]string{
		"direct in gateway mode": server(gateway, "{name: docs, url: 'https://tools.example/mcp'}"),
		"gateway url as direct":  server(gateway, "{name: docs, url: 'https://gateway.example/mcp'}"),
		"bad gateway url":        strings.Replace(mcpBase, "defaults:\n", "defaults:\n  mcp_gateway: 'ftp://gateway.example'\n", 1),
		"userinfo url":           server(mcpBase, "{name: docs, url: 'https://u:p@tools.example/mcp'}"),
		"reserved name":          server(mcpBase, "{name: gateway, url: 'https://tools.example/mcp'}"),
		"bad name":               server(mcpBase, "{name: 'Docs!', url: 'https://tools.example/mcp'}"),
		"duplicate name":         server(mcpBase, "{name: docs, url: 'https://a.example'}, {name: docs, url: 'https://b.example'}"),
	}
	for name, raw := range cases {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
