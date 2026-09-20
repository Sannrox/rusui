package env

import (
	"bytes"
	"fmt"
	"os/exec"
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
