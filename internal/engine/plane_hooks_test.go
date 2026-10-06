package engine_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

const (
	preCloneScript = "echo pre-clone"
	preSetupScript = "echo pre-setup"
)

func hookBox(t *testing.T) *setupBox {
	t.Helper()
	b := newSetupBox(t)
	b.h.e.PreCloneHooks = map[string]string{"test": preCloneScript}
	b.h.e.PreSetupHooks = map[string]string{"test": preSetupScript}
	b.rt.OutputHook = func(_ string, cmd []string) []byte {
		if len(cmd) >= 3 && cmd[1] == "-c" {
			return []byte("out:" + cmd[2] + "\n")
		}
		return []byte("out:setup\n")
	}
	return b
}

func execKinds(rt *env.FakeRuntime) []string {
	var seq []string
	for _, cmd := range rt.Execs {
		if len(cmd) >= 4 && cmd[2] == "-c" {
			switch cmd[3] {
			case preCloneScript:
				seq = append(seq, env.CapturePreClone)
			case preSetupScript:
				seq = append(seq, env.CapturePreSetup)
			}
		}
		if len(cmd) >= 3 && cmd[2] == env.SetupPath {
			seq = append(seq, env.CaptureSetup)
		}
	}
	return seq
}

func TestFailedPreCloneLeavesNoEnvironment(t *testing.T) {
	b := hookBox(t)
	b.rt.ExecHook = func(_ string, cmd []string) error {
		if len(cmd) >= 3 && cmd[1] == "-c" && cmd[2] == preCloneScript {
			return fmt.Errorf("exit status 1")
		}
		return nil
	}
	if _, err := b.provision(t, "box-preclone", "aaa"); err == nil {
		t.Fatal("expected pre-clone failure")
	}
	if len(b.rt.Created) != 1 || len(b.rt.Removed) != 1 {
		t.Fatalf("created %#v removed %#v", b.rt.Created, b.rt.Removed)
	}
	var n int
	if err := b.h.st.DB.QueryRow(`SELECT COUNT(*) FROM environments WHERE name!=?`, store.LocalEnvironmentName).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("environments %d, want none", n)
	}
	if b.tree.Calls != 0 {
		t.Fatalf("cloned after failed pre-clone: %d", b.tree.Calls)
	}
}

func TestPlaneHooksRunBeforeCloneAndSetup(t *testing.T) {
	b := hookBox(t)
	created := b.mustProvision(t, "box-hooks", "aaa")
	if got := strings.Join(execKinds(b.rt), " "); got != "pre-clone pre-setup setup" {
		t.Fatalf("order %q", got)
	}
	if b.tree.Calls != 1 {
		t.Fatalf("clone %d", b.tree.Calls)
	}
	if n := b.setupRuns(); n != 1 {
		t.Fatalf("setup %d", n)
	}
	sid, err := b.h.e.StartRun("test", "hooks", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionEnvironment(b.h.st, sid, created.ID); err != nil {
		t.Fatal(err)
	}
	caps, err := store.ListSessionCaptures(b.h.st, sid, store.CaptureListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 3 {
		t.Fatalf("captures %+v", caps)
	}
	if caps[0].Kind != env.CapturePreClone || caps[0].Output != "out:"+preCloneScript+"\n" || caps[0].Failed {
		t.Fatalf("pre-clone %+v", caps[0])
	}
	if caps[1].Kind != env.CapturePreSetup || caps[1].Output != "out:"+preSetupScript+"\n" || caps[1].Failed {
		t.Fatalf("pre-setup %+v", caps[1])
	}
	if caps[2].Kind != env.CaptureSetup || caps[2].Output != "out:setup\n" || caps[2].Failed {
		t.Fatalf("setup %+v", caps[2])
	}
}

func TestSnapshotHitSkipsPlaneHooks(t *testing.T) {
	b := hookBox(t)
	b.mustProvision(t, "box-1", "aaa")
	if got := strings.Join(execKinds(b.rt), " "); got != "pre-clone pre-setup setup" {
		t.Fatalf("first %q", got)
	}
	b.mustProvision(t, "box-2", "aaa")
	if got := strings.Join(execKinds(b.rt), " "); got != "pre-clone pre-setup setup" {
		t.Fatalf("snapshot reran hooks %q", got)
	}
	if n := b.setupRuns(); n != 1 {
		t.Fatalf("setup %d", n)
	}
	if b.tree.Calls != 1 {
		t.Fatalf("clone %d", b.tree.Calls)
	}
}

func TestProjectKeySkipsPlaneHooks(t *testing.T) {
	b := hookBox(t)
	if _, err := b.h.e.ProvisionEnvironment(engine.EnvSpec{
		Name: "box-key", Kind: env.KindContainer, Repo: policy.ProjectKey("test"),
		SourceHash: "src-key",
	}); err != nil {
		t.Fatal(err)
	}
	if kinds := execKinds(b.rt); len(kinds) != 0 {
		t.Fatalf("hooks on project key %#v", kinds)
	}
	if b.tree.Calls != 0 {
		t.Fatalf("cloned project-key repo: %d", b.tree.Calls)
	}
}
