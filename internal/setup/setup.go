// Package setup prepares a host for the plane (ADR 0018). Plan reports what
// Apply would change; Apply is idempotent. Neither prints secret values.
package setup

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	guestimage "github.com/sannrox/rusui/build/guest-image"
	"github.com/sannrox/rusui/internal/env"
)

// Actions a step can report.
const (
	Create   = "create"
	Keep     = "keep"
	Rotate   = "rotate"
	Update   = "update"
	NeedsYou = "needs-you"
	Skip     = "skip"
	Remove   = "remove"
)

// Step is one line of a plan or apply report.
type Step struct {
	Action string `json:"action"`
	Item   string `json:"item"`
	Detail string `json:"detail,omitempty"`
}

// Network is the container runtime setup uses; env.DockerCLI satisfies it.
type Network interface {
	NetworkExists(name string) bool
	EnsureNetwork(name string) error
	ImageExists(tag string) bool
	BuildImage(tag string, dockerfile []byte, fresh bool) error
	ImageID(tag string) (string, error)
	TagImage(src, dst string) error
}

type Options struct {
	StateDir  string
	Addr      string
	RotateTLS bool
	// RebuildImage rebuilds the reference guest with --pull --no-cache; a
	// changed image gets a new recorded tag.
	RebuildImage   bool
	Network        Network // nil when no container runtime is installed
	ServiceManager ServiceManager
	Now            func() time.Time
	Getenv         func(string) string // process environment used as one-shot setup fallback
}

// DefaultStateDir is $XDG_DATA_HOME/rusui, or the platform default.
func DefaultStateDir(getenv func(string) string) string {
	if d := getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "rusui")
	}
	home := getenv("HOME")
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "rusui")
	}
	return filepath.Join(home, ".local", "share", "rusui")
}

// Paths are the files setup owns under the state directory.
type Paths struct {
	Dir, DB, Snapshots, Policy, Env         string
	CACert, CAKey, PlaneCert, PlaneKey, TLS string
}

func PathsFor(dir string) Paths {
	tls := filepath.Join(dir, "tls")
	return Paths{
		Dir: dir, DB: filepath.Join(dir, "rusui.db"), Snapshots: filepath.Join(dir, "snapshots"),
		Policy: filepath.Join(dir, "policy.yaml"), Env: filepath.Join(dir, "rusui.env"), TLS: tls,
		CACert: filepath.Join(tls, "ca.pem"), CAKey: filepath.Join(tls, "ca-key.pem"),
		PlaneCert: filepath.Join(tls, "plane.pem"), PlaneKey: filepath.Join(tls, "plane-key.pem"),
	}
}

// generatedSecrets are local-only values setup creates once.
var generatedSecrets = []string{"RUSUI_WORKER_SECRET", "RUSUI_WEBHOOK_SECRET", "RUSUI_SLACK_SECRET", "RUSUI_OPERATOR_TOKEN"}

// externalKeys are secrets or settings only the operator can supply.
var externalKeys = []string{
	"RUSUI_GITHUB_TOKEN", "RUSUI_AGENT_GITHUB_TOKEN", "RUSUI_GUEST", "RUSUI_GUEST_IMAGE",
	"RUSUI_MODEL_UPSTREAM", "RUSUI_ANTHROPIC_API_KEY", "RUSUI_XAI_API_KEY",
}

// Plan reports what Apply would do and changes nothing.
func Plan(o Options) ([]Step, error) { return run(o, false) }

// Apply performs the plan. Running it twice changes nothing the second time.
func Apply(o Options) ([]Step, error) { return run(o, true) }

func run(o Options, apply bool) ([]Step, error) {
	if o.StateDir == "" {
		return nil, fmt.Errorf("setup: state directory required")
	}
	stateDir, err := filepath.Abs(o.StateDir)
	if err != nil {
		return nil, fmt.Errorf("setup: resolve state directory: %w", err)
	}
	o.StateDir = stateDir
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Getenv == nil {
		o.Getenv = func(string) string { return "" }
	}
	p := PathsFor(o.StateDir)
	var steps []Step
	add := func(action, item, detail string) { steps = append(steps, Step{action, item, detail}) }

	for _, d := range []struct{ path, item string }{{p.Dir, "state directory"}, {p.Snapshots, "snapshots"}, {p.TLS, "tls directory"}} {
		if isDir(d.path) {
			st, err := tighten(d.path, 0o700, d.item, apply)
			if err != nil {
				return steps, err
			}
			steps = append(steps, st)
			continue
		}
		add(Create, d.item, d.path)
		if apply {
			if err := os.MkdirAll(d.path, 0o700); err != nil {
				return steps, err
			}
		}
	}

	tlsExists := exists(p.CACert) && exists(p.CAKey) && exists(p.PlaneCert) && exists(p.PlaneKey)
	switch {
	case tlsExists && !o.RotateTLS:
		action := Keep
		for _, k := range []string{p.CAKey, p.PlaneKey} {
			st, err := tighten(k, 0o600, "plane TLS", apply)
			if err != nil {
				return steps, err
			}
			if st.Action == Update {
				action = Update
			}
		}
		add(action, "plane TLS", p.TLS)
	default:
		action := Create
		if tlsExists {
			action = Rotate
		}
		add(action, "plane TLS", "CA and certificate for 127.0.0.1, localhost, rusui.plane")
		if apply {
			if err := writeTLS(p, o.Now()); err != nil {
				return steps, err
			}
		}
	}

	envStep, err := ensureEnv(p, apply)
	if err != nil {
		return steps, err
	}
	steps = append(steps, envStep)

	if exists(p.Policy) {
		add(Keep, "policy", p.Policy)
	} else {
		add(Create, "policy", p.Policy+" (skeleton, no projects)")
		if apply {
			if err := os.WriteFile(p.Policy, []byte(policySkeleton), 0o600); err != nil {
				return steps, err
			}
		}
	}

	switch {
	case o.Network == nil:
		add(Skip, "container network", "no docker or podman; container guests unavailable")
	case o.Network.NetworkExists(env.TrustedNetwork):
		add(Keep, "container network", env.TrustedNetwork)
	default:
		add(Create, "container network", env.TrustedNetwork)
		if apply {
			if err := o.Network.EnsureNetwork(env.TrustedNetwork); err != nil {
				return steps, err
			}
		}
	}

	if o.Network != nil {
		st, err := ensureGuestImage(p, o.Network, apply, o.RebuildImage)
		if err != nil {
			return steps, err
		}
		steps = append(steps, st...)
	}
	if o.ServiceManager != nil {
		var st Step
		var err error
		if apply {
			st, err = o.ServiceManager.Apply(o, p)
		} else {
			st, err = o.ServiceManager.Plan(o, p)
		}
		if err != nil {
			return steps, err
		}
		steps = append(steps, st)
	}

	vals := readEnv(p.Env)
	for k, v := range vals {
		if v == "" && o.ServiceManager == nil {
			vals[k] = o.Getenv(k)
		}
	}
	for _, k := range externalKeys {
		if _, ok := vals[k]; !ok {
			if o.ServiceManager == nil {
				vals[k] = o.Getenv(k)
			}
		}
	}
	if o.Network != nil && vals["RUSUI_GUEST_IMAGE"] == "" {
		// The guest image step above builds and records it; plan and apply
		// must agree that the operator need not supply one.
		vals["RUSUI_GUEST_IMAGE"] = guestimage.CacheTag()
	}
	for _, n := range needsYou(vals) {
		if o.ServiceManager != nil {
			n[1] += "; persist it in " + p.Env + " for the user service"
		}
		add(NeedsYou, n[0], n[1])
	}
	return steps, nil
}

// needsYou names the external inputs still missing, with what to do.
func needsYou(vals map[string]string) [][2]string {
	var out [][2]string
	if vals["RUSUI_GITHUB_TOKEN"] == "" {
		out = append(out, [2]string{"RUSUI_GITHUB_TOKEN", "read-only token for intake; e.g. a fine-grained token, or `gh auth token`"})
	}
	if vals["RUSUI_MODEL_UPSTREAM"] == "" && vals["RUSUI_ANTHROPIC_API_KEY"] == "" && vals["RUSUI_XAI_API_KEY"] == "" {
		out = append(out, [2]string{"model access", "set RUSUI_ANTHROPIC_API_KEY or RUSUI_XAI_API_KEY, or RUSUI_MODEL_UPSTREAM for a CLI proxy you logged in to"})
	}
	if vals["RUSUI_GUEST_IMAGE"] == "" {
		out = append(out, [2]string{"RUSUI_GUEST_IMAGE", "guest image tag; setup builds the reference image when Docker or Podman is installed"})
	}
	return out
}

func ensureEnv(p Paths, apply bool) (Step, error) {
	vals := readEnv(p.Env)
	want := map[string]string{
		"RUSUI_TLS_CERT": p.PlaneCert, "RUSUI_TLS_KEY": p.PlaneKey, "RUSUI_PLANE_CA": p.CACert,
	}
	var missing []string
	for _, k := range append(append([]string{"RUSUI_TLS_CERT", "RUSUI_TLS_KEY", "RUSUI_PLANE_CA"}, generatedSecrets...), externalKeys...) {
		if _, ok := vals[k]; !ok {
			missing = append(missing, k)
		}
	}
	step := Step{Keep, "env file", p.Env}
	if exists(p.Env) && len(missing) == 0 {
		return tighten(p.Env, 0o600, "env file", apply)
	}
	if !exists(p.Env) {
		step = Step{Create, "env file", p.Env + " (generated local secrets; external keys left empty)"}
	} else if len(missing) > 0 {
		step = Step{Update, "env file", "add " + strings.Join(missing, ", ")}
	}
	if !apply || step.Action == Keep {
		return step, nil
	}
	var b strings.Builder
	if cur, err := os.ReadFile(p.Env); err == nil && len(cur) > 0 && cur[len(cur)-1] != '\n' {
		b.WriteString("\n") // keep the operator's last line intact
	}
	for _, k := range missing {
		v := want[k]
		for _, g := range generatedSecrets {
			if k == g {
				s, err := randomHex(32)
				if err != nil {
					return step, err
				}
				v = s
			}
		}
		fmt.Fprintf(&b, "%s=%s\n", k, v)
	}
	f, err := os.OpenFile(p.Env, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return step, err
	}
	defer func() { _ = f.Close() }()
	if err := f.Chmod(0o600); err != nil {
		return step, err
	}
	_, err = f.WriteString(b.String())
	return step, err
}

// ensureGuestImage builds the reference guest (ADR 0018 D4) and records it
// in the env file, tagged by its image ID, unless the operator chose
// another image. The Dockerfile hash is only the build cache key.
func ensureGuestImage(p Paths, rt Network, apply, rebuild bool) ([]Step, error) {
	cache := guestimage.CacheTag()
	built := rt.ImageExists(cache)
	var steps []Step
	switch {
	case built && !rebuild:
		steps = append(steps, Step{Keep, "guest image", cache})
	case built:
		steps = append(steps, Step{Rotate, "guest image", "rebuild " + cache + " with --pull --no-cache"})
	default:
		steps = append(steps, Step{Create, "guest image", "build " + cache + " (git, gh, Node.js, claude-agent-acp)"})
	}
	if apply && (!built || rebuild) {
		if err := rt.BuildImage(cache, guestimage.Dockerfile, rebuild); err != nil {
			return steps, err
		}
		built = true
	}
	tag := "" // unknown until built
	if built && (apply || !rebuild) {
		id, err := rt.ImageID(cache)
		if err != nil {
			return steps, err
		}
		tag = guestimage.ContentTag(id)
		if !rt.ImageExists(tag) {
			steps = append(steps, Step{Update, "guest image tag", tag})
			if apply {
				if err := rt.TagImage(cache, tag); err != nil {
					return steps, err
				}
			}
		}
	}

	vals := readEnv(p.Env)
	cur := vals["RUSUI_GUEST_IMAGE"]
	if cur != "" && !strings.HasPrefix(cur, guestimage.Repository+":") {
		return append(steps, Step{Keep, "guest image env", "operator image " + cur}), nil
	}
	set := map[string]string{}
	if tag == "" || cur != tag {
		set["RUSUI_GUEST_IMAGE"] = tag
	}
	if vals["RUSUI_GUEST"] == "" {
		set["RUSUI_GUEST"] = "claude" // the reference image carries the Claude Code adapter
	}
	if len(set) == 0 {
		return append(steps, Step{Keep, "guest image env", tag}), nil
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	detail := "set " + strings.Join(keys, ", ")
	if tag == "" {
		detail += " (image tag after build)"
	}
	steps = append(steps, Step{Update, "guest image env", detail})
	if apply {
		if err := setEnvValues(p.Env, set); err != nil {
			return steps, err
		}
	}
	return steps, nil
}

// setEnvValues rewrites KEY= lines in place and appends absent keys.
func setEnvValues(path string, set map[string]string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	done := map[string]bool{}
	for i, line := range lines {
		k, _, ok := envLine(line)
		if !ok {
			continue
		}
		if v, want := set[k]; want {
			lines[i] = k + "=" + v
			done[k] = true
		}
	}
	for k, v := range set {
		if !done[k] {
			lines = append(lines, k+"="+v)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// readEnv parses KEY=VALUE lines; comments and blanks are ignored.
func readEnv(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := envLine(sc.Text()); ok {
			out[k] = v
		}
	}
	return out
}

// envLine parses one `[export ]KEY=VALUE` line; matching outer quotes are
// removed, but shell expansion is not evaluated.
func envLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	k, v, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(k, "export ")), parseEnvValue(strings.TrimSpace(v)), true
}

func parseEnvValue(value string) string {
	if len(value) < 2 || value[0] != value[len(value)-1] || (value[0] != '\'' && value[0] != '"') {
		return value
	}
	value = value[1 : len(value)-1]
	if value == "" || value[0] == '\'' {
		return value
	}
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+1 < len(value) {
			switch value[i+1] {
			case '\\', '"', '$', '`':
				b.WriteByte(value[i+1])
				i++
				continue
			}
		}
		b.WriteByte(value[i])
	}
	return b.String()
}

// ReadEnv exposes the env file to the CLI (for the final diagnose).
func ReadEnv(path string) map[string]string { return readEnv(path) }

func writeTLS(p Paths, now time.Time) error {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "rusui plane CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: env.PlaneHost},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(2, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{env.PlaneHost, "localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	for _, w := range []struct {
		path  string
		block string
		der   func() ([]byte, error)
	}{
		{p.CACert, "CERTIFICATE", func() ([]byte, error) { return caDER, nil }},
		{p.CAKey, "EC PRIVATE KEY", func() ([]byte, error) { return x509.MarshalECPrivateKey(caKey) }},
		{p.PlaneCert, "CERTIFICATE", func() ([]byte, error) { return leafDER, nil }},
		{p.PlaneKey, "EC PRIVATE KEY", func() ([]byte, error) { return x509.MarshalECPrivateKey(leafKey) }},
	} {
		der, err := w.der()
		if err != nil {
			return err
		}
		if err := os.WriteFile(w.path, pem.EncodeToMemory(&pem.Block{Type: w.block, Bytes: der}), 0o600); err != nil {
			return err
		}
		// WriteFile keeps an existing file's mode; rotation must not
		// inherit a loose one.
		if err := os.Chmod(w.path, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// tighten reports (and on apply, fixes) permissions looser than want.
func tighten(path string, want os.FileMode, item string, apply bool) (Step, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return Step{}, err
	}
	if fi.Mode().Perm()&^want == 0 {
		return Step{Keep, item, path}, nil
	}
	if apply {
		if err := os.Chmod(path, want); err != nil {
			return Step{}, err
		}
	}
	return Step{Update, item, fmt.Sprintf("tighten %s to %o", path, want)}, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

const policySkeleton = `# Written by rusui setup. Bind repositories under projects; see
# docs/configuration.md. Unknown fields fail closed.
version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: false
  close: false
  implement: false
  land: false
  max_reviews_per_repo_per_utc_day: 50
projects: {}
`
