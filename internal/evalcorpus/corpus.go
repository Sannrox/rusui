// Package evalcorpus replays a versioned maintenance evaluation corpus
// through the dry-run review path and scores it against maintainer
// judgments and the promotion gates of ADR 0038.
package evalcorpus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/snapshot"
)

// FormatVersion is the corpus file format this package reads.
const FormatVersion = 1

// Splits. Development cases are public and listed per case in a report.
// Held-out cases are operator-owned and reported only in aggregate.
const (
	SplitDevelopment = "development"
	SplitHeldOut     = "held_out"
)

// Writes a case can produce or allow. None means no GitHub write: the
// result stays advisory.
const (
	WriteNone    = "none"
	WriteComment = "comment"
	WriteClose   = "close"
)

// Dispositions a maintainer records for a finding.
const (
	DispositionUseful  = "useful"
	DispositionNeutral = "neutral"
	DispositionHarmful = "harmful"
)

// Case tags name the situation a case covers.
const (
	TagUsefulFinding          = "useful_finding"
	TagIncorrectFinding       = "incorrect_finding"
	TagStaleEvent             = "stale_event"
	TagDuplicateEvent         = "duplicate_event"
	TagProtectedWork          = "protected_work"
	TagExistingImplementation = "existing_implementation"
	TagAdversarialInstruction = "adversarial_instruction"
	TagAmbiguousRequirement   = "ambiguous_requirement"
)

var knownTags = []string{
	TagUsefulFinding, TagIncorrectFinding, TagStaleEvent, TagDuplicateEvent,
	TagProtectedWork, TagExistingImplementation, TagAdversarialInstruction,
	TagAmbiguousRequirement,
}

// Corpus is one split: the configuration it replays under and its cases.
type Corpus struct {
	Version int       `json:"version"`
	Split   string    `json:"split"`
	Clock   time.Time `json:"clock"`
	Policy  string    `json:"policy"`
	Cases   []Case    `json:"cases"`
}

// Case is one recorded review result on one item with the maintainer's
// judgment of what should happen.
type Case struct {
	ID       string          `json:"id"`
	Tags     []string        `json:"tags"`
	Context  []snapshot.Item `json:"context,omitempty"`
	Item     snapshot.Item   `json:"item"`
	Edited   *snapshot.Item  `json:"edited,omitempty"`
	Result   engine.Artifact `json:"result"`
	Judgment Judgment        `json:"judgment"`
}

// Judgment is human-owned. Allowed bounds the writes the maintainer
// accepts; it always includes Write.
type Judgment struct {
	Write             string   `json:"write"`
	Allowed           []string `json:"allowed,omitempty"`
	Disposition       string   `json:"disposition,omitempty"`
	WrongFinding      bool     `json:"wrong_finding,omitempty"`
	CorrectionMinutes int      `json:"correction_minutes,omitempty"`
	Note              string   `json:"note,omitempty"`
}

// Load reads and validates one corpus file of the expected split.
// Unknown fields fail. Errors for a held-out file name only the file:
// decoder messages can echo its values.
func Load(path, split string) (*Corpus, error) {
	c, err := load(path, split)
	if err != nil && split == SplitHeldOut {
		return nil, fmt.Errorf("held-out corpus %s is invalid (details withheld)", path)
	}
	if err != nil {
		return nil, fmt.Errorf("corpus %s: %w", path, err)
	}
	return c, nil
}

func load(path, split string) (*Corpus, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var c Corpus
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	if c.Split != split {
		return nil, fmt.Errorf("split %q, want %q", c.Split, split)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks the corpus shape. It does not judge the cases.
func (c *Corpus) Validate() error {
	if c.Version != FormatVersion {
		return fmt.Errorf("version %d unsupported", c.Version)
	}
	if c.Split != SplitDevelopment && c.Split != SplitHeldOut {
		return fmt.Errorf("split %q unknown", c.Split)
	}
	if c.Clock.IsZero() {
		return fmt.Errorf("clock required")
	}
	if c.Policy == "" {
		return fmt.Errorf("policy required")
	}
	if len(c.Cases) == 0 {
		return fmt.Errorf("no cases")
	}
	ids := map[string]bool{}
	items := map[string]bool{}
	for i, k := range c.Cases {
		if err := validateCase(k, ids, items); err != nil {
			if c.Split == SplitHeldOut {
				// Held-out case ids, items, and values stay out of output.
				return fmt.Errorf("case %d: invalid (held-out details withheld)", i+1)
			}
			return fmt.Errorf("case %s: %w", k.ID, err)
		}
	}
	return nil
}

func validateCase(k Case, ids, items map[string]bool) error {
	if k.ID == "" || ids[k.ID] {
		return fmt.Errorf("id %q empty or repeated", k.ID)
	}
	ids[k.ID] = true
	key := fmt.Sprintf("%s#%d", k.Item.Repo, k.Item.Item)
	if k.Item.Repo == "" || k.Item.Item == 0 || items[key] {
		return fmt.Errorf("item %s empty or repeated", key)
	}
	items[key] = true
	if k.Edited != nil && (k.Edited.Repo != k.Item.Repo || k.Edited.Item != k.Item.Item) {
		return fmt.Errorf("edited item must be the same item")
	}
	for _, t := range k.Tags {
		if !slices.Contains(knownTags, t) {
			return fmt.Errorf("tag %q unknown", t)
		}
	}
	if slices.Contains(k.Tags, TagStaleEvent) != (k.Edited != nil) {
		return fmt.Errorf("stale_event needs exactly an edited item")
	}
	return k.Judgment.validate()
}

func (j Judgment) validate() error {
	writes := []string{WriteNone, WriteComment, WriteClose}
	if !slices.Contains(writes, j.Write) {
		return fmt.Errorf("judgment write %q unknown", j.Write)
	}
	for _, a := range j.Allowed {
		if !slices.Contains(writes, a) {
			return fmt.Errorf("allowed write %q unknown", a)
		}
	}
	switch j.Disposition {
	case "", DispositionUseful, DispositionNeutral, DispositionHarmful:
	default:
		return fmt.Errorf("disposition %q unknown", j.Disposition)
	}
	if j.WrongFinding && j.Disposition == "" {
		return fmt.Errorf("wrong_finding needs a disposition")
	}
	if j.CorrectionMinutes < 0 {
		return fmt.Errorf("correction_minutes negative")
	}
	return nil
}

// allows reports whether the maintainer accepts write w.
func (j Judgment) allows(w string) bool {
	return w == j.Write || slices.Contains(j.Allowed, w)
}

// Digest identifies the corpus content, judgments, and configuration.
// Any change produces a new digest, so earlier evidence does not apply.
func (c *Corpus) Digest() string {
	b, err := json.Marshal(c)
	if err != nil {
		panic(err) // Corpus holds only JSON-encodable fields.
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
