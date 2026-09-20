package ops

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/env"
)

func TestDiagnoseDistinguishesReadyMisconfiguredUnavailable(t *testing.T) {
	dir := t.TempDir()
	pol := filepath.Join(dir, "policy.yaml")
	if err := copyExamplePolicy(t, pol); err != nil {
		t.Fatal(err)
	}
	cert := filepath.Join(dir, "plane.crt")
	key := filepath.Join(dir, "plane.key")
	ca := filepath.Join(dir, "plane-ca.crt")
	for _, p := range []string{cert, key, ca} {
		if err := os.WriteFile(p, []byte("placeholder"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	secret := "wsec-should-never-appear"
	envfn := func(k string) string {
		switch k {
		case "RUSUI_GUEST_IMAGE":
			return "rusui-guest:test"
		case "RUSUI_TLS_CERT":
			return cert
		case "RUSUI_TLS_KEY":
			return key
		case "RUSUI_PLANE_CA":
			return ca
		case "RUSUI_WORKER_SECRET":
			return secret
		default:
			return ""
		}
	}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(hs.Close)

	rep := Diagnose(Options{
		PolicyPath:  pol,
		Addr:        "127.0.0.1:8080",
		PlaneURL:    hs.URL,
		LookRuntime: func() (env.Runtime, error) { return env.DockerCLI{Bin: "docker"}, nil },
		Env:         envfn,
		HTTP:        hs.Client(),
	})
	if !rep.Ready || rep.Topology != Topology {
		t.Fatalf("ready %+v", rep)
	}
	raw, _ := json.Marshal(rep)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "placeholder") {
		t.Fatalf("secret or file bytes leaked: %s", raw)
	}

	badPol := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(badPol, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep = Diagnose(Options{PolicyPath: badPol, LookRuntime: func() (env.Runtime, error) { return nil, fmt.Errorf("none") }, Env: envfn})
	if rep.Ready || statusOf(rep, "policy") != StatusMisconfigured {
		t.Fatalf("bad policy %+v", rep)
	}

	rep = Diagnose(Options{
		PolicyPath:  pol,
		PlaneURL:    "http://127.0.0.1:1",
		LookRuntime: func() (env.Runtime, error) { return nil, fmt.Errorf("none") },
		Env:         envfn,
	})
	if rep.Ready || statusOf(rep, "plane") != StatusUnavailable {
		t.Fatalf("unreachable %+v", rep)
	}

	rep = Diagnose(Options{
		PolicyPath:  pol,
		LookRuntime: func() (env.Runtime, error) { return nil, fmt.Errorf("missing") },
		Env:         envfn,
	})
	if statusOf(rep, "runtime") != StatusUnavailable {
		t.Fatalf("runtime %+v", rep)
	}

	rep = Diagnose(Options{
		PolicyPath:  pol,
		LookRuntime: func() (env.Runtime, error) { return env.DockerCLI{Bin: "docker"}, nil },
		Env:         func(string) string { return "" },
	})
	if rep.Ready || statusOf(rep, "plane_tls") != StatusMisconfigured || statusOf(rep, "guest_image") != StatusMisconfigured {
		t.Fatalf("tls/image %+v", rep)
	}

	rep = Diagnose(Options{
		PolicyPath:  pol,
		Addr:        "0.0.0.0:8080",
		LookRuntime: func() (env.Runtime, error) { return nil, fmt.Errorf("none") },
		Env:         func(string) string { return "" },
	})
	if statusOf(rep, "loopback") != StatusMisconfigured {
		t.Fatalf("loopback %+v", rep)
	}
}

func statusOf(r Report, name string) string {
	for _, c := range r.Checks {
		if c.Name == name {
			return c.Status
		}
	}
	return ""
}

func copyExamplePolicy(t *testing.T, dst string) error {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "..", "policy.example.yaml"),
		"policy.example.yaml",
	}
	var last error
	for _, src := range candidates {
		b, err := os.ReadFile(src)
		if err != nil {
			last = err
			continue
		}
		return os.WriteFile(dst, b, 0o600)
	}
	return last
}
