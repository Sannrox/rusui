package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func TestSessionEnvlogServesBoundedHookAndServiceCaptures(t *testing.T) {
	_, hs, e := consoleEnv(t)
	var (
		mu      sync.Mutex
		outputs = map[string][]byte{
			env.SetupPath:  []byte("setup: <b>ready</b>\n"),
			env.ResumePath: []byte("resume one\n"),
			"pnpm dev":     append([]byte("web listening\xff\n"), []byte(strings.Repeat("x", env.CaptureLimit))...),
		}
	)
	rt := &env.FakeRuntime{
		DefaultFiles:    map[string]bool{env.SetupPath: true, env.ResumePath: true},
		DefaultContents: map[string][]byte{env.ServicesRusuiPath: []byte("services:\n  web:\n    command: pnpm dev\n")},
		OutputHook: func(_ string, cmd []string) []byte {
			mu.Lock()
			defer mu.Unlock()
			return outputs[cmd[len(cmd)-1]]
		},
	}
	e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	sid, err := e.StartRun("test", "output", "")
	if err != nil {
		t.Fatal(err)
	}
	get := func(token string) (int, []store.EnvironmentCapture) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10)+"/envlog", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var body struct {
			Captures []store.EnvironmentCapture `json:"captures"`
		}
		if res.StatusCode == http.StatusOK {
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
		}
		return res.StatusCode, body.Captures
	}
	find := func(caps []store.EnvironmentCapture, kind string) *store.EnvironmentCapture {
		for i := range caps {
			if caps[i].Kind == kind {
				return &caps[i]
			}
		}
		return nil
	}

	// Before any hook ran: an explicit empty list.
	status, caps := get("op-tok")
	if status != http.StatusOK || caps == nil || len(caps) != 0 {
		t.Fatalf("empty %d %+v", status, caps)
	}
	for _, tok := range []string{"", "wsec", "turn-grant"} {
		if status, _ := get(tok); status != http.StatusUnauthorized {
			t.Fatalf("token %q status %d", tok, status)
		}
	}

	created, err := e.ProvisionEnvironment(engine.EnvSpec{Name: "box-out", Kind: env.KindContainer, SourceHash: "src-out"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionEnvironment(e.Store, sid, created.ID); err != nil {
		t.Fatal(err)
	}
	// Wake here is the operator path; the queued run turn would hold the
	// environment awake.
	if _, err := e.Store.DB.Exec(`UPDATE turns SET state='failed' WHERE session_id=?`, sid); err != nil {
		t.Fatal(err)
	}
	_, caps = get("op-tok")
	setup := find(caps, env.CaptureSetup)
	if setup == nil || setup.Output != "setup: <b>ready</b>\n" || setup.Failed || setup.Truncated {
		t.Fatalf("setup %+v", caps)
	}
	if find(caps, env.CaptureResume) != nil {
		t.Fatalf("resume before wake %+v", caps)
	}
	web := find(caps, env.CaptureService)
	if web == nil || web.Name != "web" || !web.Truncated || len(web.Output) != env.CaptureLimit {
		t.Fatalf("service truncated=%v len=%d", web != nil && web.Truncated, len(caps))
	}

	sleepWake := func() {
		t.Helper()
		if _, err := e.SleepEnvironment(created.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.WakeEnvironment(created.ID); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	outputs["pnpm dev"] = []byte("web listening\xff\n")
	mu.Unlock()
	sleepWake()
	_, caps = get("op-tok")
	if r := find(caps, env.CaptureResume); r == nil || r.Output != "resume one\n" {
		t.Fatalf("resume %+v", caps)
	}
	if web := find(caps, env.CaptureService); web == nil || web.Truncated || web.Output != "web listening�\n" {
		t.Fatalf("service after wake %+v", web)
	}

	mu.Lock()
	outputs[env.ResumePath] = []byte("resume two\n")
	mu.Unlock()
	sleepWake()
	_, caps = get("op-tok")
	if r := find(caps, env.CaptureResume); r == nil || r.Output != "resume two\n" {
		t.Fatalf("second resume kept old body %+v", caps)
	}

	// The resume hook is removed: no leftover resume body, setup stays.
	rt.RemoveFile(created.Handle, env.ResumePath)
	sleepWake()
	_, caps = get("op-tok")
	if find(caps, env.CaptureResume) != nil || find(caps, env.CaptureSetup) == nil {
		t.Fatalf("missing resume %+v", caps)
	}

	c := operatorClient(t, hs)
	res, err := c.Get(hs.URL + "/console/sessions/" + strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(page), "Environment output") || !strings.Contains(string(page), "setup: &lt;b&gt;ready&lt;/b&gt;") {
		t.Fatalf("console page missing escaped output")
	}
	if strings.Contains(string(page), "<b>ready</b>") {
		t.Fatal("console rendered hook output as HTML")
	}
}
