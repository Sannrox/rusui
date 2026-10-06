package engine_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func TestArchiveRefusesPromptAndKeepsDirtyFilePastTTL(t *testing.T) {
	h := setup(t)
	root := t.TempDir()
	h.e.Env = env.Process{Root: root}

	created, err := h.e.CreateEnvironment("ws-archive")
	if err != nil {
		t.Fatal(err)
	}
	dirty := filepath.Join(created.Handle, "dirty.txt")
	if err := os.WriteFile(dirty, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}

	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionEnvironment(h.st, sid, created.ID); err != nil {
		t.Fatal(err)
	}

	if err := h.e.ArchiveSession(sid); err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil || !sess.Archived || sess.EnvironmentID != created.ID {
		t.Fatalf("archived session %+v %v", sess, err)
	}
	slept, err := store.GetEnvironment(h.st, created.ID)
	if err != nil || slept.State != store.EnvSleeping || slept.Handle != created.Handle {
		t.Fatalf("sleep %+v %v", slept, err)
	}

	if _, _, err := h.e.PromptFollowUp(sid, "second"); !errors.Is(err, engine.ErrArchived) {
		t.Fatalf("prompt after archive: %v", err)
	}

	h.clk.T = created.ExpiresAt.Add(time.Hour)
	if err := h.e.ReapEnvironments(); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetEnvironment(h.st, created.ID)
	if err != nil || got.State != store.EnvSleeping || got.Handle != created.Handle {
		t.Fatalf("after ttl %+v %v", got, err)
	}
	b, err := os.ReadFile(dirty)
	if err != nil || string(b) != "keep me" {
		t.Fatalf("dirty file %q %v", b, err)
	}
	if _, err := store.GetSession(h.st, sid); err != nil {
		t.Fatalf("session row gone: %v", err)
	}

	if err := h.e.UnarchiveSession(sid); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.e.PromptFollowUp(sid, "second"); err != nil {
		t.Fatalf("prompt after unarchive: %v", err)
	}
	woke, err := h.e.WakeSessionEnvironment(sid, "operator prompt")
	if err != nil || woke.ID != created.ID || woke.Handle != created.Handle || woke.State != store.EnvReady {
		t.Fatalf("wake %+v %v", woke, err)
	}
	b, err = os.ReadFile(dirty)
	if err != nil || string(b) != "keep me" {
		t.Fatalf("dirty file after unarchive %q %v", b, err)
	}
}

func TestArchivePromptHTTPConflict(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(sid, 10)
	req, err := http.NewRequest(http.MethodPost, h.http.URL+"/sessions/"+id+"/archive", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := ioRead(res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("archive %d %s", res.StatusCode, body)
	}

	prompt, err := json.Marshal(map[string]any{"prompt": "nope"})
	if err != nil {
		t.Fatal(err)
	}
	req, err = http.NewRequest(http.MethodPost, h.http.URL+"/sessions/"+id+"/turns", bytes.NewReader(prompt))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = ioRead(res)
	if res.StatusCode != http.StatusConflict || !strings.Contains(string(body), "archived") {
		t.Fatalf("prompt %d %s", res.StatusCode, body)
	}

	req, err = http.NewRequest(http.MethodPost, h.http.URL+"/sessions/"+id+"/unarchive", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = ioRead(res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unarchive %d %s", res.StatusCode, body)
	}

	req, err = http.NewRequest(http.MethodPost, h.http.URL+"/sessions/"+id+"/turns", bytes.NewReader(prompt))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = ioRead(res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("prompt after unarchive %d %s", res.StatusCode, body)
	}
}

func ioRead(res *http.Response) ([]byte, error) {
	defer func() { _ = res.Body.Close() }()
	var buf bytes.Buffer
	_, err := buf.ReadFrom(res.Body)
	return buf.Bytes(), err
}
