package runner

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/guest"
)

func TestGuestHomeOutsideWorkspaceAndRemoved(t *testing.T) {
	for _, kind := range []string{guest.KindCodex, guest.KindClaude, guest.KindShikigami} {
		t.Run(kind, func(t *testing.T) {
			workspace := t.TempDir()
			if err := os.Mkdir(filepath.Join(workspace, ".git"), 0o700); err != nil {
				t.Fatal(err)
			}
			a := &Assignment{Guest: kind, TurnToken: "runner-tok", GuestGrant: "turn-grant"}
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
	argv := guestHomeArgv(&Assignment{Guest: guest.KindShikigami}, entry.Argv, "/tmp/plane-home/state")
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
			a := &Assignment{Guest: kind, TurnToken: "runner-tok", GuestGrant: "fixture-grant", ModelBaseURL: "http://127.0.0.1:1234/model-proxy/v1", GuestSpec: guest.Entry{Argv: []string{harness}}}
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

// A shikigami session's conversation store survives its turns, so a
// follow-up can load the stored conversation, and is removed with the
// environment (#598).
func TestSessionStateOutlivesTurnsUntilEnvironmentDestroyed(t *testing.T) {
	drv := env.Process{Root: t.TempDir()}
	workspace, err := drv.Create("session-env")
	if err != nil {
		t.Fatal(err)
	}
	turn := func(session int64) string {
		t.Helper()
		dir, err := sessionStateDir(nil, &Assignment{Guest: guest.KindShikigami, Workspace: workspace, SessionID: session})
		if err != nil || dir == "" {
			t.Fatalf("state %q %v", dir, err)
		}
		return dir
	}
	first := turn(7)
	if err := os.WriteFile(filepath.Join(first, "conversation"), []byte("kiwi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if again := turn(7); again != first {
		t.Fatalf("follow-up state %s, want %s", again, first)
	}
	if b, err := os.ReadFile(filepath.Join(first, "conversation")); err != nil || string(b) != "kiwi" {
		t.Fatalf("session state lost between turns: %q %v", b, err)
	}
	if other := turn(8); other == first {
		t.Fatal("sessions share guest state")
	}
	realWorkspace, _ := filepath.EvalSymlinks(workspace)
	if rel, err := filepath.Rel(realWorkspace, first); err != nil || !strings.HasPrefix(rel, "..") {
		t.Fatalf("state inside workspace: %s %v", rel, err)
	}
	if err := drv.Destroy(workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("guest state survived environment: %v", err)
	}
	if dir, err := sessionStateDir(nil, &Assignment{Guest: guest.KindShikigami, SessionID: 7}); err != nil || dir != "" {
		t.Fatalf("turn without environment kept state: %q %v", dir, err)
	}
}

// The container keeps the session state in its own /tmp across turns and
// refuses a state directory the guest replaced with a link.
func TestContainerSessionStateReusedAndLinkRefused(t *testing.T) {
	x := &tmpExec{root: t.TempDir()}
	a := &Assignment{Driver: "container", Handle: "ctr", Guest: guest.KindShikigami, SessionID: 7}
	host := filepath.Join(x.root, "rusui-state-s7")
	for range 2 {
		if dir, err := sessionStateDir(x, a); err != nil || dir != "/tmp/rusui-state-s7" {
			t.Fatalf("container state %q %v", dir, err)
		}
		if _, err := os.Stat(host); err != nil {
			t.Fatalf("session state missing: %v", err)
		}
	}
	if err := os.RemoveAll(host); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), host); err != nil {
		t.Fatal(err)
	}
	if dir, err := sessionStateDir(x, a); err == nil {
		t.Fatalf("linked state admitted: %s", dir)
	}
}

// tmpExec runs a guest command on the host with the guest's /tmp mapped to
// root, and maps paths in its output back.
type tmpExec struct{ root string }

func (x *tmpExec) ExecStdio(_ string, argv, _ []string) (io.WriteCloser, io.ReadCloser, func(), error) {
	args := make([]string, len(argv))
	for i, a := range argv {
		args[i] = strings.ReplaceAll(a, "/tmp/", x.root+"/")
	}
	out, _ := exec.Command(args[0], args[1:]...).Output()
	reader, writer := io.Pipe()
	_ = reader.Close()
	return writer, io.NopCloser(strings.NewReader(strings.ReplaceAll(string(out), x.root+"/", "/tmp/"))), func() {}, nil
}

// GuestHost points shikigami at the session state on every turn while the
// rest of the home stays per turn.
func TestGuestHostReusesShikigamiSessionState(t *testing.T) {
	drv := env.Process{Root: t.TempDir()}
	workspace, err := drv.Create("session-env")
	if err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(t.TempDir(), "harness")
	script := "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do [ \"$1\" = --state ] && { printf '%s %s\\n' \"$2\" \"$HOME\"; exit; }; shift; done\n"
	if err := os.WriteFile(harness, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	turn := func() []string {
		t.Helper()
		a := &Assignment{Guest: guest.KindShikigami, Workspace: workspace, SessionID: 7, ModelBaseURL: "http://127.0.0.1:1/model-proxy", GuestSpec: guest.Entry{Argv: []string{harness, "--state", "./state", "acp"}}}
		host, stop, err := GuestHost(&Client{})(a, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		out, err := io.ReadAll(host.In)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Fields(string(out))
	}
	first, second := turn(), turn()
	if len(first) != 2 || len(second) != 2 || first[0] != second[0] || first[1] == second[1] {
		t.Fatalf("state/home per turn: %v then %v", first, second)
	}
	if strings.HasPrefix(first[0], first[1]) {
		t.Fatalf("state kept in the per-turn home: %v", first)
	}
}
