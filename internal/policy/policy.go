package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/sannrox/rusui/internal/guest"
	"gopkg.in/yaml.v3"
)

const (
	EgressNone    = "none"
	EgressTrusted = "trusted"
	EgressCustom  = "custom"
	EgressFull    = "full"
	KindReview    = "review"
	KindRun       = "run"
	KindScheduled = "scheduled"
	// Local sessions remain project-opt-in and are never inherited from defaults.
	KindLocal                 = "local"
	BudgetMaxConcurrentLeases = "max_concurrent_leases"
	ShipPullRequest           = "pull-request"
	ShipPushBase              = "push-base"
	RepairDocs                = "docs"
	LabelHold                 = "rusui:hold"
	LabelSecurity             = "security"
	DefaultMaxRepairs         = 1
)

type File struct {
	Guests   map[string]guest.Entry `yaml:"guests,omitempty"`
	Version  int                    `yaml:"version"`
	Defaults DefaultsYAML           `yaml:"defaults"`
	Projects map[string]ProjectYAML `yaml:"projects"`
}

type DefaultsYAML struct {
	NeverRelease               bool     `yaml:"never_release"`
	NeverLeakPrivateToPublic   bool     `yaml:"never_leak_private_to_public"`
	SessionKinds               []string `yaml:"session_kinds"`
	Egress                     string   `yaml:"egress"`
	Visibility                 string   `yaml:"visibility"`
	Review                     *bool    `yaml:"review"`
	Comments                   bool     `yaml:"comments"`
	Comment                    bool     `yaml:"comment"`
	Close                      bool     `yaml:"close"`
	Implement                  bool     `yaml:"implement"`
	Land                       bool     `yaml:"land"`
	Repair                     string   `yaml:"repair"`
	ProtectedLabels            []string `yaml:"protected_labels"`
	MaxReviewsPerRepoPerUTCDay int      `yaml:"max_reviews_per_repo_per_utc_day"`
	MaxRepairsPerRepoPerUTCDay int      `yaml:"max_repairs_per_repo_per_utc_day"`
	// MCPGateway is the governed tool gateway URL. When set, it is the only
	// remote MCP server a session sees and projects may not name others.
	MCPGateway string `yaml:"mcp_gateway"`
}

type ProjectGuests struct {
	Default string   `yaml:"default"`
	Allowed []string `yaml:"allowed"`
}

type ProjectYAML struct {
	Guests       *ProjectGuests      `yaml:"guests"`
	Repos        map[string]RepoYAML `yaml:"repos"`
	SessionKinds []string            `yaml:"session_kinds"`
	LocalRuntime *LocalRuntimeYAML   `yaml:"local_runtime"`
	Egress       string              `yaml:"egress"`
	Size         string              `yaml:"size"`
	Budgets      map[string]int      `yaml:"budgets"`
	Permissions  []AllowRule         `yaml:"permissions"`
	// Secrets is the project allowlist of plane secret ids injected at wake.
	Secrets []string `yaml:"secrets"`
	// Ship is pull-request (default) or push-base (ADR 0068).
	Ship string `yaml:"ship"`
	// Tenant is the isolation key for sessions created in this project.
	// Empty means the single-operator default tenant.
	Tenant string `yaml:"tenant"`
	// MCPServers are remote MCP tool servers the session connects to
	// directly. Refused when defaults.mcp_gateway is set.
	MCPServers []MCPServer `yaml:"mcp_servers"`
}

// MCPServer is a remote MCP tool server reached over Streamable HTTP.
type MCPServer struct {
	Name string `yaml:"name" json:"name"`
	URL  string `yaml:"url" json:"url"`
}

type LocalRuntimeYAML struct {
	Argv []string `yaml:"argv"`
	Cwd  string   `yaml:"cwd"`
}

type RepoYAML struct {
	Visibility                 *string `yaml:"visibility"`
	Review                     *bool   `yaml:"review"`
	Comments                   *bool    `yaml:"comments"`
	Comment                    *bool    `yaml:"comment"`
	Close                      *bool    `yaml:"close"`
	Implement                  *bool    `yaml:"implement"`
	Land                       *bool    `yaml:"land"`
	Repair                     *string  `yaml:"repair"`
	ProtectedLabels            []string `yaml:"protected_labels"`
	MaxReviewsPerRepoPerUTCDay *int     `yaml:"max_reviews_per_repo_per_utc_day"`
	MaxRepairsPerRepoPerUTCDay *int     `yaml:"max_repairs_per_repo_per_utc_day"`
}

type AllowRule struct {
	Tool    string `yaml:"tool" json:"tool"`
	Kind    string `yaml:"kind" json:"kind"`
	Command string `yaml:"command" json:"command"`
	Action  string `yaml:"action" json:"action,omitempty"` // "" or "allow", or "reject" (ADR 0017 D3)
}

type Repo struct {
	Project                    string
	Visibility                 string
	Review                     bool
	Comments                   bool
	Comment                    bool
	Close                      bool
	Implement                  bool
	Land                       bool
	Repair                     string
	ProtectedLabels            []string
	MaxReviewsPerRepoPerUTCDay int
	MaxRepairsPerRepoPerUTCDay int
	NeverRelease               bool
	NeverLeakPrivateToPublic   bool
}

type Project struct {
	Guests       *ProjectGuests
	Slug         string
	SessionKinds []string
	LocalRuntime *LocalRuntime
	Egress       string
	Size         string
	Budgets      map[string]int
	Permissions  []AllowRule
	// Secrets is the project allowlist of plane secret ids injected at wake.
	Secrets []string
	// Ship is pull-request or push-base (ADR 0068). Parse stores the
	// default as pull-request when the field is omitted.
	Ship  string
	Repos []string
	// Tenant is the isolation key; empty means the default tenant.
	Tenant     string
	MCPServers []MCPServer
}

type LocalRuntime struct {
	Argv []string
	Cwd  string
}

type Effective struct {
	Guests   map[string]guest.Entry
	Hash     string
	Raw      []byte
	Projects map[string]Project
	Repos    map[string]Repo
	// MCPGateway is defaults.mcp_gateway; empty means gateway mode is off.
	MCPGateway string
}

func Load(path string) (*Effective, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

func Parse(raw []byte) (*Effective, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	if f.Version != 2 {
		return nil, fmt.Errorf("policy: unsupported version %d", f.Version)
	}
	if f.Projects == nil {
		return nil, fmt.Errorf("policy: projects required")
	}
	out := &Effective{
		Raw:      append([]byte(nil), raw...),
		Projects: map[string]Project{},
		Repos:    map[string]Repo{},
	}
	guests, err := validGuests(f.Guests)
	if err != nil {
		return nil, err
	}
	out.Guests = guests
	if f.Defaults.MCPGateway != "" {
		if err := validMCPURL(f.Defaults.MCPGateway); err != nil {
			return nil, fmt.Errorf("policy: mcp_gateway %w", err)
		}
		out.MCPGateway = f.Defaults.MCPGateway
	}
	defKinds := f.Defaults.SessionKinds
	if len(defKinds) == 0 {
		defKinds = []string{KindReview, KindRun, KindScheduled}
	}
	if slices.Contains(defKinds, KindLocal) {
		return nil, fmt.Errorf("policy: local session kind cannot be enabled in defaults")
	}
	defEgress := f.Defaults.Egress
	if defEgress == "" {
		defEgress = EgressTrusted
	}
	for slug, y := range f.Projects {
		if err := validSlug(slug); err != nil {
			return nil, err
		}
		kinds := y.SessionKinds
		if len(kinds) == 0 {
			kinds = append([]string(nil), defKinds...)
		}
		if err := validKinds(slug, kinds); err != nil {
			return nil, err
		}
		localRuntime, err := validLocalRuntime(slug, y.LocalRuntime, kinds)
		if err != nil {
			return nil, err
		}
		if err := validRules(slug, y.Permissions); err != nil {
			return nil, err
		}
		egress := y.Egress
		if egress == "" {
			egress = defEgress
		}
		if err := validEgress(slug, egress); err != nil {
			return nil, err
		}
		budgets, err := validBudgets(slug, y.Budgets)
		if err != nil {
			return nil, err
		}
		size, err := validSize(slug, y.Size)
		if err != nil {
			return nil, err
		}
		secrets, err := validSecrets(slug, y.Secrets)
		if err != nil {
			return nil, err
		}
		ship, err := validShip(slug, y.Ship)
		if err != nil {
			return nil, err
		}
		if err := validProjectGuests(slug, y.Guests, guests); err != nil {
			return nil, err
		}
		if err := validTenant(slug, y.Tenant); err != nil {
			return nil, err
		}
		if err := validMCPServers(slug, y.MCPServers, out.MCPGateway); err != nil {
			return nil, err
		}
		p := Project{
			Guests:       y.Guests,
			Slug:         slug,
			SessionKinds: kinds,
			LocalRuntime: localRuntime,
			Egress:       egress,
			Size:         size,
			Budgets:      budgets,
			Permissions:  y.Permissions,
			Secrets:      secrets,
			Ship:         ship,
			Tenant:       y.Tenant,
			MCPServers:   y.MCPServers,
		}
		for name, ry := range y.Repos {
			if _, ok := out.Repos[name]; ok {
				return nil, fmt.Errorf("policy: %s bound to more than one project", name)
			}
			r := Repo{
				Project:                    slug,
				Visibility:                 "private",
				Review:                     true,
				MaxReviewsPerRepoPerUTCDay: 50,
				MaxRepairsPerRepoPerUTCDay: DefaultMaxRepairs,
				NeverRelease:               true,
				NeverLeakPrivateToPublic:   true,
			}
			if f.Defaults.Visibility != "" {
				r.Visibility = f.Defaults.Visibility
			}
			if f.Defaults.MaxReviewsPerRepoPerUTCDay != 0 {
				r.MaxReviewsPerRepoPerUTCDay = f.Defaults.MaxReviewsPerRepoPerUTCDay
			}
			if f.Defaults.Review != nil {
				r.Review = *f.Defaults.Review
			}
			r.Comments = f.Defaults.Comments
			r.Comment = f.Defaults.Comment
			r.Close = f.Defaults.Close
			r.Implement = f.Defaults.Implement
			r.Land = f.Defaults.Land
			repair, err := parseRepairCategory(f.Defaults.Repair)
			if err != nil {
				return nil, fmt.Errorf("policy: %s %w", name, err)
			}
			r.Repair = repair
			r.ProtectedLabels = mergeProtectedLabels(f.Defaults.ProtectedLabels)
			if f.Defaults.MaxRepairsPerRepoPerUTCDay != 0 {
				r.MaxRepairsPerRepoPerUTCDay = f.Defaults.MaxRepairsPerRepoPerUTCDay
			}
			if ry.Visibility != nil {
				r.Visibility = *ry.Visibility
			}
			if ry.Review != nil {
				r.Review = *ry.Review
			}
			if ry.Comments != nil {
				r.Comments = *ry.Comments
			}
			if ry.Comment != nil {
				r.Comment = *ry.Comment
			}
			if ry.Close != nil {
				r.Close = *ry.Close
			}
			if ry.Implement != nil {
				r.Implement = *ry.Implement
			}
			if ry.Land != nil {
				r.Land = *ry.Land
			}
			if ry.Repair != nil {
				repair, err := parseRepairCategory(*ry.Repair)
				if err != nil {
					return nil, fmt.Errorf("policy: %s %w", name, err)
				}
				r.Repair = repair
			}
			if len(ry.ProtectedLabels) > 0 {
				r.ProtectedLabels = mergeProtectedLabels(append(append([]string(nil), r.ProtectedLabels...), ry.ProtectedLabels...))
			}
			if ry.MaxReviewsPerRepoPerUTCDay != nil {
				r.MaxReviewsPerRepoPerUTCDay = *ry.MaxReviewsPerRepoPerUTCDay
			}
			if ry.MaxRepairsPerRepoPerUTCDay != nil {
				r.MaxRepairsPerRepoPerUTCDay = *ry.MaxRepairsPerRepoPerUTCDay
			}
			if r.MaxRepairsPerRepoPerUTCDay < 0 {
				return nil, fmt.Errorf("policy: %s max_repairs_per_repo_per_utc_day must be >= 0", name)
			}
			if r.Visibility != "public" && r.Visibility != "private" {
				return nil, fmt.Errorf("policy: %s invalid visibility", name)
			}
			out.Repos[name] = r
			p.Repos = append(p.Repos, name)
		}
		sort.Strings(p.Repos)
		out.Projects[slug] = p
	}
	sum := sha256.Sum256(raw)
	out.Hash = hex.EncodeToString(sum[:])
	return out, nil
}

func validSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("policy: empty project slug")
	}
	for i, c := range slug {
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || (c == '-' && i > 0)
		if !ok {
			return fmt.Errorf("policy: invalid project slug %q", slug)
		}
	}
	if slug[0] < 'a' || slug[0] > 'z' {
		return fmt.Errorf("policy: invalid project slug %q", slug)
	}
	return nil
}

func validTenant(slug, tenant string) error {
	if tenant == "" {
		return nil
	}
	if err := validSlug(tenant); err != nil {
		return fmt.Errorf("policy: %s invalid tenant %q", slug, tenant)
	}
	return nil
}

func validRules(slug string, rules []AllowRule) error {
	for i, r := range rules {
		switch r.Action {
		case "", "allow":
		case "reject":
			if r.Tool == "" && r.Kind == "" && r.Command == "" {
				return fmt.Errorf("policy: %s permission %d: reject rule needs tool, kind, or command", slug, i)
			}
		default:
			return fmt.Errorf("policy: %s permission %d: invalid action %q", slug, i, r.Action)
		}
	}
	return nil
}

func validKinds(slug string, kinds []string) error {
	seen := map[string]bool{}
	for _, k := range kinds {
		switch k {
		case KindReview, KindRun, KindScheduled, KindLocal:
		default:
			return fmt.Errorf("policy: %s invalid session kind %q", slug, k)
		}
		if seen[k] {
			return fmt.Errorf("policy: %s duplicate session kind %q", slug, k)
		}
		seen[k] = true
	}
	return nil
}

func validLocalRuntime(slug string, runtime *LocalRuntimeYAML, kinds []string) (*LocalRuntime, error) {
	enabled := slices.Contains(kinds, KindLocal)
	if enabled != (runtime != nil) {
		return nil, fmt.Errorf("policy: %s must configure local_runtime exactly when local is enabled", slug)
	}
	if !enabled {
		return nil, nil
	}
	if len(runtime.Argv) == 0 || runtime.Argv[0] == "" {
		return nil, fmt.Errorf("policy: %s local_runtime.argv must contain an executable", slug)
	}
	for i, arg := range runtime.Argv {
		if strings.IndexByte(arg, 0) >= 0 {
			return nil, fmt.Errorf("policy: %s local_runtime.argv[%d] contains NUL", slug, i)
		}
	}
	if runtime.Cwd == "" || strings.IndexByte(runtime.Cwd, 0) >= 0 || !filepath.IsAbs(runtime.Cwd) || filepath.Clean(runtime.Cwd) != runtime.Cwd {
		return nil, fmt.Errorf("policy: %s local_runtime.cwd must be a clean absolute path", slug)
	}
	return &LocalRuntime{Argv: append([]string(nil), runtime.Argv...), Cwd: runtime.Cwd}, nil
}

func validEgress(slug, egress string) error {
	switch egress {
	case EgressNone, EgressTrusted, EgressCustom, EgressFull:
		return nil
	default:
		return fmt.Errorf("policy: %s invalid egress %q", slug, egress)
	}
}

// MCPGatewayName is the server name a session sees for the gateway.
const MCPGatewayName = "gateway"

var mcpName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// validMCPServers refuses every direct server while a gateway is configured,
// so gateway mode fails closed instead of mixing direct and governed calls.
func validMCPServers(slug string, servers []MCPServer, gateway string) error {
	seen := map[string]bool{}
	for _, s := range servers {
		if gateway != "" {
			return fmt.Errorf("policy: %s mcp server %q connects directly; mcp_gateway routes every remote MCP server through the gateway", slug, s.Name)
		}
		if !mcpName.MatchString(s.Name) || s.Name == MCPGatewayName {
			return fmt.Errorf("policy: %s invalid mcp server name %q", slug, s.Name)
		}
		if seen[s.Name] {
			return fmt.Errorf("policy: %s duplicate mcp server %q", slug, s.Name)
		}
		seen[s.Name] = true
		if err := validMCPURL(s.URL); err != nil {
			return fmt.Errorf("policy: %s mcp server %q %w", slug, s.Name, err)
		}
	}
	return nil
}

func validMCPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return fmt.Errorf("url must be http(s) with a host and no userinfo")
	}
	return nil
}

// SessionMCPServers is the remote MCP server list for a session in p. In
// gateway mode it is the gateway alone; the runner authenticates it with the
// turn credential.
func (e *Effective) SessionMCPServers(p Project) []MCPServer {
	if e.MCPGateway != "" {
		return []MCPServer{{Name: MCPGatewayName, URL: e.MCPGateway}}
	}
	return p.MCPServers
}

func validSecrets(slug string, ids []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !ValidSecretID(id) {
			return nil, fmt.Errorf("policy: %s invalid secret id %q", slug, id)
		}
		if seen[id] {
			return nil, fmt.Errorf("policy: %s duplicate secret id %q", slug, id)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// ValidSecretID is a plane secret id: lowercase, env-safe, short.
func ValidSecretID(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for i, c := range id {
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || (c == '_' && i > 0)
		if !ok {
			return false
		}
	}
	return id[0] >= 'a' && id[0] <= 'z'
}

// AllowSecrets reports whether every requested id is in the project allowlist.
func AllowSecrets(allow, want []string) error {
	set := map[string]bool{}
	for _, id := range allow {
		set[id] = true
	}
	for _, id := range want {
		if !set[id] {
			return fmt.Errorf("secret %s", id)
		}
	}
	return nil
}

func validSize(slug, size string) (string, error) {
	switch size {
	case "", "small", "medium", "large":
		return size, nil
	default:
		return "", fmt.Errorf("policy: %s invalid size %q", slug, size)
	}
}

func validShip(slug, ship string) (string, error) {
	switch ship {
	case "", ShipPullRequest:
		return ShipPullRequest, nil
	case ShipPushBase:
		return ShipPushBase, nil
	default:
		return "", fmt.Errorf("policy: %s invalid ship %q", slug, ship)
	}
}

func validBudgets(slug string, in map[string]int) (map[string]int, error) {
	out := map[string]int{BudgetMaxConcurrentLeases: 1}
	for k, v := range in {
		if k != BudgetMaxConcurrentLeases {
			return nil, fmt.Errorf("policy: %s unsupported budget %q", slug, k)
		}
		if v < 1 {
			return nil, fmt.Errorf("policy: %s %s must be >= 1", slug, k)
		}
		out[k] = v
	}
	return out, nil
}

func (e *Effective) Repo(name string) (Repo, bool) {
	r, ok := e.Repos[name]
	return r, ok
}

func (e *Effective) Project(slug string) (Project, bool) {
	p, ok := e.Projects[slug]
	return p, ok
}

func (e *Effective) ProjectForRepo(name string) (Project, bool) {
	r, ok := e.Repos[name]
	if !ok {
		return Project{}, false
	}
	return e.Project(r.Project)
}

func (p Project) AllowsKind(kind string) bool {
	return slices.Contains(p.SessionKinds, kind)
}

func (p Project) MaxConcurrentLeases() int {
	if p.Budgets == nil {
		return 1
	}
	n := p.Budgets[BudgetMaxConcurrentLeases]
	if n < 1 {
		return 1
	}
	return n
}

func parseRepairCategory(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "off", "false":
		return "", nil
	case RepairDocs:
		return RepairDocs, nil
	default:
		return "", fmt.Errorf("unknown repair category %q", raw)
	}
}

func mergeProtectedLabels(in []string) []string {
	seen := map[string]bool{LabelHold: true, LabelSecurity: true}
	out := []string{LabelHold, LabelSecurity}
	for _, l := range in {
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}
