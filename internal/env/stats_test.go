package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDockerStats(t *testing.T) {
	t.Parallel()
	got, err := parseDockerStats("" +
		"ctr-1\t12.50%\t64MiB / 2GiB\n" +
		"ctr-idle\t0.00%\t0B / 512MiB\n" +
		"ctr-skip\t--\t-- / --\n" +
		"ctr-space\t0.03%\t796 KiB / 64 MiB\n")
	if err != nil {
		t.Fatal(err)
	}
	if got["ctr-1"].CPUMillis != 125 || got["ctr-1"].MemoryBytes != 64<<20 {
		t.Fatalf("ctr-1 %+v", got["ctr-1"])
	}
	if _, ok := got["ctr-idle"]; !ok || got["ctr-idle"].CPUMillis != 0 || got["ctr-idle"].MemoryBytes != 0 {
		t.Fatalf("idle sample %+v present=%v", got["ctr-idle"], ok)
	}
	if _, ok := got["ctr-skip"]; ok {
		t.Fatalf("placeholder zeros %v", got)
	}
	if got["ctr-space"].CPUMillis != 0 || got["ctr-space"].MemoryBytes != 796*1024 {
		t.Fatalf("space %+v", got["ctr-space"])
	}
}

func TestFakeRuntimeSampleOmitsStopped(t *testing.T) {
	t.Parallel()
	f := &FakeRuntime{}
	id, err := f.CreateAndStart(Spec{Name: "box", Image: "img"})
	if err != nil {
		t.Fatal(err)
	}
	f.Samples = map[string]Sample{id: {CPUMillis: 40, MemoryBytes: 8 << 20}}
	got, err := f.Sample(id)
	if err != nil || got[id].CPUMillis != 40 || got[id].MemoryBytes != 8<<20 {
		t.Fatalf("live %+v %v", got, err)
	}
	if err := f.Stop(id); err != nil {
		t.Fatal(err)
	}
	got, err = f.Sample(id)
	if err != nil || len(got) != 0 {
		t.Fatalf("stopped %+v %v", got, err)
	}
}

func TestDockerCLISampleParsesStub(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	bin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> \"$STUB_LOG\"\n" +
		"if [ \"$1\" = stats ]; then echo \"0123456789abcdef\t12.50%\t64MiB / 2GiB\"; exit 0; fi\n" +
		"if [ \"$1\" = run ]; then echo 0123456789abcdef; exit 0; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STUB_LOG", logPath)
	d := DockerCLI{Bin: bin}
	id, err := d.CreateAndStart(Spec{Name: "box", Image: "alpine:3"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.Sample(id)
	if err != nil {
		t.Fatal(err)
	}
	if got[id].CPUMillis != 125 || got[id].MemoryBytes != 64<<20 {
		t.Fatalf("sample %+v", got)
	}
	logb, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logb), "stats --no-stream --format") {
		t.Fatalf("argv %s", logb)
	}
}
