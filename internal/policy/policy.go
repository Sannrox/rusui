package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type File struct {
	Version  int                 `yaml:"version"`
	Defaults Repo                `yaml:"defaults"`
	Repos    map[string]RepoYAML `yaml:"repos"`
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

type Repo struct {
	Visibility                 string `yaml:"visibility"`
	Review                     bool   `yaml:"review"`
	Comments                   bool   `yaml:"comments"`
	Close                      bool   `yaml:"close"`
	Implement                  bool   `yaml:"implement"`
	Land                       bool   `yaml:"land"`
	MaxReviewsPerRepoPerUTCDay int    `yaml:"max_reviews_per_repo_per_utc_day"`
	NeverRelease               bool   `yaml:"never_release"`
	NeverLeakPrivateToPublic   bool   `yaml:"never_leak_private_to_public"`
}

type Effective struct {
	Hash  string
	Raw   []byte
	Repos map[string]Repo
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
	if f.Version != 1 {
		return nil, fmt.Errorf("policy: unsupported version %d", f.Version)
	}
	if f.Repos == nil {
		return nil, fmt.Errorf("policy: repos required")
	}
	out := &Effective{Raw: append([]byte(nil), raw...), Repos: map[string]Repo{}}
	for name, y := range f.Repos {
		r := Repo{
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
		if y.Visibility != nil {
			r.Visibility = *y.Visibility
		}
		if y.Review != nil {
			r.Review = *y.Review
		}
		if y.Comments != nil {
			r.Comments = *y.Comments
		}
		if y.Close != nil {
			r.Close = *y.Close
		}
		if y.Implement != nil {
			r.Implement = *y.Implement
		}
		if y.Land != nil {
			r.Land = *y.Land
		}
		if y.MaxReviewsPerRepoPerUTCDay != nil {
			r.MaxReviewsPerRepoPerUTCDay = *y.MaxReviewsPerRepoPerUTCDay
		}
		if r.Visibility != "public" && r.Visibility != "private" {
			return nil, fmt.Errorf("policy: %s invalid visibility", name)
		}
		out.Repos[name] = r
	}
	sum := sha256.Sum256(raw)
	out.Hash = hex.EncodeToString(sum[:])
	return out, nil
}

func (e *Effective) Repo(name string) (Repo, bool) {
	r, ok := e.Repos[name]
	return r, ok
}
