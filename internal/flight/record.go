// Package flight records each agent turn with the ontology snapshot and
// inputs it saw, so a replay can rebuild a Shikigami manifest and report
// divergence without repeating external effects.
package flight

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
)

const SchemaVersion = 1

const (
	KindObserve = "observe"
	KindEffect  = "effect"
)

var (
	ErrNoRead    = errors.New("flight: no read access")
	ErrNotFound  = errors.New("flight: object not in snapshot")
	ErrOverwrite = errors.New("flight: record already exists")
)

// Object is one ontology object the turn read.
type Object struct {
	ID       string   `json:"id"`
	Revision string   `json:"revision"`
	Readers  []string `json:"readers,omitempty"`
}

// Snapshot is the exact ontology read-set of a turn.
type Snapshot struct {
	Objects []Object `json:"objects"`
	Digest  string   `json:"digest"`
}

// Tool is one recorded tool call.
type Tool struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Kind   string         `json:"kind"`
	Input  map[string]any `json:"input,omitempty"`
	Result string         `json:"result,omitempty"`
}

// Model is credential-free model identity and settings.
type Model struct {
	Name     string `json:"name,omitempty"`
	Provider string `json:"provider,omitempty"`
	Settings string `json:"settings,omitempty"`
}

// Record is the append-only flight record for one turn.
type Record struct {
	SchemaVersion int      `json:"schema_version"`
	SessionID     int64    `json:"session_id"`
	TurnID        int64    `json:"turn_id"`
	ReceiptID     string   `json:"receipt_id"`
	Prompt        string   `json:"prompt"`
	Model         Model    `json:"model"`
	Tools         []Tool   `json:"tools"`
	Snapshot      Snapshot `json:"snapshot"`
	Context       []string `json:"context,omitempty"`
}

// View is a Record with objects the principal cannot read removed.
type View struct {
	Record
	Redacted []string `json:"redacted_objects,omitempty"`
}

// DigestSnapshot is the SHA-256 of sorted object id and revision pairs.
func DigestSnapshot(objects []Object) string {
	sorted := append([]Object(nil), objects...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	h := sha256.New()
	for _, o := range sorted {
		_, _ = h.Write([]byte(o.ID))
		_, _ = h.Write([]byte{'\n'})
		_, _ = h.Write([]byte(o.Revision))
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Normalize fills schema version and snapshot digest.
func Normalize(rec Record) Record {
	if rec.SchemaVersion == 0 {
		rec.SchemaVersion = SchemaVersion
	}
	rec.Snapshot.Digest = DigestSnapshot(rec.Snapshot.Objects)
	for i, t := range rec.Tools {
		if t.Kind == "" {
			rec.Tools[i].Kind = Classify(t.Name)
		}
	}
	return rec
}

// Classify treats unknown tools as effects so replay fails closed.
func Classify(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read_file", "read", "glob", "grep", "search":
		return KindObserve
	default:
		return KindEffect
	}
}

func canRead(o Object, principal string) bool {
	if len(o.Readers) == 0 {
		return true
	}
	return slices.Contains(o.Readers, principal)
}

// Open returns the parts of rec the principal may read.
func Open(rec Record, principal string) View {
	rec = Normalize(rec)
	v := View{Record: rec, Redacted: nil}
	kept := make([]Object, 0, len(rec.Snapshot.Objects))
	for _, o := range rec.Snapshot.Objects {
		if canRead(o, principal) {
			kept = append(kept, o)
			continue
		}
		v.Redacted = append(v.Redacted, o.ID)
	}
	v.Snapshot.Objects = kept
	v.Snapshot.Digest = DigestSnapshot(kept)
	return v
}

// Object returns one snapshot object the principal may read.
func (v View) Object(id string) (Object, error) {
	if slices.Contains(v.Redacted, id) {
		return Object{}, ErrNoRead
	}
	for _, o := range v.Snapshot.Objects {
		if o.ID == id {
			return o, nil
		}
	}
	return Object{}, ErrNotFound
}

func digestBytes(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func textDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
