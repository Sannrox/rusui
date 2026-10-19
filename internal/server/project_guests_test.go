package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/policy"
	"gopkg.in/yaml.v3"
)

func TestProjectGuestsRequiresAuthAndHidesUnknownProjects(t *testing.T) {
	_, hs, _ := consoleEnv(t)
	req, err := http.NewRequest(http.MethodGet, hs.URL+"/projects/test/guests", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token %d", res.StatusCode)
	}
	req.Header.Set("X-Member-Id", "alice")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("member header %d", res.StatusCode)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("worker %d", res.StatusCode)
	}
	req, err = http.NewRequest(http.MethodGet, hs.URL+"/projects/missing/guests", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown project %d", res.StatusCode)
	}
}

func TestProjectGuestsReturnsDefaultAndPins(t *testing.T) {
	_, hs, e := consoleEnv(t)
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
	req, err := http.NewRequest(http.MethodGet, hs.URL+"/projects/test/guests", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body projectGuestsResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Default != "shikigami" || len(body.Allowed) != 2 {
		t.Fatalf("body %+v", body)
	}
	if body.Allowed[0].Name != "shikigami" || body.Allowed[0].Pin == "" {
		t.Fatalf("shikigami %+v", body.Allowed[0])
	}
	if body.Allowed[1].Name != "claude" || body.Allowed[1].Pin == "" {
		t.Fatalf("claude %+v", body.Allowed[1])
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{`"argv"`, `"image"`, `"secrets"`} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("leaked %s in %s", leak, encoded)
		}
	}
}
