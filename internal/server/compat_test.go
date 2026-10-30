package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/runner"
)

func TestRunnerHelloRejectsMismatchedCommit(t *testing.T) {
	s, hs, _ := consoleEnv(t)
	s.GitCommit = "aaa111"
	req, err := http.NewRequest(http.MethodPost, hs.URL+"/runners/hello", strings.NewReader(`{"name":"local","commit":"bbb222"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict || !strings.Contains(string(body), "does not match plane") {
		t.Fatalf("status %d body %s", res.StatusCode, body)
	}
}

func TestRunnerHelloAcceptsMatchingAndUnknownCommits(t *testing.T) {
	s, hs, _ := consoleEnv(t)
	s.GitCommit = "aaa111"
	for _, body := range []string{
		`{"name":"local","commit":"aaa111"}`,
		`{"name":"local"}`,
		`{"name":"local","commit":"unknown"}`,
	} {
		req, err := http.NewRequest(http.MethodPost, hs.URL+"/runners/hello", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer wsec")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		reply, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s -> %d %s", body, res.StatusCode, reply)
		}
	}
	c := &runner.Client{Base: hs.URL, Bootstrap: "wsec", Name: "local", Commit: "aaa111", HTTP: http.DefaultClient}
	if err := c.Hello(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAndFollowUpOmitOptionalRequestFields(t *testing.T) {
	_, hs, _ := consoleEnv(t)
	sid := createRunSession(t, hs, "hello")
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/sessions/%d/turns", hs.URL, sid), strings.NewReader(`{"prompt":"again"}`))
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
	if res.StatusCode != http.StatusOK {
		t.Fatalf("follow-up %d %s", res.StatusCode, body)
	}
	var out struct {
		TurnID   int64  `json:"turn_id"`
		Delivery string `json:"delivery"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.TurnID == 0 || out.Delivery != "follow_up" {
		t.Fatalf("follow-up %s %v", body, err)
	}
}
