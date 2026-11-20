package engine_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

const packageRegistryHost = "registry.example.com"

type fakeRegistryIssuer struct {
	token  string
	expiry time.Time
	err    error
	calls  int
	last   struct {
		host      string
		ttl       time.Duration
		sessionID int64
		project   string
		turnID    int64
	}
}

func (f *fakeRegistryIssuer) Mint(host string, ttl time.Duration, sessionID int64, project string, turnID int64) (engine.PackageRegistryToken, error) {
	f.calls++
	f.last.host = host
	f.last.ttl = ttl
	f.last.sessionID = sessionID
	f.last.project = project
	f.last.turnID = turnID
	if f.err != nil {
		return engine.PackageRegistryToken{}, f.err
	}
	tok := f.token
	if tok == "" {
		tok = "minted-registry-token"
	}
	return engine.PackageRegistryToken{Value: tok, Expiry: f.expiry}, nil
}

func parsePackageRegistryPolicy(t *testing.T, extra string) *policy.Effective {
	t.Helper()
	p, err := policy.Parse([]byte(fixture + extra))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func enablePackageRegistry(t *testing.T, h *harn, issuer engine.PackageRegistryIssuer) *env.FakeRuntime {
	t.Helper()
	p := parsePackageRegistryPolicy(t, "    egress: custom\n    egress_allow: ["+packageRegistryHost+"]\n    package_registry: "+packageRegistryHost+"\n")
	h.e.ReloadPolicy(p)
	h.e.PackageRegistryIssuer = issuer
	rt := &env.FakeRuntime{}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	return rt
}

func claimedRegistryGuest(t *testing.T, h *harn) (c *engine.Claim, handle string, sessionID int64) {
	t.Helper()
	h.putRefresh(issue(1))
	c = h.claim()
	turn, err := store.GetTurn(h.st, c.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, turn.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	envRow, err := store.GetEnvironment(h.st, sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	return c, envRow.Handle, sess.ID
}

func TestPackageRegistryDefaultOffRefusesMint(t *testing.T) {
	h := setup(t)
	issuer := &fakeRegistryIssuer{token: "minted-off", expiry: h.clk.T.Add(engine.GrantTTL)}
	h.e.PackageRegistryIssuer = issuer
	rt := &env.FakeRuntime{}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	_, handle, _ := claimedRegistryGuest(t, h)
	if _, ok := rt.LookupSecret(handle, "package_registry"); ok {
		t.Fatal("minted while audience unnamed")
	}
	if issuer.calls != 0 {
		t.Fatalf("issuer calls %d", issuer.calls)
	}
}

func TestPackageRegistryOverlayCannotEnable(t *testing.T) {
	h := setup(t)
	issuer := &fakeRegistryIssuer{token: "minted-overlay", expiry: h.clk.T.Add(engine.GrantTTL)}
	h.e.PackageRegistryIssuer = issuer
	rt := &env.FakeRuntime{}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	if err := h.st.Tx(func(tx *sql.Tx) error {
		return store.OverlaySet(tx, "package_registry:test", packageRegistryHost)
	}); err != nil {
		t.Fatal(err)
	}
	_, handle, _ := claimedRegistryGuest(t, h)
	if _, ok := rt.LookupSecret(handle, "package_registry"); ok {
		t.Fatal("overlay enabled mint")
	}
	if issuer.calls != 0 {
		t.Fatalf("issuer calls %d", issuer.calls)
	}
}

func TestPackageRegistryOverlayRemovesAudience(t *testing.T) {
	h := setup(t)
	issuer := &fakeRegistryIssuer{token: "minted-removed", expiry: h.clk.T.Add(engine.GrantTTL)}
	rt := enablePackageRegistry(t, h, issuer)
	if err := h.st.Tx(func(tx *sql.Tx) error {
		return store.OverlaySet(tx, "package_registry:test", "0")
	}); err != nil {
		t.Fatal(err)
	}
	_, handle, _ := claimedRegistryGuest(t, h)
	if _, ok := rt.LookupSecret(handle, "package_registry"); ok {
		t.Fatal("overlay did not remove audience")
	}
	if issuer.calls != 0 {
		t.Fatalf("issuer calls %d", issuer.calls)
	}
}

func TestPackageRegistryTurnStartMintsAndSleepStrips(t *testing.T) {
	h := setup(t)
	const value = "minted-turn-token"
	issuer := &fakeRegistryIssuer{token: value, expiry: h.clk.T.Add(engine.GrantTTL)}
	rt := enablePackageRegistry(t, h, issuer)
	c, handle, sessionID := claimedRegistryGuest(t, h)
	got, ok := rt.LookupSecret(handle, "package_registry")
	if !ok || got != value {
		t.Fatalf("guest missing minted token %q ok=%v", got, ok)
	}
	if issuer.calls != 1 || issuer.last.host != packageRegistryHost || issuer.last.ttl != engine.GrantTTL || issuer.last.turnID != c.Job.ID || issuer.last.project != "test" || issuer.last.sessionID != sessionID {
		t.Fatalf("mint %+v calls %d", issuer.last, issuer.calls)
	}
	for path, data := range rt.Contents[handle] {
		if strings.Contains(string(data), value) {
			t.Fatalf("value in workspace %s", path)
		}
	}
	receipts, err := store.ListEnvironmentReceipts(h.st, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range receipts {
		if r.Kind != "package_registry" || r.State != "succeeded" {
			continue
		}
		found = true
		if strings.Contains(r.Detail, value) {
			t.Fatalf("token in receipt %q", r.Detail)
		}
		for _, want := range []string{"audience package_registry", "host " + packageRegistryHost, fmt.Sprintf("session %d", sessionID), "project test", "expires ", fmt.Sprintf("turn %d", c.Job.ID)} {
			if !strings.Contains(r.Detail, want) {
				t.Fatalf("detail %q missing %q", r.Detail, want)
			}
		}
	}
	if !found {
		t.Fatalf("missing package_registry receipt %+v", receipts)
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.SleepEnvironment(sess.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.LookupSecret(handle, "package_registry"); ok {
		t.Fatal("minted token survived sleep")
	}
}

func TestPackageRegistryWakeAndPrepareDoNotMint(t *testing.T) {
	h := setup(t)
	issuer := &fakeRegistryIssuer{token: "minted-wake", expiry: h.clk.T.Add(engine.GrantTTL)}
	rt := enablePackageRegistry(t, h, issuer)
	created, err := h.e.ProvisionEnvironment(engine.EnvSpec{
		Name: fmt.Sprintf("prep-%d-%s", os.Getpid(), t.Name()), Kind: env.KindContainer, Image: "rusui-guest:test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.LookupSecret(created.Handle, "package_registry"); ok {
		t.Fatal("minted during prepare")
	}
	if issuer.calls != 0 {
		t.Fatalf("prepare issuer calls %d", issuer.calls)
	}
	c, handle, sessionID := claimedRegistryGuest(t, h)
	if _, ok := rt.LookupSecret(handle, "package_registry"); !ok {
		t.Fatal("turn start did not mint")
	}
	if _, err := h.e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, art(c, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.SleepEnvironment(sess.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	wakeCalls := issuer.calls
	woke, err := h.e.WakeEnvironment(sess.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.LookupSecret(woke.Handle, "package_registry"); ok {
		t.Fatal("operator wake minted")
	}
	if issuer.calls != wakeCalls {
		t.Fatalf("wake issuer calls %d want %d", issuer.calls, wakeCalls)
	}
}

func TestPackageRegistryLongerTTLFailsTurn(t *testing.T) {
	h := setup(t)
	issuer := &fakeRegistryIssuer{token: "too-long", expiry: h.clk.T.Add(engine.GrantTTL + time.Minute)}
	enablePackageRegistry(t, h, issuer)
	h.putRefresh(issue(1))
	c, err := h.e.Claim("example/test-repo")
	if err == nil || c != nil {
		t.Fatalf("claim %v %v", c, err)
	}
	if !strings.Contains(err.Error(), "shorter-lived") {
		t.Fatalf("error %v", err)
	}
	j, err := store.JobState(h.st, "example/test-repo", 1)
	if err != nil {
		t.Fatal(err)
	}
	if j.State == "leased" {
		t.Fatalf("state %s", j.State)
	}
}

func TestPackageRegistryIssuerDownFailsTurn(t *testing.T) {
	h := setup(t)
	issuer := &fakeRegistryIssuer{err: errors.New("issuer down"), expiry: h.clk.T.Add(engine.GrantTTL)}
	enablePackageRegistry(t, h, issuer)
	h.putRefresh(issue(1))
	c, err := h.e.Claim("example/test-repo")
	if err == nil || c != nil {
		t.Fatalf("claim %v %v", c, err)
	}
	if !strings.Contains(err.Error(), "package-registry identity") {
		t.Fatalf("error %v", err)
	}
	j, err := store.JobState(h.st, "example/test-repo", 1)
	if err != nil {
		t.Fatal(err)
	}
	if j.State == "leased" {
		t.Fatalf("state %s", j.State)
	}
}

func TestPackageRegistryHeartbeatRenews(t *testing.T) {
	h := setup(t)
	issuer := &fakeRegistryIssuer{token: "minted-1", expiry: h.clk.T.Add(engine.GrantTTL)}
	rt := enablePackageRegistry(t, h, issuer)
	c, handle, _ := claimedRegistryGuest(t, h)
	got, ok := rt.LookupSecret(handle, "package_registry")
	if !ok || got != "minted-1" {
		t.Fatalf("first mint %q ok=%v", got, ok)
	}
	issuer.token = "minted-2"
	if err := h.e.Heartbeat(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision); err != nil {
		t.Fatal(err)
	}
	got, ok = rt.LookupSecret(handle, "package_registry")
	if !ok || got != "minted-2" {
		t.Fatalf("renewed %q ok=%v", got, ok)
	}
	if issuer.calls != 2 {
		t.Fatalf("issuer calls %d", issuer.calls)
	}
}

func TestHTTPPackageRegistryIssuerMintsShorterLivedToken(t *testing.T) {
	t.Parallel()
	var sawAuth, sawAudience, sawSubject bool
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		sawAuth = r.Header.Get("Authorization") == "Bearer durable-plane-token"
		raw, _ := io.ReadAll(r.Body)
		body = raw
		var req struct {
			Audience string `json:"audience"`
			TTL      int    `json:"ttl_seconds"`
			Subject  string `json:"subject"`
		}
		_ = json.Unmarshal(raw, &req)
		sawAudience = req.Audience == packageRegistryHost
		sawSubject = req.Subject == "session 7 project test"
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "short-lived", "expires_in": req.TTL})
	}))
	t.Cleanup(srv.Close)
	iss := engine.HTTPPackageRegistryIssuer{Durable: "durable-plane-token", Client: srv.Client(), Endpoint: srv.URL + "/token"}
	tok, err := iss.Mint(packageRegistryHost, engine.GrantTTL, 7, "test", 3)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Value != "short-lived" || tok.Value == "durable-plane-token" {
		t.Fatalf("token %q", tok.Value)
	}
	if !sawAuth || !sawAudience || !sawSubject {
		t.Fatalf("request auth=%v audience=%v subject=%v body=%s", sawAuth, sawAudience, sawSubject, body)
	}
	if !tok.Expiry.After(time.Now()) || tok.Expiry.After(time.Now().Add(engine.GrantTTL+time.Second)) {
		t.Fatalf("expiry %s", tok.Expiry)
	}
}

func TestHTTPPackageRegistryIssuerFailsClosed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		durable  string
		status   int
		response string
	}{
		{"no durable", "", 200, `{"token":"x","expires_in":60}`},
		{"down", "durable-plane-token", 503, `{"token":"x","expires_in":60}`},
		{"same token", "durable-plane-token", 200, `{"token":"durable-plane-token","expires_in":60}`},
		{"ttl too long", "durable-plane-token", 200, `{"token":"x","expires_in":700}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.response)
			}))
			t.Cleanup(srv.Close)
			iss := engine.HTTPPackageRegistryIssuer{Durable: tc.durable, Client: srv.Client(), Endpoint: srv.URL + "/token"}
			tok, err := iss.Mint(packageRegistryHost, time.Minute, 1, "test", 1)
			if err == nil || tok.Value != "" {
				t.Fatalf("got %+v %v", tok, err)
			}
			if strings.Contains(err.Error(), "durable-plane-token") || strings.Contains(err.Error(), `"token"`) {
				t.Fatalf("token in error %v", err)
			}
		})
	}
}
