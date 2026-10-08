package runner

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/guest"
)

func TestGuestHomeOutsideWorkspaceAndRemoved(t *testing.T) {
	for _, kind := range []string{guest.KindCodex, guest.KindClaude, guest.KindShikigami} {
		t.Run(kind, func(t *testing.T) {
			workspace := t.TempDir()
			if err := os.Mkdir(filepath.Join(workspace, ".git"), 0o700); err != nil {
				t.Fatal(err)
			}
			a := &Assignment{Guest: kind, TurnToken: "turn-grant"}
			home, clean, err := prepareGuestHome(nil, a, workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer clean()
			rel, err := filepath.Rel(workspace, home)
			if err != nil || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..") {
				t.Fatalf("home inside workspace: %s %v", rel, err)
			}
			env := strings.Join(DriverEnv(a, home, "/bin"), "\n")
			if !strings.Contains(env, "HOME="+home) {
				t.Fatalf("missing home: %s", env)
			}
			if kind == guest.KindCodex && !strings.Contains(env, "CODEX_HOME="+home) {
				t.Fatalf("missing Codex home: %s", env)
			}
			if kind == guest.KindClaude && !strings.Contains(env, "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".rusui-claude")) {
				t.Fatalf("missing Claude config: %s", env)
			}
			if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte("turn-grant"), 0o600); err != nil {
				t.Fatal(err)
			}
			clean()
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatalf("guest home survived cleanup: %v", err)
			}
		})
	}
}

func TestCodexWorkspaceConfigurationRefusedBeforeHomeCreated(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".codex", "config.toml"), []byte(`model = "repo-model"`), 0o600); err != nil {
		t.Fatal(err)
	}
	home, clean, err := prepareGuestHome(nil, &Assignment{Guest: guest.KindCodex}, workspace)
	if err == nil || home != "" || clean != nil {
		t.Fatalf("repo config admitted: %s %v", home, err)
	}
}

func TestShikigamiStateArgvMovesOutsideWorkspace(t *testing.T) {
	entry := guest.Builtin()[guest.KindShikigami]
	argv := guestHomeArgv(&Assignment{Guest: guest.KindShikigami}, entry.Argv, "/tmp/plane-home")
	if strings.Join(argv, " ") != "shikigami --state /tmp/plane-home/state acp" {
		t.Fatalf("state argv: %v", argv)
	}
	if entry.Argv[2] != "./state" {
		t.Fatal("frozen registry entry mutated")
	}
}

func TestGuestHomeRefusesTemporaryRootInsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("TMPDIR", workspace)
	home, clean, err := prepareGuestHome(nil, &Assignment{Guest: guest.KindClaude}, workspace)
	if err == nil || home != "" || clean != nil {
		t.Fatalf("workspace home admitted: %s %v", home, err)
	}
	files, err := os.ReadDir(workspace)
	if err != nil || len(files) != 0 {
		t.Fatalf("refused home leaked: %v %v", files, err)
	}
}

func TestGuestHostKeepsCredentialFilesOutsideWorkspace(t *testing.T) {
	for _, kind := range []string{guest.KindCodex, guest.KindClaude, guest.KindShikigami} {
		t.Run(kind, func(t *testing.T) {
			workspace := t.TempDir()
			if err := os.Mkdir(filepath.Join(workspace, ".git"), 0o700); err != nil {
				t.Fatal(err)
			}
			harness := filepath.Join(t.TempDir(), "harness")
			script := `#!/bin/sh
config=${CODEX_HOME:-${CLAUDE_CONFIG_DIR:-$HOME}}
while [ "$#" -gt 0 ]; do
 if [ "$1" = --state ]; then shift; config=$1; break; fi
 shift
done
mkdir -p "$config" || exit 1
printf '%s' "$RUSUI_TURN_TOKEN" > "$config/auth.json" || exit 1
printf '%s\n' "$config"
`
			if err := os.WriteFile(harness, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			a := &Assignment{Guest: kind, TurnToken: "fixture-grant", ModelBaseURL: "http://127.0.0.1:1234/model-proxy/v1", GuestSpec: guest.Entry{Argv: []string{harness}}}
			host, stop, err := GuestHost(&Client{})(a, workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			out, err := io.ReadAll(host.In)
			if err != nil {
				t.Fatal(err)
			}
			config := strings.TrimSpace(string(out))
			realWorkspace, err := filepath.EvalSymlinks(workspace)
			if err != nil {
				t.Fatal(err)
			}
			realConfig, err := filepath.EvalSymlinks(config)
			if err != nil {
				t.Fatal(err)
			}
			rel, err := filepath.Rel(realWorkspace, realConfig)
			if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
				t.Fatalf("guest credential path in workspace: %s %v", config, err)
			}
			auth, err := os.ReadFile(filepath.Join(config, "auth.json"))
			if err != nil || string(auth) != "fixture-grant" {
				t.Fatalf("harness auth file: %s %v", auth, err)
			}
			stop()
			if _, err := os.Stat(config); !os.IsNotExist(err) {
				t.Fatalf("guest state survived stop: %v", err)
			}
			files, err := os.ReadDir(workspace)
			if err != nil || len(files) != 1 || files[0].Name() != ".git" {
				t.Fatalf("guest wrote workspace credentials: %v %v", files, err)
			}
		})
	}
}
