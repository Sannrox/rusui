//go:build darwin || linux

package sumika

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockAttachSerializesClients(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sumika.sock")
	t.Setenv("SUMIKA_SOCK", path)
	unlock, err := LockAttach()
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan func(), 1)
	go func() {
		secondUnlock, err := LockAttach()
		if err == nil {
			acquired <- secondUnlock
		}
	}()
	select {
	case <-acquired:
		t.Fatal("second attach acquired the lock while the first was active")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case secondUnlock := <-acquired:
		secondUnlock()
	case <-time.After(time.Second):
		t.Fatal("second attach did not acquire the released lock")
	}
	if _, err := os.Stat(path + ".rusui-attach.lock"); err != nil {
		t.Fatal(err)
	}
}
