package env

import "testing"

func TestContainerUsesRuntimeNotDaemon(t *testing.T) {
	rt := &FakeRuntime{DefaultFiles: map[string]bool{SetupPath: true}}
	d := Container{RT: rt, Image: "base:1"}
	id, err := d.Create("n")
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.Created) != 1 || rt.Created[0].Network != TrustedNetwork || !rt.Created[0].DisableIPv6 {
		t.Fatalf("trusted network %#v", rt.Created)
	}
	if _, err := d.Setup(id, "hash"); err != nil {
		t.Fatal(err)
	}
	if err := d.Sleep(id); err != nil {
		t.Fatal(err)
	}
	if err := d.Wake(id); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Resume(id); err != nil {
		t.Fatal(err)
	}
	if err := d.Destroy(id); err != nil {
		t.Fatal(err)
	}
	if len(rt.Created) != 1 || len(rt.Stopped) != 1 || len(rt.Started) != 1 || len(rt.Removed) != 1 {
		t.Fatalf("calls created=%d stop=%d start=%d rm=%d", len(rt.Created), len(rt.Stopped), len(rt.Started), len(rt.Removed))
	}
}

func TestContainerCreateRequiresImage(t *testing.T) {
	d := Container{RT: &FakeRuntime{}}
	if _, err := d.Create("n"); err == nil {
		t.Fatal("expected image required")
	}
}

func TestContainerCapturesHookOutput(t *testing.T) {
	rt := &FakeRuntime{
		DefaultFiles: map[string]bool{SetupPath: true},
		OutputHook: func(_ string, cmd []string) []byte {
			return []byte("ran " + cmd[len(cmd)-1] + "\n")
		},
	}
	d := Container{RT: rt, Image: "base:1"}
	id, err := d.Create("n")
	if err != nil {
		t.Fatal(err)
	}
	setup, err := d.Setup(id, "hash")
	if err != nil {
		t.Fatal(err)
	}
	if !setup.Ran || setup.Kind != CaptureSetup || string(setup.Output) != "ran "+SetupPath+"\n" || setup.Failed {
		t.Fatalf("setup %+v", setup)
	}
	resume, err := d.Resume(id)
	if err != nil {
		t.Fatal(err)
	}
	if resume.Ran || resume.Kind != CaptureResume || resume.Output != nil {
		t.Fatalf("missing resume %+v", resume)
	}
	rt.SetFileContent(id, ServicesRusuiPath, []byte("services:\n  web:\n    command: pnpm dev\n"))
	svcs, err := d.StartServices(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 1 || svcs[0].Name != "web" || string(svcs[0].Output) != "ran pnpm dev\n" {
		t.Fatalf("services %+v", svcs)
	}
}

func TestTailBufferKeepsLastLimitBytes(t *testing.T) {
	buf := &tailBuffer{limit: 4}
	for _, chunk := range []string{"ab", "cdef", "ghijk"} {
		_, _ = buf.Write([]byte(chunk))
	}
	out, truncated := buf.result()
	if string(out) != "hijk" || !truncated {
		t.Fatalf("out %q truncated %v", out, truncated)
	}
	small := &tailBuffer{limit: 4}
	_, _ = small.Write([]byte("abcd"))
	if out, truncated := small.result(); string(out) != "abcd" || truncated {
		t.Fatalf("out %q truncated %v", out, truncated)
	}
}
