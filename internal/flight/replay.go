package flight

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// EffectSink is notified of denied effects. Replay never calls Apply.
type EffectSink interface {
	Apply(name string, input map[string]any) error
	Denied(name string)
}

// DenyEffects is a sink that records denials and panics on Apply.
type DenyEffects struct {
	DeniedNames []string
}

func (d *DenyEffects) Apply(name string, _ map[string]any) error {
	return fmt.Errorf("flight: replay applied effect %q", name)
}

func (d *DenyEffects) Denied(name string) {
	d.DeniedNames = append(d.DeniedNames, name)
}

// Bindings is the schema-v1 Shikigami replay binding set.
type Bindings struct {
	TaskDigest             string `json:"task_digest"`
	InputsDigest           string `json:"inputs_digest"`
	PromptDigest           string `json:"prompt_digest"`
	ModelDigest            string `json:"model_digest"`
	ToolCatalogDigest      string `json:"tool_catalog_digest"`
	PolicyEvidenceDigest   string `json:"policy_evidence_digest"`
	RetainedEvidenceDigest string `json:"retained_evidence_digest"`
}

// StepEvidence is one expected replay step.
type StepEvidence struct {
	Kind          string `json:"kind"`
	Name          string `json:"name,omitempty"`
	ContentDigest string `json:"content_digest"`
}

// TerminalEvidence is the expected terminal comparison.
type TerminalEvidence struct {
	Success       bool   `json:"success"`
	Termination   string `json:"termination"`
	SummaryDigest string `json:"summary_digest"`
}

// Evidence is a schema-v1 Shikigami ReplayEvidenceBundle.
type Evidence struct {
	SchemaVersion    int              `json:"schema_version"`
	SourceRunID      string           `json:"source_run_id"`
	Task             string           `json:"task"`
	Bindings         Bindings         `json:"bindings"`
	ExpectedSteps    []StepEvidence   `json:"expected_steps"`
	ExpectedTerminal TerminalEvidence `json:"expected_terminal"`
}

// Manifest is a schema-v1 Shikigami ReplayManifest.
type Manifest struct {
	SchemaVersion  int      `json:"schema_version"`
	EvidenceDigest string   `json:"evidence_digest"`
	Bindings       Bindings `json:"bindings"`
}

// ToolInput is the input replay would send for one recorded tool.
type ToolInput struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input,omitempty"`
}

// Report is the outcome of replaying a turn against a live snapshot.
type Report struct {
	Manifest     Manifest    `json:"manifest"`
	Evidence     Evidence    `json:"evidence"`
	ToolInputs   []ToolInput `json:"tool_inputs"`
	Denied       []string    `json:"denied_effects"`
	Divergence   string      `json:"divergence,omitempty"`
	ObjectID     string      `json:"object_id,omitempty"`
	WantRevision string      `json:"want_revision,omitempty"`
	GotRevision  string      `json:"got_revision,omitempty"`
}

// EvidenceFrom builds a Shikigami evidence bundle from a flight record.
func EvidenceFrom(rec Record) Evidence {
	rec = Normalize(rec)
	steps := make([]StepEvidence, 0, len(rec.Tools))
	names := make([]string, 0, len(rec.Tools))
	for _, t := range rec.Tools {
		names = append(names, t.Name)
		steps = append(steps, StepEvidence{
			Kind:          "tool",
			Name:          t.Name,
			ContentDigest: digestBytes(t.Input),
		})
	}
	b := Bindings{
		TaskDigest:             textDigest(rec.Prompt),
		InputsDigest:           rec.Snapshot.Digest,
		PromptDigest:           textDigest(rec.Prompt),
		ModelDigest:            digestBytes(rec.Model),
		ToolCatalogDigest:      digestBytes(names),
		PolicyEvidenceDigest:   textDigest("observation-only"),
		RetainedEvidenceDigest: rec.Snapshot.Digest,
	}
	return Evidence{
		SchemaVersion:    SchemaVersion,
		SourceRunID:      "turn-" + strconv.FormatInt(rec.TurnID, 10),
		Task:             rec.Prompt,
		Bindings:         b,
		ExpectedSteps:    steps,
		ExpectedTerminal: TerminalEvidence{Success: true, Termination: "report", SummaryDigest: textDigest("ok")},
	}
}

// ManifestFrom binds a schema-v1 Shikigami replay manifest to the evidence.
func ManifestFrom(rec Record) Manifest {
	ev := EvidenceFrom(rec)
	return Manifest{
		SchemaVersion:  SchemaVersion,
		EvidenceDigest: digestBytes(ev),
		Bindings:       ev.Bindings,
	}
}

// Replay re-runs rec against live. It reports object-revision divergence
// and identical tool inputs, and never applies external effects.
func Replay(rec Record, live Snapshot, effects EffectSink) Report {
	rec = Normalize(rec)
	if effects == nil {
		effects = &DenyEffects{}
	}
	ev := EvidenceFrom(rec)
	rep := Report{
		Manifest: ManifestFrom(rec),
		Evidence: ev,
	}
	live.Digest = DigestSnapshot(live.Objects)
	byID := make(map[string]Object, len(live.Objects))
	for _, o := range live.Objects {
		byID[o.ID] = o
	}
	for _, want := range rec.Snapshot.Objects {
		got, ok := byID[want.ID]
		if !ok || got.Revision != want.Revision {
			rep.Divergence = "object_revision"
			rep.ObjectID = want.ID
			rep.WantRevision = want.Revision
			if ok {
				rep.GotRevision = got.Revision
			}
			break
		}
	}
	for _, t := range rec.Tools {
		rep.ToolInputs = append(rep.ToolInputs, ToolInput{ID: t.ID, Name: t.Name, Input: t.Input})
		if t.Kind == KindEffect {
			rep.Denied = append(rep.Denied, t.Name)
			effects.Denied(t.Name)
		}
	}
	return rep
}

// EqualInputs reports whether two tool-input lists match by JSON.
func EqualInputs(a, b []ToolInput) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Name != b[i].Name {
			return false
		}
		left, err := json.Marshal(a[i].Input)
		if err != nil {
			return false
		}
		right, err := json.Marshal(b[i].Input)
		if err != nil {
			return false
		}
		if string(left) != string(right) {
			return false
		}
	}
	return true
}
