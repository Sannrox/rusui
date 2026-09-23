package setup

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	guestimage "github.com/sannrox/rusui/build/guest-image"
	"github.com/sannrox/rusui/internal/ops"
)

type fakeNet struct {
	exists, created bool
	images          map[string]string // tag -> image ID
	builds          int
}

func (f *fakeNet) NetworkExists(string) bool { return f.exists }
func (f *fakeNet) EnsureNetwork(string) error {
	f.created, f.exists = true, true
	return nil
}
func (f *fakeNet) ImageExists(tag string) bool { return f.images[tag] != "" }
func (f *fakeNet) BuildImage(tag string, dockerfile []byte, fresh bool) error {
	if len(dockerfile) == 0 {
		return os.ErrInvalid
	}
	if f.images == nil {
		f.images = map[string]string{}
	}
	f.builds++
	// Each build yields new bits, as a fresh build after upstream changes would.
	f.images[tag] = fmt.Sprintf("sha256:%016x%048x", f.builds, 0)
	return nil
}
func (f *fakeNet) ImageID(tag string) (string, error) {
	if f.images[tag] == "" {
		return "", os.ErrNotExist
	}
	return f.images[tag], nil
}
func (f *fakeNet) TagImage(src, dst string) error {
	f.images[dst] = f.images[src]
	return nil
}

func actions(steps []Step) map[string]string {
	m := map[string]string{}
	for _, s := range steps {
		m[s.Item] = s.Action
	}
	return m
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestPlanWritesNothingAndApplyIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	net := &fakeNet{}
	opt := Options{StateDir: dir, Network: net}

	plan, err := Plan(opt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) || net.created {
		t.Fatal("plan changed the host")
	}
	a := actions(plan)
	for _, item := range []string{"state directory", "plane TLS", "env file", "policy", "container network"} {
		if a[item] != Create {
			t.Fatalf("plan %s = %q", item, a[item])
		}
	}

	if _, err := Apply(opt); err != nil {
		t.Fatal(err)
	}
	p := PathsFor(dir)
	if mode(t, p.Dir) != 0o700 || mode(t, p.TLS) != 0o700 {
		t.Fatal("directories not 0700")
	}
	for _, f := range []string{p.Env, p.Policy, p.CAKey, p.PlaneKey, p.CACert, p.PlaneCert} {
		if mode(t, f) != 0o600 {
			t.Fatalf("%s mode %v", f, mode(t, f))
		}
	}
	if !net.created {
		t.Fatal("network not created")
	}
	vals := ReadEnv(p.Env)
	for _, k := range generatedSecrets {
		if len(vals[k]) != 64 {
			t.Fatalf("%s not generated", k)
		}
	}
	if vals["RUSUI_PLANE_CA"] != p.CACert || vals["RUSUI_GITHUB_TOKEN"] != "" {
		t.Fatalf("env %v", vals)
	}

	envBefore, _ := os.ReadFile(p.Env)
	certBefore, _ := os.ReadFile(p.PlaneCert)
	again, err := Apply(opt)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range again {
		if s.Action == Create || s.Action == Update || s.Action == Rotate {
			t.Fatalf("second apply changed %s: %s", s.Item, s.Action)
		}
	}
	envAfter, _ := os.ReadFile(p.Env)
	certAfter, _ := os.ReadFile(p.PlaneCert)
	if string(envBefore) != string(envAfter) || string(certBefore) != string(certAfter) {
		t.Fatal("second apply rewrote files")
	}
}

func TestApplyKeepsOperatorFilesAndAddsMissingKeys(t *testing.T) {
	dir := t.TempDir()
	p := PathsFor(dir)
	if err := os.WriteFile(p.Policy, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Env, []byte("RUSUI_GITHUB_TOKEN=ghp_operator\nRUSUI_WORKER_SECRET=kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	steps, err := Apply(Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	a := actions(steps)
	if a["policy"] != Keep || a["env file"] != Update || a["container network"] != Skip {
		t.Fatalf("steps %v", a)
	}
	if b, _ := os.ReadFile(p.Policy); string(b) != "mine\n" {
		t.Fatal("policy overwritten")
	}
	vals := ReadEnv(p.Env)
	if vals["RUSUI_GITHUB_TOKEN"] != "ghp_operator" || vals["RUSUI_WORKER_SECRET"] != "kept" || len(vals["RUSUI_WEBHOOK_SECRET"]) != 64 {
		t.Fatalf("env %v", vals)
	}
	if mode(t, p.Env) != 0o600 {
		t.Fatal("env file not tightened to 0600")
	}
	for _, s := range steps {
		if strings.Contains(s.Detail, "ghp_operator") || strings.Contains(s.Detail, vals["RUSUI_WEBHOOK_SECRET"]) {
			t.Fatalf("secret in step %+v", s)
		}
		if s.Item == "RUSUI_GITHUB_TOKEN" {
			t.Fatal("token reported missing although set")
		}
	}
}

func TestRotateTLSReplacesOnlyTLS(t *testing.T) {
	dir := t.TempDir()
	if _, err := Apply(Options{StateDir: dir}); err != nil {
		t.Fatal(err)
	}
	p := PathsFor(dir)
	env1, _ := os.ReadFile(p.Env)
	ca1, _ := os.ReadFile(p.CACert)
	steps, err := Apply(Options{StateDir: dir, RotateTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	if actions(steps)["plane TLS"] != Rotate {
		t.Fatalf("steps %v", actions(steps))
	}
	env2, _ := os.ReadFile(p.Env)
	ca2, _ := os.ReadFile(p.CACert)
	if string(ca1) == string(ca2) || string(env1) != string(env2) {
		t.Fatal("rotate did not replace only TLS")
	}
}

func TestGeneratedCertificateServesThePlane(t *testing.T) {
	dir := t.TempDir()
	if _, err := Apply(Options{StateDir: dir}); err != nil {
		t.Fatal(err)
	}
	p := PathsFor(dir)
	pair, err := tls.LoadX509KeyPair(p.PlaneCert, p.PlaneKey)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	client, err := ops.TLSClient(p.CACert)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.Get(srv.URL) // https://127.0.0.1:port, verified against the CA
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()

	caPEM, _ := os.ReadFile(p.CACert)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	block, _ := pem.Decode(mustRead(t, p.PlaneCert))
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rusui.plane", "localhost", "127.0.0.1"} {
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: pool}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestApplyTightensLooseSecretFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := Apply(Options{StateDir: dir}); err != nil {
		t.Fatal(err)
	}
	p := PathsFor(dir)
	for path, m := range map[string]os.FileMode{p.Env: 0o644, p.CAKey: 0o644, p.Dir: 0o755} {
		if err := os.Chmod(path, m); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := Plan(Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if a := actions(plan); a["env file"] != Update || a["plane TLS"] != Update || a["state directory"] != Update {
		t.Fatalf("plan %v", a)
	}
	if mode(t, p.Env) != 0o644 {
		t.Fatal("plan changed a mode")
	}
	if _, err := Apply(Options{StateDir: dir}); err != nil {
		t.Fatal(err)
	}
	if mode(t, p.Env) != 0o600 || mode(t, p.CAKey) != 0o600 || mode(t, p.Dir) != 0o700 {
		t.Fatal("apply did not tighten modes")
	}
}

func TestNeedsYouHonoursProcessEnvironment(t *testing.T) {
	dir := t.TempDir()
	getenv := func(k string) string {
		return map[string]string{"RUSUI_GITHUB_TOKEN": "from-shell", "RUSUI_MODEL_UPSTREAM": "http://127.0.0.1:8317"}[k]
	}
	steps, err := Apply(Options{StateDir: dir, Getenv: getenv})
	if err != nil {
		t.Fatal(err)
	}
	a := actions(steps)
	if _, ok := a["RUSUI_GITHUB_TOKEN"]; ok {
		t.Fatal("github token reported although exported")
	}
	if _, ok := a["model access"]; ok {
		t.Fatal("model access reported although upstream exported")
	}
	if a["RUSUI_GUEST_IMAGE"] != NeedsYou {
		t.Fatal("guest image should still be needed")
	}
}

func TestRotateTightensLooseKeysAndAppendKeepsLastLine(t *testing.T) {
	dir := t.TempDir()
	if _, err := Apply(Options{StateDir: dir}); err != nil {
		t.Fatal(err)
	}
	p := PathsFor(dir)
	if err := os.Chmod(p.PlaneKey, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Options{StateDir: dir, RotateTLS: true}); err != nil {
		t.Fatal(err)
	}
	if mode(t, p.PlaneKey) != 0o600 {
		t.Fatalf("rotated key mode %v", mode(t, p.PlaneKey))
	}

	dir2 := t.TempDir()
	p2 := PathsFor(dir2)
	if err := os.WriteFile(p2.Env, []byte("RUSUI_XAI_API_KEY=operator-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Options{StateDir: dir2}); err != nil {
		t.Fatal(err)
	}
	vals := ReadEnv(p2.Env)
	if vals["RUSUI_XAI_API_KEY"] != "operator-value" || vals["RUSUI_TLS_CERT"] != p2.PlaneCert {
		t.Fatalf("append corrupted the last line: %v", vals)
	}
}

func TestApplyBuildsAndRecordsTheGuestImageOnce(t *testing.T) {
	dir := t.TempDir()
	rt := &fakeNet{}
	plan, err := Plan(Options{StateDir: dir, Network: rt})
	if err != nil {
		t.Fatal(err)
	}
	if actions(plan)["guest image"] != Create || rt.builds != 0 {
		t.Fatalf("plan %v builds %d", actions(plan), rt.builds)
	}
	for _, st := range plan {
		if st.Action == NeedsYou && st.Item == "RUSUI_GUEST_IMAGE" {
			t.Fatal("plan asks for a guest image it will build itself")
		}
	}
	steps, err := Apply(Options{StateDir: dir, Network: rt})
	if err != nil {
		t.Fatal(err)
	}
	if rt.builds != 1 {
		t.Fatalf("builds %d", rt.builds)
	}
	vals := ReadEnv(PathsFor(dir).Env)
	id, _ := rt.ImageID(guestimage.CacheTag())
	if vals["RUSUI_GUEST_IMAGE"] != guestimage.ContentTag(id) || vals["RUSUI_GUEST"] != "claude" || !rt.ImageExists(vals["RUSUI_GUEST_IMAGE"]) {
		t.Fatalf("env %v", vals)
	}
	if _, ok := actions(steps)["RUSUI_GUEST_IMAGE"]; ok {
		t.Fatal("guest image still reported as needs-you")
	}
	again, err := Apply(Options{StateDir: dir, Network: rt})
	if err != nil {
		t.Fatal(err)
	}
	if rt.builds != 1 || actions(again)["guest image"] != Keep || actions(again)["guest image env"] != Keep {
		t.Fatalf("second apply %v builds %d", actions(again), rt.builds)
	}
}

func TestApplyKeepsAnOperatorGuestImage(t *testing.T) {
	dir := t.TempDir()
	p := PathsFor(dir)
	if err := os.WriteFile(p.Env, []byte("RUSUI_GUEST_IMAGE=ghcr.io/me/guest:1\nRUSUI_GUEST=grok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Options{StateDir: dir, Network: &fakeNet{}}); err != nil {
		t.Fatal(err)
	}
	vals := ReadEnv(p.Env)
	if vals["RUSUI_GUEST_IMAGE"] != "ghcr.io/me/guest:1" || vals["RUSUI_GUEST"] != "grok" {
		t.Fatalf("operator choices overwritten: %v", vals)
	}
}

func TestSetEnvValuesRewritesExportedKeysInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rusui.env")
	if err := os.WriteFile(path, []byte("# comment\nexport RUSUI_GUEST_IMAGE=rusui-guest:old\nRUSUI_GUEST = \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setEnvValues(path, map[string]string{"RUSUI_GUEST_IMAGE": "rusui-guest:new", "RUSUI_GUEST": "claude"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Count(string(b), "RUSUI_GUEST_IMAGE") != 1 || strings.Count(string(b), "RUSUI_GUEST=") != 1 || !strings.Contains(string(b), "# comment") {
		t.Fatalf("env file:\n%s", b)
	}
	if v := ReadEnv(path); v["RUSUI_GUEST_IMAGE"] != "rusui-guest:new" || v["RUSUI_GUEST"] != "claude" {
		t.Fatalf("values %v", v)
	}
}

func TestPlanStillNeedsAGuestImageWithoutARuntime(t *testing.T) {
	plan, err := Plan(Options{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if actions(plan)["RUSUI_GUEST_IMAGE"] != NeedsYou {
		t.Fatalf("plan %v", actions(plan))
	}
}

func TestRebuildRecordsANewImageTag(t *testing.T) {
	dir := t.TempDir()
	rt := &fakeNet{}
	if _, err := Apply(Options{StateDir: dir, Network: rt}); err != nil {
		t.Fatal(err)
	}
	first := ReadEnv(PathsFor(dir).Env)["RUSUI_GUEST_IMAGE"]
	plan, err := Plan(Options{StateDir: dir, Network: rt, RebuildImage: true})
	if err != nil {
		t.Fatal(err)
	}
	if actions(plan)["guest image"] != Rotate || rt.builds != 1 {
		t.Fatalf("plan %v builds %d", actions(plan), rt.builds)
	}
	if _, err := Apply(Options{StateDir: dir, Network: rt, RebuildImage: true}); err != nil {
		t.Fatal(err)
	}
	second := ReadEnv(PathsFor(dir).Env)["RUSUI_GUEST_IMAGE"]
	if rt.builds != 2 || second == first || !strings.HasPrefix(second, guestimage.Repository+":") {
		t.Fatalf("rebuild recorded %q (was %q), builds %d", second, first, rt.builds)
	}
}

func TestApplyMigratesADockerfileHashTag(t *testing.T) {
	dir := t.TempDir()
	rt := &fakeNet{}
	if _, err := Apply(Options{StateDir: dir, Network: rt}); err != nil {
		t.Fatal(err)
	}
	p := PathsFor(dir)
	if err := setEnvValues(p.Env, map[string]string{"RUSUI_GUEST_IMAGE": "rusui-guest:357ec5bcff52f96b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Options{StateDir: dir, Network: rt}); err != nil {
		t.Fatal(err)
	}
	id, _ := rt.ImageID(guestimage.CacheTag())
	if got := ReadEnv(p.Env)["RUSUI_GUEST_IMAGE"]; got != guestimage.ContentTag(id) || rt.builds != 1 {
		t.Fatalf("env %q builds %d", got, rt.builds)
	}
}

func TestContentTagFollowsTheImageID(t *testing.T) {
	if got := guestimage.ContentTag("sha256:0123456789abcdef0123"); got != "rusui-guest:0123456789abcdef" {
		t.Fatal(got)
	}
	if guestimage.CacheTag() == guestimage.ContentTag("sha256:"+strings.Repeat("0", 64)) || !strings.Contains(guestimage.CacheTag(), ":build-") {
		t.Fatal(guestimage.CacheTag())
	}
}
