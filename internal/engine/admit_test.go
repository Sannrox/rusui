package engine_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

// admitHook installs script as project test's admit hook. The hook appends
// its stdin and environment to files in dir, since it gets only PATH.
func admitHook(t *testing.T, h *harn, script string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	path := filepath.Join(dir, "admit")
	body := "#!/bin/sh\ncat >> " + filepath.Join(dir, "stdin") + "\necho >> " + filepath.Join(dir, "stdin") +
		"\nenv > " + filepath.Join(dir, "env") + "\n" + script + "\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	h.e.AdmitHooks = map[string]string{"test": path}
	return dir
}

func countRows(t *testing.T, h *harn, query string, args ...any) int {
	t.Helper()
	var n int
	if err := h.st.DB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func hookRuns(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "stdin"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(strings.TrimSpace(string(b)))
}

func TestAdmitHookRefusalLeavesNoSession(t *testing.T) {
	h := setup(t)
	t.Setenv("RUSUI_OPERATOR_TOKEN", "operator-secret")
	dir := admitHook(t, h, "echo 'over budget for today'\nexit 2")
	before := countRows(t, h, `SELECT COUNT(*) FROM sessions`)
	envs := countRows(t, h, `SELECT COUNT(*) FROM environments`)
	_, err := h.e.StartRunExec("test", "secret prompt text", "", "", "", "", "", "", "alice")
	var refused *engine.AdmitRefusedError
	if !errors.As(err, &refused) || refused.Reason != "over budget for today" {
		t.Fatalf("start %v", err)
	}
	if n := countRows(t, h, `SELECT COUNT(*) FROM sessions`); n != before {
		t.Fatalf("sessions %d after refusal, want %d", n, before)
	}
	if n := countRows(t, h, `SELECT COUNT(*) FROM environments`); n != envs {
		t.Fatalf("environments %d after refusal, want %d", n, envs)
	}
	var body string
	if err := h.st.DB.QueryRow(`SELECT body FROM actions WHERE action_type='admit.refuse' AND session_id IS NULL`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"reason":"over budget for today"`) {
		t.Fatalf("refusal record %s", body)
	}
	runs := hookRuns(t, dir)
	if len(runs) != 1 {
		t.Fatalf("hook runs %v", runs)
	}
	var req map[string]string
	if err := json.Unmarshal([]byte(runs[0]), &req); err != nil {
		t.Fatal(err)
	}
	if req["project"] != "test" || req["kind"] != "run" || req["actor"] != "alice" || len(req["prompt_sha256"]) != 64 || strings.Contains(runs[0], "secret prompt") {
		t.Fatalf("hook stdin %s", runs[0])
	}
	env, err := os.ReadFile(filepath.Join(dir, "env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(env), "RUSUI_") || strings.Contains(string(env), "operator-secret") {
		t.Fatalf("hook environment leaked plane secrets:\n%s", env)
	}
}

func TestAdmitHookAllowRecordsReferenceOnce(t *testing.T) {
	h := setup(t)
	dir := admitHook(t, h, "echo 'ticket-42'\necho 'second line ignored'")
	sid, err := h.e.StartRunExec("test", "work", "retry-key", "", "", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.e.StartRunExec("test", "work", "retry-key", "", "", "", "", "", "")
	if err != nil || again != sid {
		t.Fatalf("retry %d %v, want %d", again, err, sid)
	}
	if runs := hookRuns(t, dir); len(runs) != 1 {
		t.Fatalf("hook ran %d times for one session", len(runs))
	}
	a, err := store.GetAction(h.st, "admit-allow-"+strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatal(err)
	}
	if a.Type != "admit.allow" || a.SessionID == nil || *a.SessionID != sid || !strings.Contains(a.Body, `"ref":"ticket-42"`) {
		t.Fatalf("allow record %+v", a)
	}
}

func TestAdmitHookFaultsRefuse(t *testing.T) {
	cases := map[string]string{
		"exit 1": "echo 'looks fine'\nexit 1",
		"crash":  "kill -9 $$",
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			h := setup(t)
			admitHook(t, h, script)
			var refused *engine.AdmitRefusedError
			if _, err := h.e.StartRunExec("test", "work", "", "", "", "", "", "", ""); !errors.As(err, &refused) {
				t.Fatalf("start %v", err)
			}
		})
	}
	t.Run("not executable", func(t *testing.T) {
		h := setup(t)
		dir := admitHook(t, h, "exit 0")
		if err := os.Chmod(filepath.Join(dir, "admit"), 0o600); err != nil {
			t.Fatal(err)
		}
		var refused *engine.AdmitRefusedError
		if _, err := h.e.StartRunExec("test", "work", "", "", "", "", "", "", ""); !errors.As(err, &refused) {
			t.Fatalf("start %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		h := setup(t)
		admitHook(t, h, "exec sleep 30")
		start := time.Now()
		var refused *engine.AdmitRefusedError
		if _, err := h.e.StartRunExec("test", "work", "", "", "", "", "", "", ""); !errors.As(err, &refused) || refused.Reason != "admit hook timed out" {
			t.Fatalf("start %v", err)
		}
		if d := time.Since(start); d > engine.AdmitTimeout+5*time.Second {
			t.Fatalf("timeout took %s", d)
		}
	})
}

// A schedule bucket that was refused is not re-asked every tick, and a
// refused fire is not a scheduler fault.
func TestAdmitHookScheduleRefusalReplaysPerBucket(t *testing.T) {
	h := setup(t)
	dir := admitHook(t, h, "echo 'no scheduled work'\nexit 2")
	if _, err := h.e.CreateSchedule("test", "hourly", "1m", "scheduled work", 0); err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_700_000_000, 0).UTC()
	for i := range 2 {
		if err := h.e.StepSchedules(t0, holdScheduler(t, h.st)); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	if runs := hookRuns(t, dir); len(runs) != 1 {
		t.Fatalf("hook ran %d times for one bucket", len(runs))
	}
	if n := countRows(t, h, `SELECT COUNT(*) FROM sessions WHERE kind='scheduled'`); n != 0 {
		t.Fatalf("scheduled sessions %d", n)
	}
	if n := countRows(t, h, `SELECT COUNT(*) FROM actions WHERE action_type='admit.refuse'`); n != 1 {
		t.Fatalf("refusal records %d", n)
	}
}

// Without an admit file nothing runs and nothing is recorded.
func TestAdmitWithoutHookRecordsNothing(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRunExec("test", "work", "", "", "", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, h, `SELECT COUNT(*) FROM actions WHERE action_type LIKE 'admit.%'`); n != 0 {
		t.Fatalf("admit records %d", n)
	}
}

// A child start passes through the same gate; the parent was admitted
// before the hook existed.
func TestAdmitHookRefusesChildSession(t *testing.T) {
	h := setup(t)
	parent, err := h.e.StartRunExec("test", "parent", "", "", "", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	dir := admitHook(t, h, "echo 'no fan-out'\nexit 2")
	var refused *engine.AdmitRefusedError
	if _, err := h.e.StartChildGuest(parent, "child", "", ""); !errors.As(err, &refused) || refused.Reason != "no fan-out" {
		t.Fatalf("child start %v", err)
	}
	if n := countRows(t, h, `SELECT COUNT(*) FROM sessions WHERE parent_session_id=?`, parent); n != 0 {
		t.Fatalf("children %d", n)
	}
	var req map[string]string
	if err := json.Unmarshal([]byte(hookRuns(t, dir)[0]), &req); err != nil || req["kind"] != "child" {
		t.Fatalf("hook stdin %v %v", req, err)
	}
}

// A broken admit entry still gates its project: starts refuse.
func TestReadAdmitHooksKeepsDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "test"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "test", "admit")); err != nil {
		t.Fatal(err)
	}
	hooks, err := engine.ReadAdmitHooks(dir)
	if err != nil || hooks["test"] == "" {
		t.Fatalf("hooks %v %v", hooks, err)
	}
	h := setup(t)
	h.e.AdmitHooks = hooks
	var refused *engine.AdmitRefusedError
	if _, err := h.e.StartRunExec("test", "work", "", "", "", "", "", "", ""); !errors.As(err, &refused) {
		t.Fatalf("start %v", err)
	}
}
