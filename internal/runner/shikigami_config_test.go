package runner

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/guest"
)

func TestShikigamiGuestReceivesPlaneModelConfiguration(t *testing.T) {
	workspace := t.TempDir()
	harness := filepath.Join(t.TempDir(), "harness")
	script := `#!/bin/sh
while [ "$#" -gt 0 ]; do
 if [ "$1" = acp ]; then exit 1; fi
 if [ "$1" = --config ]; then shift; cat "$1"; exit; fi
 shift
done
exit 1
`
	if err := os.WriteFile(harness, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	a := &Assignment{Guest: guest.KindShikigami, TurnToken: "fixture-grant", ModelBaseURL: "http://127.0.0.1:1234/model-proxy", GuestModel: "fixture-model", GuestSpec: guest.Entry{Argv: []string{harness, "acp"}}}
	client, stop, err := GuestHost(&Client{})(a, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	out, err := io.ReadAll(client.In)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, field := range []string{`adapter = "local"`, `base_url = "http://127.0.0.1:1234/model-proxy/v1"`, `model = "fixture-model"`, `allow_hosts = ["127.0.0.1"]`, `api_key_env = "OPENAI_API_KEY"`} {
		if !strings.Contains(text, field) {
			t.Fatalf("guest config missing %s: %s", field, text)
		}
	}
	if strings.Contains(text, a.TurnToken) {
		t.Fatal("turn credential persisted in guest config")
	}
}
