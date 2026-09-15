package env

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerCLIRecordsCLI(t *testing.T) {
	bin, logPath := stubDocker(t)
	d := DockerCLI{Bin: bin}
	id, err := d.CreateAndStart(Spec{Name: "box", Image: "alpine:3", CPUMillis: 500, MemoryBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if id != "fake-ctr-id" {
		t.Fatalf("id %q", id)
	}
	if err := d.Stop(id); err != nil {
		t.Fatal(err)
	}
	if err := d.Start(id); err != nil {
		t.Fatal(err)
	}
	if !d.HasFile(id, ".agents/setup") {
		t.Fatal("has file")
	}
	b, err := d.ReadFile(id, "README")
	if err != nil || strings.TrimSpace(string(b)) != "hi" {
		t.Fatalf("read %q %v", b, err)
	}
	if err := d.Exec(id, []string{"/bin/sh", ".agents/setup"}); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.PlaceTree(id, src); err != nil {
		t.Fatal(err)
	}
	in, out, stop, err := d.ExecStdio(id, []string{"agent", "stdio"}, []string{"XAI_API_KEY=tok"})
	if err != nil {
		t.Fatal(err)
	}
	_ = in.Close()
	_, _ = out.Read(make([]byte, 1))
	stop()
	if err := d.Remove(id); err != nil {
		t.Fatal(err)
	}
	logb, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logb)
	for _, want := range []string{"run -d", "--cpus 0.500", "--memory 67108864", "alpine:3", "stop fake-ctr-id", "start fake-ctr-id", "exec -i", "cp "} {
		if !strings.Contains(log, want) {
			t.Fatalf("missing %q in %s", want, log)
		}
	}
	if strings.Contains(log, "chdir") {
		t.Fatal(log)
	}
}

func TestLookRuntimeFindsDockerOrPodman(t *testing.T) {
	rt, err := LookRuntime()
	if err != nil {
		if _, e2 := exec.LookPath("docker"); e2 != nil {
			if _, e3 := exec.LookPath("podman"); e3 != nil {
				return
			}
		}
		t.Fatal(err)
	}
	_, ok := rt.(DockerCLI)
	if !ok {
		t.Fatalf("%T", rt)
	}
}

func stubDocker(t *testing.T) (bin, logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "log")
	bin = filepath.Join(dir, "docker")
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> \"$STUB_LOG\"\n" +
		"cmd=$1; shift\n" +
		"if [ \"$cmd\" = run ]; then echo fake-ctr-id; exit 0; fi\n" +
		"if [ \"$cmd\" = exec ]; then\n" +
		"  if [ \"$1\" = -i ]; then cat >/dev/null; exit 0; fi\n" +
		"  if [ \"$2\" = test ] || [ \"$3\" = test ]; then exit 0; fi\n" +
		"  if [ \"$2\" = cat ] || [ \"$3\" = cat ]; then echo hi; exit 0; fi\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STUB_LOG", logPath)
	return bin, logPath
}
