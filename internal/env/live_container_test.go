package env

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveContainerProcessIdentity(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		if _, err2 := exec.LookPath("podman"); err2 != nil {
			t.Skip("docker or podman required for live proof")
		}
	}
	rt, err := LookRuntime()
	if err != nil {
		t.Skip(err)
	}
	d := Container{RT: rt, Image: "alpine:3.20"}
	name := fmt.Sprintf("proof-%d", time.Now().UnixNano()%1_000_000_000)
	id, err := d.Create(name)
	if err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "cannot connect to the docker daemon") || strings.Contains(message, "cannot connect to podman") {
			t.Skipf("container runtime is installed but unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Destroy(id) })
	if err := d.RT.Exec(id, []string{"true"}); err != nil {
		t.Fatalf("running exec: %v", err)
	}
	if err := d.RT.Exec(id, []string{"sh", "-c", "echo ident > marker"}); err != nil {
		t.Fatalf("write workspace marker: %v", err)
	}
	if !d.RT.HasFile(id, "marker") {
		t.Fatal("workspace marker missing after write")
	}
	if err := d.Sleep(id); err != nil {
		t.Fatal(err)
	}
	if err := d.RT.Exec(id, []string{"true"}); err == nil {
		t.Fatal("exec succeeded while slept")
	}
	if err := d.Wake(id); err != nil {
		t.Fatal(err)
	}
	if err := d.RT.Exec(id, []string{"true"}); err != nil {
		t.Fatalf("wake exec: %v", err)
	}
	got, err := d.RT.ReadFile(id, "marker")
	if err != nil {
		t.Fatalf("read workspace marker: %v", err)
	}
	if string(bytes.TrimSpace(got)) != "ident" {
		t.Fatalf("workspace identity %q", got)
	}
	if err := d.Destroy(id); err != nil {
		t.Fatal(err)
	}
	if err := d.RT.Exec(id, []string{"true"}); err == nil {
		t.Fatal("exec succeeded after destroy")
	}
}

// KillGuest ends processes started by exec, which have parent PID 0 in
// the container, and keeps the container itself running (#441).
func TestLiveKillGuestStopsExecProcessesOnly(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker required for live proof")
	}
	rt, err := LookRuntime()
	if err != nil {
		t.Skip(err)
	}
	d := Container{RT: rt, Image: "alpine:3.20"}
	id, err := d.Create(fmt.Sprintf("kill-%d", time.Now().UnixNano()%1_000_000_000))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "cannot connect") {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Destroy(id) })
	docker := rt.(DockerCLI).bin()
	for _, args := range [][]string{
		{"exec", "-d", id, "sleep", "600"},
		{"exec", "-d", id, "sh", "-c", "trap '' TERM; sleep 600"},
		{"exec", "-d", id, "sh", "-c", "sleep 600 & wait"},
	} {
		if out, err := exec.Command(docker, args...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	count := func() string {
		// Live sleepers other than PID 1; an unreaped zombie does not count.
		out, err := exec.Command(docker, "exec", id, "sh", "-c", "for d in /proc/[0-9]*; do p=${d#/proc/}; [ $p = 1 ] && continue; grep -q '^Name:.sleep' $d/status 2>/dev/null && ! grep -q '^State:.Z' $d/status && echo $p; done | wc -l").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	var n string
	for range 50 {
		if n = count(); n == "3" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if n != "3" {
		t.Fatalf("sleepers before kill %s", n)
	}
	if err := d.KillGuest(id); err != nil {
		t.Fatal(err)
	}
	if n = count(); n != "0" {
		t.Fatalf("sleepers after kill %s", n)
	}
	if err := d.RT.Exec(id, []string{"true"}); err != nil {
		t.Fatalf("container did not survive the kill: %v", err)
	}
}

// KillGuest returns as soon as the guest's processes have ended on TERM,
// rather than after a fixed grace second (#448).
func TestLiveKillGuestEndsGraceWhenProcessesExit(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker required for live proof")
	}
	rt, err := LookRuntime()
	if err != nil {
		t.Skip(err)
	}
	d := Container{RT: rt, Image: "alpine:3.20"}
	id, err := d.Create(fmt.Sprintf("killfast-%d", time.Now().UnixNano()%1_000_000_000))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "cannot connect") {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Destroy(id) })
	docker := rt.(DockerCLI).bin()
	// The orphaned sleep becomes an unreaped zombie under PID 1 once
	// killed; it must not hold the grace open.
	for _, args := range [][]string{
		{"exec", "-d", id, "sleep", "600"},
		{"exec", "-d", id, "sh", "-c", "sleep 600 & wait"},
	} {
		if out, err := exec.Command(docker, args...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	time.Sleep(300 * time.Millisecond)
	// Measure the exec's own cost so a slow daemon does not fail the test.
	base := time.Now()
	if err := d.RT.Exec(id, []string{"true"}); err != nil {
		t.Fatal(err)
	}
	overhead := time.Since(base)
	start := time.Now()
	if err := d.KillGuest(id); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start) - overhead; took > 700*time.Millisecond {
		t.Fatalf("kill took %s beyond exec overhead %s; want the grace to end early", took, overhead)
	}
}

// The container workspace is listed and read through the runtime with
// the console's limits: no directories, no symlinks, a size cap, a
// name that is an argument rather than script text (#440).
func TestLiveWorkspaceInspection(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker required for live proof")
	}
	rt, err := LookRuntime()
	if err != nil {
		t.Skip(err)
	}
	d := Container{RT: rt, Image: "alpine:3.20"}
	id, err := d.Create(fmt.Sprintf("inspect-%d", time.Now().UnixNano()%1_000_000_000))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "cannot connect") {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Destroy(id) })
	setup := `cd /workspace && printf 'hello\n' > note.txt && printf 'x' > .hidden && mkdir dir && ln -s /etc/passwd link && head -c 300 /dev/zero > big && printf 'q' > 'odd $(id) name'`
	if err := d.RT.Exec(id, []string{"sh", "-c", setup}); err != nil {
		t.Fatal(err)
	}
	in, ok := d.Inspector()
	if !ok {
		t.Fatal("runtime cannot inspect")
	}
	names, err := in.WorkspaceNames(id)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(names, "|")
	for _, want := range []string{"note.txt", ".hidden", "link", "big", "odd $(id) name"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "dir") {
		t.Fatalf("directory listed: %q", got)
	}
	for name, want := range map[string]string{
		"note.txt":       "hello\n",
		"odd $(id) name": "q",
		"link":           FileIrregular,
		"dir":            FileIrregular,
		"absent":         FileMissing,
		"big":            FileOversized,
		"../etc/passwd":  FilePathDenied,
	} {
		body, state, err := in.WorkspaceFile(id, name, 256)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if state != "" && state != want || state == "" && string(body) != want {
			t.Fatalf("%s: body %q state %q, want %q", name, body, state, want)
		}
	}
}

// A guest that replaces /workspace with a symlink does not get other
// files listed or read as its workspace (#447).
func TestLiveWorkspaceInspectionRefusesReplacedRoot(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker required for live proof")
	}
	rt, err := LookRuntime()
	if err != nil {
		t.Skip(err)
	}
	d := Container{RT: rt, Image: "alpine:3.20"}
	id, err := d.Create(fmt.Sprintf("root-%d", time.Now().UnixNano()%1_000_000_000))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "cannot connect") {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Destroy(id) })
	if err := d.RT.Exec(id, []string{"sh", "-c", "cd / && rm -rf /workspace && ln -s /etc /workspace"}); err != nil {
		t.Fatal(err)
	}
	in, _ := d.Inspector()
	if names, err := in.WorkspaceNames(id); !errors.Is(err, ErrWorkspaceReplaced) {
		t.Fatalf("listed %q, err %v", names, err)
	}
	if body, state, err := in.WorkspaceFile(id, "passwd", 1<<16); err != nil || state != FileRootReplaced || len(body) != 0 {
		t.Fatalf("read %d bytes, state %q, err %v", len(body), state, err)
	}
}

// One exec snapshots the whole workspace with the per-file and total
// caps, and odd names or bodies cannot break the framing (#449).
func TestLiveWorkspaceSnapshot(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker required for live proof")
	}
	rt, err := LookRuntime()
	if err != nil {
		t.Skip(err)
	}
	d := Container{RT: rt, Image: "alpine:3.20"}
	id, err := d.Create(fmt.Sprintf("snap-%d", time.Now().UnixNano()%1_000_000_000))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "cannot connect") {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Destroy(id) })
	setup := `cd /workspace && printf 'hello\n' > note.txt && printf 'a\000b' > nul.bin && mkdir dir && ln -s /etc/passwd link && head -c 300 /dev/zero > big && printf 'q' > "$(printf 'odd\nname $(id)')" && for i in 1 2 3; do printf '12345678' > "z$i"; done`
	if err := d.RT.Exec(id, []string{"sh", "-c", setup}); err != nil {
		t.Fatal(err)
	}
	in, _ := d.Inspector()
	entries, err := in.WorkspaceSnapshot(id, 256, 20)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]WorkspaceEntry{}
	for _, e := range entries {
		got[e.Name] = e
	}
	if _, ok := got["dir"]; ok {
		t.Fatal("directory in snapshot")
	}
	if e := got["link"]; e.State != FileIrregular {
		t.Fatalf("link %+v", e)
	}
	if e := got["big"]; e.State != FileOversized {
		t.Fatalf("big %+v", e)
	}
	if e := got["nul.bin"]; string(e.Body) != "a\x00b" {
		t.Fatalf("binary body %+v", e)
	}
	if e, ok := got["odd\nname $(id)"]; !ok || string(e.Body) != "q" {
		t.Fatalf("odd name %+v in %v", e, entries)
	}
	// Glob order: note.txt, nul.bin, the odd name, then z1..z3. The
	// 20-byte total cap is reached after z2.
	if e := got["z2"]; string(e.Body) != "12345678" {
		t.Fatalf("z2 %+v", e)
	}
	if e := got["z3"]; e.State != FileSkipped {
		t.Fatalf("z3 %+v", e)
	}
	if err := d.RT.Exec(id, []string{"sh", "-c", "cd / && rm -rf /workspace && ln -s /etc /workspace"}); err != nil {
		t.Fatal(err)
	}
	if _, err := in.WorkspaceSnapshot(id, 256, 1<<20); !errors.Is(err, ErrWorkspaceReplaced) {
		t.Fatalf("replaced root err %v", err)
	}
}

// TestLiveCaptureTreeRoundTrip proves the tree after setup, .git
// included, comes back to the host and places into a new guest (#512).
func TestLiveCaptureTreeRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker required for live proof")
	}
	rt, err := LookRuntime()
	if err != nil {
		t.Skip(err)
	}
	d := Container{RT: rt, Image: "alpine:3.20"}
	create := func(prefix string) string {
		id, err := d.Create(fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()%1_000_000_000))
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "cannot connect") {
				t.Skipf("container runtime unavailable: %v", err)
			}
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.Destroy(id) })
		return id
	}
	id := create("cap")
	setup := `cd /workspace && mkdir -p .git out && printf 'ref\n' > .git/HEAD && printf 'built\n' > out/built.txt && ln -s /etc/passwd link`
	if err := d.RT.Exec(id, []string{"sh", "-c", setup}); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := d.CaptureTree(id, dest); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{".git/HEAD": "ref\n", "out/built.txt": "built\n"} {
		if b, err := os.ReadFile(filepath.Join(dest, rel)); err != nil || string(b) != want {
			t.Fatalf("%s = %q %v", rel, b, err)
		}
	}
	if target, err := os.Readlink(filepath.Join(dest, "link")); err != nil || target != "/etc/passwd" {
		t.Fatalf("link %q %v", target, err)
	}
	next := create("place")
	if err := d.PlaceTree(next, dest); err != nil {
		t.Fatal(err)
	}
	if b, err := d.RT.ReadFile(next, "out/built.txt"); err != nil || string(b) != "built\n" {
		t.Fatalf("placed %q %v", b, err)
	}
}
