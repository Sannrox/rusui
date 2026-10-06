package engine_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

var png1x1 = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, 0x00, 0x00, 0x00,
	0x0c, 0x49, 0x44, 0x41, 0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
	0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0x05, 0xfe, 0xd4, 0xef, 0x00, 0x00,
	0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestPromptAttachmentReachesGuestAndStaysOffWorkspace(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.e.PromptFollowUpFiles(sid, "look", []store.PromptFile{{Name: "shot.png", Body: png1x1}}); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", h.http.URL+"/jobs/claim", strings.NewReader(`{"repo":"example/test-repo"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("claim %d %s", res.StatusCode, b)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	listed, _ := out["attachments"].([]any)
	if len(listed) != 1 {
		t.Fatalf("claim attachments %v", out["attachments"])
	}
	row, _ := listed[0].(map[string]any)
	if row["name"] != "shot.png" || row["mime"] != "image/png" || row["data"] != base64.StdEncoding.EncodeToString(png1x1) {
		t.Fatalf("claim attachment %v", row)
	}
	rev := int(out["claimed_revision"].(float64))
	atts, err := store.ListPromptAttachments(h.st, sid, rev)
	if err != nil || len(atts) != 1 || atts[0].Name != "shot.png" || atts[0].MIME != "image/png" || !bytes.Equal(atts[0].Body, png1x1) {
		t.Fatalf("stored %+v %v", atts, err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(h.st, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if envRow.Handle != "" {
		if _, err := os.Stat(filepath.Join(envRow.Handle, "shot.png")); !os.IsNotExist(err) {
			t.Fatalf("attachment copied into workspace: %v", err)
		}
	}
	sum := sha256.Sum256(png1x1)
	digest := hex.EncodeToString(sum[:])
	acts, err := store.ListActionsForSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range acts {
		if a.Type != "prompt.attachment" {
			continue
		}
		found = true
		if !strings.Contains(a.Body, digest) || !strings.Contains(a.Body, "shot.png") {
			t.Fatalf("receipt %s", a.Body)
		}
		if strings.Contains(a.Body, base64.StdEncoding.EncodeToString(png1x1)) {
			t.Fatal("receipt contained bytes")
		}
	}
	if !found {
		t.Fatal("missing attachment receipt")
	}
}

func TestPromptAttachmentPathEscapeAndOversize(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.e.PromptFollowUpFiles(sid, "look", []store.PromptFile{{Name: "../x.png", Body: png1x1}}); err == nil || err.Error() != "path" {
		t.Fatalf("path %v", err)
	}
	if _, _, err := h.e.PromptFollowUpFiles(sid, "look", []store.PromptFile{{Name: "a/b.png", Body: png1x1}}); err == nil || err.Error() != "path" {
		t.Fatalf("slash %v", err)
	}
	big := bytes.Repeat([]byte("a"), env.WorkspaceUploadCap+1)
	if _, _, err := h.e.PromptFollowUpFiles(sid, "look", []store.PromptFile{{Name: "big.bin", Body: big}}); err == nil || err.Error() != "oversize" {
		t.Fatalf("oversize %v", err)
	}
}

func TestPromptAttachmentHTTP(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	do := func(body string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest("POST", h.http.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/turns", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer wsec")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		return res.StatusCode, b
	}
	payload, _ := json.Marshal(map[string]any{
		"prompt": "look",
		"attachments": []map[string]string{{
			"name":    "shot.png",
			"content": base64.StdEncoding.EncodeToString(png1x1),
		}},
	})
	code, out := do(string(payload))
	if code != 200 {
		t.Fatalf("ok %d %s", code, out)
	}
	code, _ = do(`{"prompt":"look","attachments":[{"name":"../x.png","content":"` + base64.StdEncoding.EncodeToString(png1x1) + `"}]}`)
	if code != 400 {
		t.Fatalf("path %d", code)
	}
	code, out = do(`{"prompt":"look","steer":true,"attachments":[{"name":"shot.png","content":"` + base64.StdEncoding.EncodeToString(png1x1) + `"}]}`)
	if code != 400 || !strings.Contains(string(out), "attachments are not a steer") {
		t.Fatalf("steer %d %s", code, out)
	}
}
