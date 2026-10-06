package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
)

func TestReadPlaneHooksLoadsProjectScripts(t *testing.T) {
	dir := t.TempDir()
	if pre, setup, err := engine.ReadPlaneHooks(filepath.Join(dir, "missing")); err != nil || pre != nil || setup != nil {
		t.Fatalf("missing dir %v %v %v", pre, setup, err)
	}
	write := func(slug, name, body string) {
		t.Helper()
		p := filepath.Join(dir, slug)
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("rusui", "pre-clone", "echo clone\r\n")
	write("rusui", "pre-setup", "echo setup\n")
	write("other", "pre-clone", "echo other\n")
	if err := os.WriteFile(filepath.Join(dir, "not-a-project"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	pre, setup, err := engine.ReadPlaneHooks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pre["rusui"] != "echo clone\n" || pre["other"] != "echo other\n" {
		t.Fatalf("pre-clone %#v", pre)
	}
	if setup["rusui"] != "echo setup\n" || setup["other"] != "" {
		t.Fatalf("pre-setup %#v", setup)
	}
}
