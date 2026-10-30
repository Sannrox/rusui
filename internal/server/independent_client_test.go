package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/acp"
)

func TestIndependentClientAuthRefused(t *testing.T) {
	_, hs, _ := consoleEnv(t)
	post := func(token string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, hs.URL+"/projects/test/sessions", strings.NewReader(`{"kind":"run","prompt":"x"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	if got := post(""); got != http.StatusUnauthorized {
		t.Fatalf("missing token %d", got)
	}
	if got := post("nope"); got != http.StatusUnauthorized {
		t.Fatalf("bad token %d", got)
	}
}

func TestIndependentClientExplicitIncompatibility(t *testing.T) {
	_, hs, _ := consoleEnv(t)
	req, err := http.NewRequest(http.MethodPost, hs.URL+"/projects/test/sessions", strings.NewReader(`{"kind":"review","prompt":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "kind") {
		t.Fatalf("kind %d %s", res.StatusCode, body)
	}
}

func TestIndependentClientJourney(t *testing.T) {
	_, hs, e := consoleEnv(t)
	payload := []byte(`{"kind":"run","prompt":"independent client"}`)
	dispatch := func(idem string) (int64, int) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, hs.URL+"/projects/test/sessions", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer op-tok")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idem)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("dispatch %d %s", res.StatusCode, body)
		}
		var created struct {
			SessionID int64 `json:"session_id"`
		}
		if err := json.Unmarshal(body, &created); err != nil || created.SessionID == 0 {
			t.Fatalf("dispatch body %s %v", body, err)
		}
		return created.SessionID, res.StatusCode
	}
	a, _ := dispatch("independent-k1")
	b, _ := dispatch("independent-k1")
	if a != b {
		t.Fatalf("idempotent %d %d", a, b)
	}
	id := strconv.FormatInt(a, 10)

	req, _ := http.NewRequest(http.MethodGet, hs.URL+"/sessions/"+id, nil)
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(got), "independent client") {
		t.Fatalf("session %d %s", res.StatusCode, got)
	}

	s, followRes := openFollow(t, hs, a, 0, "op-tok")
	if followRes.StatusCode != http.StatusOK {
		t.Fatalf("follow %d", followRes.StatusCode)
	}
	s.expect("session")
	s.expect("state")
	s.cancel()
	again, _ := openFollow(t, hs, a, 0, "op-tok")
	again.expect("session")
	again.cancel()

	addApproval(t, e, a, "ind-deny", acp.ReasonUnmatched)
	req, _ = http.NewRequest(http.MethodGet, hs.URL+"/approvals", nil)
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	listBody, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(listBody), "ind-deny") {
		t.Fatalf("approvals %d %s", res.StatusCode, listBody)
	}
	req, _ = http.NewRequest(http.MethodPost, hs.URL+"/approvals/ind-deny", strings.NewReader(`{"decision":"deny"}`))
	req.Header.Set("Authorization", "Bearer op-tok")
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("decide %d", res.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodGet, hs.URL+"/sessions/"+id+"/logs", nil)
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	logs, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(logs), "ind-deny") {
		t.Fatalf("logs %d %s", res.StatusCode, logs)
	}
}

func TestIndependentClientExampleBinary(t *testing.T) {
	_, hs, _ := consoleEnv(t)
	dir := filepath.Join(repoRoot(t), "examples", "independent-client")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".",
		"-url", hs.URL,
		"-token", "op-tok",
		"-project", "test",
		"-prompt", "example binary",
		"-follow", "500ms",
		"-idempotency-key", "example-bin-1",
	)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	var rep struct {
		SessionID int64 `json:"session_id"`
		Followed  int   `json:"followed_events"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil || rep.SessionID == 0 {
		t.Fatalf("report %s %v stderr=%s", stdout.String(), err, stderr.String())
	}
	if rep.Followed < 1 {
		t.Fatalf("no follow events: %s", stdout.String())
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	if _, err := os.Stat(filepath.Join(root, "examples", "independent-client", "main.go")); err != nil {
		t.Fatal(err)
	}
	return root
}
