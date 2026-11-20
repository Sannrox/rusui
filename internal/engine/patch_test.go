package engine_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func TestRunPatchAppliesAfterPrepareAndKeepsSourceHash(t *testing.T) {
	b := newSetupBox(t)
	hash := engine.SourceHash("rusui-guest:test", "aaa", nil)
	patch := readmePatch("pin-a\n", "patched\n")
	patched := postRunCreate(t, b.h, runPatchBody(patch))
	if patched["source_hash"] != hash || patched["snapshot_hit"] != false {
		t.Fatalf("patched create %+v, want miss %s", patched, hash)
	}
	sid := int64(patched["session_id"].(float64))
	c, err := b.h.e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatalf("claim %v %v", c, err)
	}
	sess, err := store.GetSession(b.h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(b.h.st, sess.EnvironmentID)
	if err != nil || envRow.Handle == "" || envRow.SourceHash != hash {
		t.Fatalf("env %+v %v, want hash %s", envRow, err, hash)
	}
	got, err := b.rt.ReadFile(envRow.Handle, "README")
	if err != nil || string(got) != "patched\n" {
		t.Fatalf("workspace README %q %v", got, err)
	}
	built, err := b.rt.ReadFile(envRow.Handle, "out/built.txt")
	if err != nil || string(built) != "built from pin-a\n" {
		t.Fatalf("setup output %q %v", built, err)
	}
}

func TestRunPatchCreateReportsSnapshotHit(t *testing.T) {
	b := newSetupBox(t)
	hash := engine.SourceHash("rusui-guest:test", "aaa", nil)
	patch := readmePatch("pin-a\n", "patched\n")
	plain := postRunCreate(t, b.h, `{"kind":"run","prompt":"plain"}`)
	first := postRunCreate(t, b.h, runPatchBody(patch))
	if first["snapshot_hit"] != false || first["source_hash"] != hash || first["source_hash"] != plain["source_hash"] {
		t.Fatalf("first %+v plain %+v, want miss %s", first, plain, hash)
	}
	b.mustProvision(t, "box-1", "aaa")
	second := postRunCreate(t, b.h, runPatchBody(patch))
	if second["snapshot_hit"] != true || second["source_hash"] != hash {
		t.Fatalf("second %+v, want hit %s", second, hash)
	}
}

func TestRunPatchApplyFailureDestroysGuest(t *testing.T) {
	b := newSetupBox(t)
	patch := readmePatch("missing\n", "nope\n")
	created := postRunCreate(t, b.h, runPatchBody(patch))
	sid := int64(created["session_id"].(float64))
	c, err := b.h.e.Claim("example/test-repo")
	if err == nil || c != nil {
		t.Fatalf("claim succeeded %v %v", c, err)
	}
	if !strings.Contains(err.Error(), "patch") {
		t.Fatalf("claim error %v", err)
	}
	if len(b.rt.Created) != 1 || len(b.rt.Removed) != 1 {
		t.Fatalf("created %#v removed %#v", b.rt.Created, b.rt.Removed)
	}
	sess, err := store.GetSession(b.h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(b.h.st, sess.EnvironmentID)
	if err != nil || envRow.Handle != "" {
		t.Fatalf("guest left running %+v %v", envRow, err)
	}
}

func TestRunPatchOversizedRefusesSession(t *testing.T) {
	h := setup(t)
	var before int
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	raw := strings.Repeat("a", env.WorkspaceUploadCap+1)
	body := fmt.Sprintf(`{"kind":"run","prompt":"hi","patch":%q}`, base64.StdEncoding.EncodeToString([]byte(raw)))
	req, err := http.NewRequest(http.MethodPost, h.http.URL+"/projects/test/sessions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(got), "oversize") {
		t.Fatalf("code %d %s", res.StatusCode, got)
	}
	var after int
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("sessions %d -> %d", before, after)
	}
}

func postRunCreate(t *testing.T, h *harn, body string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.http.URL+"/projects/test/sessions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("code %d %s", res.StatusCode, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json %s: %v", raw, err)
	}
	return out
}

func runPatchBody(patch []byte) string {
	return fmt.Sprintf(`{"kind":"run","prompt":"hi","patch":%q}`, base64.StdEncoding.EncodeToString(patch))
}

func readmePatch(old, new string) []byte {
	return []byte("--- a/README\n+++ b/README\n@@ -1 +1 @@\n-" + strings.TrimSuffix(old, "\n") + "\n+" + strings.TrimSuffix(new, "\n") + "\n")
}
