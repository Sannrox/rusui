package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"

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
)

type File struct {
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
	Review                     bool     `yaml:"review"`
	Comments                   bool     `yaml:"comments"`
	Close                      bool     `yaml:"close"`
	Implement                  bool     `yaml:"implement"`
	Land                       bool     `yaml:"land"`
	MaxReviewsPerRepoPerUTCDay int      `yaml:"max_reviews_per_repo_per_utc_day"`
}

type ProjectYAML struct {
	Repos        map[string]RepoYAML `yaml:"repos"`
	SessionKinds []string            `yaml:"session_kinds"`
	Egress       string              `yaml:"egress"`
	Budgets      map[string]int      `yaml:"budgets"`
	Permissions  []AllowRule         `yaml:"permissions"`
}

type RepoYAML struct {
	Visibility                 *string `yaml:"visibility"`
	Review                     *bool   `yaml:"review"`
	Comments                   *bool   `yaml:"comments"`
	Close                      *bool   `yaml:"close"`
	Implement                  *bool   `yaml:"implement"`
	Land                       *bool   `yaml:"land"`
	MaxReviewsPerRepoPerUTCDay *int    `yaml:"max_reviews_per_repo_per_utc_day"`
}

type AllowRule struct {
	Tool    string `yaml:"tool" json:"tool"`
	Kind    string `yaml:"kind" json:"kind"`
	Command string `yaml:"command" json:"command"`
}

type Repo struct {
	Project                    string
	Visibility                 string
	Review                     bool
	Comments                   bool
	Close                      bool
	Implement                  bool
	Land                       bool
	MaxReviewsPerRepoPerUTCDay int
	NeverRelease               bool
	NeverLeakPrivateToPublic   bool
}

type Project struct {
	Slug         string
	SessionKinds []string
	Egress       string
	Budgets      map[string]int
	Permissions  []AllowRule
	Repos        []string
}

type Effective struct {
	Hash     string
	Raw      []byte
	Projects map[string]Project
	Repos    map[string]Repo
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
	defKinds := f.Defaults.SessionKinds
	if len(defKinds) == 0 {
		defKinds = []string{KindReview, KindRun, KindScheduled}
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
		egress := y.Egress
		if egress == "" {
			egress = defEgress
		}
		if err := validEgress(slug, egress); err != nil {
			return nil, err
		}
		p := Project{
			Slug:         slug,
			SessionKinds: kinds,
			Egress:       egress,
			Budgets:      y.Budgets,
			Permissions:  y.Permissions,
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
				NeverRelease:               true,
				NeverLeakPrivateToPublic:   true,
			}
			if f.Defaults.Visibility != "" {
				r.Visibility = f.Defaults.Visibility
			}
			if f.Defaults.MaxReviewsPerRepoPerUTCDay != 0 {
				r.MaxReviewsPerRepoPerUTCDay = f.Defaults.MaxReviewsPerRepoPerUTCDay
			}
			r.Comments = f.Defaults.Comments
			r.Close = f.Defaults.Close
			r.Implement = f.Defaults.Implement
			r.Land = f.Defaults.Land
			if ry.Visibility != nil {
				r.Visibility = *ry.Visibility
			}
			if ry.Review != nil {
				r.Review = *ry.Review
			}
			if ry.Comments != nil {
				r.Comments = *ry.Comments
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
			if ry.MaxReviewsPerRepoPerUTCDay != nil {
				r.MaxReviewsPerRepoPerUTCDay = *ry.MaxReviewsPerRepoPerUTCDay
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

func validKinds(slug string, kinds []string) error {
	seen := map[string]bool{}
	for _, k := range kinds {
		switch k {
		case KindReview, KindRun, KindScheduled:
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

func validEgress(slug, egress string) error {
	switch egress {
	case EgressNone, EgressTrusted, EgressCustom, EgressFull:
		return nil
	default:
		return fmt.Errorf("policy: %s invalid egress %q", slug, egress)
	}
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
	for _, k := range p.SessionKinds {
		if k == kind {
			return true
		}
	}
	return false
}
