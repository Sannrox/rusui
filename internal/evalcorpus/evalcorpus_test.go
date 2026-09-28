package evalcorpus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/snapshot"
)

const devPath = "../../eval/corpus/development.json"

const heldOutPolicy = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
projects:
  held:
    repos:
      example/held:
        visibility: public
        review: true
        comments: true
`

// heldOut builds n comment cases. The first harmful cases carry a wrong
// comment the maintainer did not want; the rest are useful comments.
func heldOut(n, harmful int) *Corpus {
	c := &Corpus{Version: FormatVersion, Split: SplitHeldOut,
		Clock: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC), Policy: heldOutPolicy}
	for i := range n {
		k := Case{
			ID: fmt.Sprintf("H%d", i+1),
			Item: snapshot.Item{Repo: "example/held", Item: 1000 + i, ItemKind: "issue", State: "open",
				Title: fmt.Sprintf("private title %d", i), Body: "private body", DefaultBranch: "main", MainSHA: "aaa",
				CreatedAt: "2026-09-01T00:00:00Z", UpdatedAt: "2026-09-01T00:00:00Z"},
			Result: engine.Artifact{Verdict: "propose_comment", Confidence: "high",
				ProposedActions: []engine.ProposedAction{{Type: "comment", ReasonCode: "note"}}},
			Judgment: Judgment{Write: WriteComment, Disposition: DispositionUseful, Note: "held-out note"},
		}
		if i < harmful {
			k.Tags = []string{TagIncorrectFinding}
			k.Judgment = Judgment{Write: WriteNone, Disposition: DispositionHarmful, WrongFinding: true, Note: "held-out note"}
		}
		c.Cases = append(c.Cases, k)
	}
	if err := c.Validate(); err != nil {
		panic(err)
	}
	return c
}

func gate(t *testing.T, r Report, class string) Gate {
	t.Helper()
	for _, g := range r.Gates {
		if g.Class == class {
			return g
		}
	}
	t.Fatalf("no %s gate", class)
	return Gate{}
}

func requirement(t *testing.T, g Gate, name string) Requirement {
	t.Helper()
	for _, q := range g.Requirements {
		if q.Name == name {
			return q
		}
	}
	t.Fatalf("%s gate has no %q", g.Class, name)
	return Requirement{}
}

func TestKnownRegressionFailsCommentGate(t *testing.T) {
	dev, err := Load(devPath, SplitDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	base, err := Run("rev", dev, heldOut(30, 0))
	if err != nil {
		t.Fatal(err)
	}
	g := gate(t, base, "comment")
	for _, name := range []string{"held-out cases ≥ 30", "useful ≥ 2× harmful", "false recommendations ≤ 10%", "0 duplicate comments", "0 wrong comment actions"} {
		if q := requirement(t, g, name); q.Status != GatePass {
			t.Fatalf("baseline %q = %s (%s)", name, q.Status, q.Detail)
		}
	}
	if g.Status != GateIncomplete {
		t.Fatalf("baseline comment gate %s; live shadow and leak checks are not measured, so it cannot pass", g.Status)
	}

	regressed, err := Run("rev", dev, heldOut(30, 12))
	if err != nil {
		t.Fatal(err)
	}
	g = gate(t, regressed, "comment")
	if g.Status != GateFail {
		t.Fatalf("regressed comment gate %s, want fail", g.Status)
	}
	for _, name := range []string{"useful ≥ 2× harmful", "false recommendations ≤ 10%"} {
		if q := requirement(t, g, name); q.Status != GateFail {
			t.Fatalf("regressed %q = %s (%s)", name, q.Status, q.Detail)
		}
	}
	// Unwanted comments are false recommendations, not wrong actions: no
	// case is protected or stale.
	if q := requirement(t, g, "0 wrong comment actions"); q.Status != GatePass {
		t.Fatalf("regressed wrong comment actions = %s (%s)", q.Status, q.Detail)
	}
	m := regressed.Splits[1].Metrics
	if m.Cases != 30 || m.FalseActions != 12 || m.WrongActions != 0 || m.Harmful != 12 || m.Useful != 18 {
		t.Fatalf("regressed metrics %+v", m)
	}
}

func TestWriteOnProtectedWorkIsWrongAction(t *testing.T) {
	dev, err := Load(devPath, SplitDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Run("rev", dev)
	if err != nil {
		t.Fatal(err)
	}
	wrong := map[string]bool{}
	for _, c := range r.Splits[0].Cases {
		if c.WrongAction {
			wrong[c.ID] = true
		}
	}
	// E12 closes held work; E4 and E14 are unwanted but eligible writes.
	if len(wrong) != 1 || !wrong["E12"] {
		t.Fatalf("wrong actions %v, want only E12", wrong)
	}
	if m := r.Splits[0].Metrics; m.FalseActions != 3 || m.WrongActions != 1 {
		t.Fatalf("development metrics %+v", m)
	}
}

func TestChangedCorpusDoesNotInheritEvidence(t *testing.T) {
	load := func() *Corpus {
		c, err := Load(devPath, SplitDevelopment)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	run := func(c *Corpus) Report {
		r, err := Run("rev", c)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	prior := run(load())
	if err := SameEvidence(prior, run(load())); err != nil {
		t.Fatalf("same inputs: %v", err)
	}
	judged := load()
	judged.Cases[0].Judgment.Disposition = DispositionHarmful
	configured := load()
	configured.Policy = strings.Replace(configured.Policy, "max_reviews_per_repo_per_utc_day: 50", "max_reviews_per_repo_per_utc_day: 49", 1)
	for name, c := range map[string]*Corpus{"judgment": judged, "configuration": configured} {
		if err := SameEvidence(prior, run(c)); err == nil {
			t.Fatalf("changed %s inherited prior evidence", name)
		}
	}
	later, err := Run("rev2", load())
	if err != nil {
		t.Fatal(err)
	}
	if err := SameEvidence(prior, later); err == nil {
		t.Fatal("a new revision inherited prior evidence")
	}
	for _, rev := range []string{"rev-dirty", "rev-unverified", "unknown", "unknown-dirty", ""} {
		r, err := Run(rev, load())
		if err != nil {
			t.Fatal(err)
		}
		if err := SameEvidence(r, r); err == nil {
			t.Fatalf("revision %q matched itself", rev)
		}
	}
}

func TestHeldOutReportedInAggregateOnly(t *testing.T) {
	dev, err := Load(devPath, SplitDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Run("rev", dev, heldOut(3, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Splits[1].Cases) != 0 {
		t.Fatalf("held-out cases in report: %v", r.Splits[1].Cases)
	}
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{string(body), Markdown(r)} {
		for _, secret := range []string{"H1", "private title", "private body", "held-out note", "example/held"} {
			if strings.Contains(out, secret) {
				t.Fatalf("report leaks held-out %q", secret)
			}
		}
	}
	if r.Splits[1].Metrics.Cases != 3 || r.Splits[1].Metrics.Harmful != 1 {
		t.Fatalf("held-out aggregate %+v", r.Splits[1].Metrics)
	}
}

func TestHeldOutErrorsWithholdDetails(t *testing.T) {
	repeated := heldOut(2, 0)
	repeated.Cases[1].ID = repeated.Cases[0].ID
	unbound := heldOut(1, 0)
	unbound.Cases[0].Item.Repo = "example/unbound-private"
	badPolicy := heldOut(1, 0)
	badPolicy.Policy = strings.Replace(badPolicy.Policy, "visibility: public", "visibility: private-secret", 1)
	badFile := filepath.Join(t.TempDir(), "held.json")
	if err := os.WriteFile(badFile, []byte(`{"version":1,"split":"held_out","clock":"private-clock-value"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func() error{
		"validate": repeated.Validate,
		"replay":   func() error { _, err := Replay(unbound); return err },
		"policy":   func() error { _, err := Replay(badPolicy); return err },
		"decode":   func() error { _, err := Load(badFile, SplitHeldOut); return err },
	} {
		err := run()
		if err == nil {
			t.Fatalf("%s: no error", name)
		}
		for _, secret := range []string{"H1", "example/", "private-", "unbound-private"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("%s error leaks %q: %v", name, secret, err)
			}
		}
	}
}

func TestLoadRejectsMalformedCorpus(t *testing.T) {
	good, err := os.ReadFile(devPath)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(map[string]any){
		"unknown field": func(m map[string]any) { m["extra"] = true },
		"repeated id": func(m map[string]any) {
			cs := m["cases"].([]any)
			cs[1].(map[string]any)["id"] = cs[0].(map[string]any)["id"]
		},
		"stale without edit": func(m map[string]any) {
			for _, c := range m["cases"].([]any) {
				delete(c.(map[string]any), "edited")
			}
		},
		"unknown write": func(m map[string]any) {
			m["cases"].([]any)[0].(map[string]any)["judgment"].(map[string]any)["write"] = "merge"
		},
		"unknown split": func(m map[string]any) { m["split"] = "training" },
	}
	for name, mutate := range cases {
		var m map[string]any
		if err := json.Unmarshal(good, &m); err != nil {
			t.Fatal(err)
		}
		mutate(m)
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "c.json")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path, SplitDevelopment); err == nil {
			t.Fatalf("%s: loaded", name)
		}
	}
}

func TestAdvisoryGateReadsHeldOutTrailingWindow(t *testing.T) {
	for _, tc := range []struct {
		n, harmful int
		want       string
	}{{19, 0, GateIncomplete}, {30, 0, GatePass}, {30, 11, GatePass}} {
		r, err := Run("rev", heldOut(tc.n, tc.harmful))
		if err != nil {
			t.Fatal(err)
		}
		if g := gate(t, r, "advisory"); g.Status != tc.want {
			t.Fatalf("held-out %d cases, %d harmful: advisory %s, want %s", tc.n, tc.harmful, g.Status, tc.want)
		}
	}
	// Harmful findings in the trailing window fail the rule.
	late := heldOut(30, 0)
	for i := 10; i < 30; i++ {
		late.Cases[i].Judgment = Judgment{Write: WriteNone, Disposition: DispositionHarmful, WrongFinding: true}
	}
	r, err := Run("rev", late)
	if err != nil {
		t.Fatal(err)
	}
	if g := gate(t, r, "advisory"); g.Status != GateFail {
		t.Fatalf("20 trailing harmful: advisory %s, want fail", g.Status)
	}
}
