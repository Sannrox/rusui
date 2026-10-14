package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/guest"
	"gopkg.in/yaml.v3"
)

func TestACPConformancePrintsPlaneReceiptForPolicy(t *testing.T) {
	want := guest.Receipt{Suite: guest.ConformanceSuite, Entry: strings.Repeat("e", 64), Image: "sha256:" + strings.Repeat("a", 64), Time: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC), MAC: strings.Repeat("m", 64)}
	var posted guest.Entry
	plane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/guests/conformance" || r.Header.Get("Authorization") != "Bearer wsec" {
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&posted)
		_ = json.NewEncoder(w).Encode(want)
	}))
	t.Cleanup(plane.Close)
	policyPath := filepath.Join(t.TempDir(), "policy.yaml")
	pol := "version: 2\nguests:\n  second:\n    protocol: acp\n    argv: [second-agent, acp]\n    probe: [second-agent, --version]\n    pin: \"2\"\n    image: example/second@" + want.Image + "\nprojects: {}\n"
	if err := os.WriteFile(policyPath, []byte(pol), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := acpConformanceMain([]string{"-policy", policyPath, "-guest", "second", "-url", plane.URL, "-token", "wsec"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if posted.Image != "example/second@"+want.Image || posted.Argv[0] != "second-agent" {
		t.Fatalf("posted %+v", posted)
	}
	var got struct {
		Receipt guest.Receipt `yaml:"receipt"`
	}
	if err := yaml.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Receipt.Time.Equal(want.Time) || got.Receipt.MAC != want.MAC || got.Receipt.Entry != want.Entry {
		t.Fatalf("printed %+v", got.Receipt)
	}
}
