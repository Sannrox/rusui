package env

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProcessDriverNeverUsesContainerRuntime(t *testing.T) {
	root := t.TempDir()
	d := Process{Root: root}
	handle, err := d.Create("box")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(handle) != root {
		t.Fatalf("handle %s not under %s", handle, root)
	}
	if err := d.Sleep(handle); err != nil {
		t.Fatal(err)
	}
	if err := d.Wake(handle); err != nil {
		t.Fatal(err)
	}
	if err := d.Destroy(handle); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(handle); !os.IsNotExist(err) {
		t.Fatalf("destroy left %s", handle)
	}
}
