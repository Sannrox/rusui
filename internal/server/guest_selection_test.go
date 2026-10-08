package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
	"gopkg.in/yaml.v3"
)

func TestGuestRequestRefusedBeforeEnvironmentStart(t *testing.T) {
	_, hs, e := consoleEnv(t)
	runtime := &env.FakeRuntime{}
	e.Container = env.Container{RT: runtime, Image: "fixture:default"}
	var config policy.File
	if err := yaml.Unmarshal(e.PolicySnapshot().Raw, &config); err != nil {
		t.Fatal(err)
	}
	p := config.Projects["test"]
	p.Guests = &policy.ProjectGuests{Default: "shikigami", Allowed: []string{"shikigami", "claude"}}
	config.Projects["test"] = p
	raw, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ReloadPolicyBytes(raw); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codex", "unknown", "shikigami", "claude"} {
		req, err := http.NewRequest(http.MethodPost, hs.URL+"/projects/test/sessions", strings.NewReader(fmt.Sprintf(`{"kind":"run","prompt":"verify","guest":%q}`, name)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer wsec")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if name == "unknown" || name == "codex" {
			_ = res.Body.Close()
			if res.StatusCode != http.StatusConflict {
				t.Fatalf("unlisted guest status %d", res.StatusCode)
			}
			rows, err := store.ListSessions(e.Store, "test", 50)
			if err != nil || len(rows) != 0 {
				t.Fatalf("unlisted guest created session %+v %v", rows, err)
			}
		} else {
			if res.StatusCode != http.StatusCreated {
				t.Fatalf("allowed guest status %d", res.StatusCode)
			}
			var body struct {
				SessionID int64 `json:"session_id"`
			}
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			sess, err := store.GetSession(e.Store, body.SessionID)
			if err != nil || sess.GuestName != name || sess.GuestPin == "" {
				t.Fatalf("chosen guest %+v %v", sess, err)
			}
		}
		if len(runtime.Created) != 0 {
			t.Fatal("session create started an environment")
		}
	}
}
