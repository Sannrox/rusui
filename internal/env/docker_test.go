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
	if id != "0123456789abcdef" {
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
	for _, want := range []string{"network inspect rusui-trusted", "network create rusui-trusted", "run -d", "--network rusui-trusted", "--add-host rusui.plane:host-gateway", "--sysctl net.ipv6.conf.all.disable_ipv6=1", "--cpus 0.500", "--memory 67108864", "alpine:3", "stop 0123456789abcdef", "start 0123456789abcdef", "exec -i", "cp "} {
		if !strings.Contains(log, want) {
			t.Fatalf("missing %q in %s", want, log)
		}
	}
	if strings.Contains(log, "chdir") {
		t.Fatal(log)
	}
}

func TestContainerIDIgnoresPullProgress(t *testing.T) {
	out := []byte("Unable to find image 'alpine:3.20' locally\n" +
		"3.20: Pulling from library/alpine\n" +
		"Status: Downloaded newer image for alpine:3.20\n" +
		"d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc\n")
	id, err := containerID(out)
	if err != nil || id != "d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc" {
		t.Fatalf("id %q err %v", id, err)
	}
	if _, err := containerID([]byte("Unable to find image\nStatus: Downloaded\n")); err == nil {
		t.Fatal("non-id output succeeded")
	}
}

func TestDockerCLIParsesIDAfterPullLogs(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	bin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> \"$STUB_LOG\"\n" +
		"cmd=$1; shift\n" +
		"if [ \"$cmd\" = network ]; then\n" +
		"  if [ \"$1\" = inspect ]; then exit 1; fi\n" +
		"  if [ \"$1\" = create ]; then exit 0; fi\n" +
		"fi\n" +
		"if [ \"$cmd\" = run ]; then\n" +
		"  echo \"Unable to find image 'alpine:3' locally\"\n" +
		"  echo \"Status: Downloaded newer image for alpine:3\"\n" +
		"  echo 0123456789abcdef\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$cmd\" = exec ]; then\n" +
		"  case \" $* \" in\n" +
		"  *\" 0123456789abcdef \"*) exit 0 ;;\n" +
		"  *) echo \"page not found\"; exit 1 ;;\n" +
		"  esac\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STUB_LOG", logPath)
	d := DockerCLI{Bin: bin}
	id, err := d.CreateAndStart(Spec{Name: "box", Image: "alpine:3"})
	if err != nil || id != "0123456789abcdef" {
		t.Fatalf("id %q err %v", id, err)
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

func TestDockerCLIFailsClosedWhenNetworkCreateFails(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	bin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> \"$STUB_LOG\"\n" +
		"cmd=$1; shift\n" +
		"if [ \"$cmd\" = network ]; then\n" +
		"  if [ \"$1\" = inspect ]; then exit 1; fi\n" +
		"  if [ \"$1\" = create ]; then echo cannot-create; exit 1; fi\n" +
		"fi\n" +
		"if [ \"$cmd\" = run ]; then echo should-not-run; exit 0; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STUB_LOG", logPath)
	d := DockerCLI{Bin: bin}
	if _, err := d.CreateAndStart(Spec{Name: "box", Image: "alpine:3"}); err == nil {
		t.Fatal("expected network create failure")
	}
	logb, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logb)
	if !strings.Contains(log, "network create rusui-trusted") {
		t.Fatalf("missing create in %s", log)
	}
	if strings.Contains(log, "run -d") {
		t.Fatalf("ran container without network: %s", log)
	}
}

func TestDockerCLINetworkCreateRace(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	bin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> \"$STUB_LOG\"\n" +
		"cmd=$1; shift\n" +
		"if [ \"$cmd\" = network ]; then\n" +
		"  if [ \"$1\" = inspect ]; then\n" +
		"    n=$(grep -c \"network inspect\" \"$STUB_LOG\")\n" +
		"    if [ \"$n\" -ge 2 ]; then exit 0; fi\n" +
		"    exit 1\n" +
		"  fi\n" +
		"  if [ \"$1\" = create ]; then echo already; exit 1; fi\n" +
		"fi\n" +
		"if [ \"$cmd\" = run ]; then echo 0123456789abcdef; exit 0; fi\n" +
		"if [ \"$cmd\" = exec ]; then exit 0; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STUB_LOG", logPath)
	d := DockerCLI{Bin: bin}
	id, err := d.CreateAndStart(Spec{Name: "box", Image: "alpine:3"})
	if err != nil || id != "0123456789abcdef" {
		t.Fatalf("id %q err %v", id, err)
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
		"if [ \"$cmd\" = network ]; then\n" +
		"  if [ \"$1\" = inspect ]; then echo missing; exit 1; fi\n" +
		"  if [ \"$1\" = create ]; then echo net-id; exit 0; fi\n" +
		"  exit 1\n" +
		"fi\n" +
		"if [ \"$cmd\" = run ]; then echo 0123456789abcdef; exit 0; fi\n" +
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
