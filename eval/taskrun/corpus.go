package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
)

// Corpus is a set of historical tasks run against one repository. Checks
// and the test patch are held back: the runner sends only Prompt and
// AllowedPaths to the plane.
type Corpus struct {
	Version int    `json:"version"`
	Project string `json:"project"`
	Repo    string `json:"repo"`
	Ref     string `json:"ref"`
	// Gate is the repository's own gate, run on each published head
	// without the test patch. A failure is a regression. Empty skips it.
	Gate  []string `json:"gate,omitempty"`
	Tasks []Task   `json:"tasks"`

	digest string
}

// Task is one historical change: the agent starts at BaseSHA and is
// verified when every check fails on the base and passes on the head,
// both with TestPatch applied.
type Task struct {
	ID           string     `json:"id"`
	BaseSHA      string     `json:"base_sha"`
	Prompt       string     `json:"prompt"`
	AllowedPaths []string   `json:"allowed_paths"`
	TestPatch    string     `json:"test_patch,omitempty"`
	Checks       [][]string `json:"checks"`
}

var (
	taskID  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// loadCorpus reads and validates a corpus file. Unknown fields fail, and
// the digest covers the exact bytes, so any change to a check, prompt, or
// patch makes earlier reports stale.
func loadCorpus(path string) (*Corpus, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c Corpus
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("corpus %s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("corpus %s: %w", path, err)
	}
	c.digest = digest(raw)
	return &c, nil
}

func (c *Corpus) validate() error {
	switch {
	case c.Version != 1:
		return fmt.Errorf("version %d, want 1", c.Version)
	case c.Project == "" || c.Repo == "" || c.Ref == "":
		return errors.New("project, repo, and ref are required")
	case len(c.Tasks) == 0:
		return errors.New("no tasks")
	}
	seen := map[string]bool{}
	for _, t := range c.Tasks {
		if !taskID.MatchString(t.ID) || seen[t.ID] {
			return fmt.Errorf("task id %q is invalid or repeated", t.ID)
		}
		seen[t.ID] = true
		if !fullSHA.MatchString(t.BaseSHA) {
			return fmt.Errorf("task %s: base_sha must be a full commit SHA", t.ID)
		}
		if t.Prompt == "" || len(t.AllowedPaths) == 0 || len(t.Checks) == 0 {
			return fmt.Errorf("task %s: prompt, allowed_paths, and checks are required", t.ID)
		}
		for _, argv := range t.Checks {
			if len(argv) == 0 || argv[0] == "" {
				return fmt.Errorf("task %s: empty check", t.ID)
			}
		}
	}
	return nil
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
