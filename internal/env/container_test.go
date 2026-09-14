package env

import "testing"

func TestContainerUsesRuntimeNotDaemon(t *testing.T) {
	rt := &FakeRuntime{DefaultFiles: map[string]bool{SetupPath: true}}
	d := Container{RT: rt, Image: "base:1"}
	id, err := d.Create("n")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Setup(id, "hash"); err != nil {
		t.Fatal(err)
	}
	if err := d.Sleep(id); err != nil {
		t.Fatal(err)
	}
	if err := d.Wake(id); err != nil {
		t.Fatal(err)
	}
	if err := d.Resume(id); err != nil {
		t.Fatal(err)
	}
	if err := d.Destroy(id); err != nil {
		t.Fatal(err)
	}
	if len(rt.Created) != 1 || len(rt.Stopped) != 1 || len(rt.Started) != 1 || len(rt.Removed) != 1 {
		t.Fatalf("calls created=%d stop=%d start=%d rm=%d", len(rt.Created), len(rt.Stopped), len(rt.Started), len(rt.Removed))
	}
}
