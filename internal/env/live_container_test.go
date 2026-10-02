package env

import (
	"bytes"
	"fmt"
	"os/exec"
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
