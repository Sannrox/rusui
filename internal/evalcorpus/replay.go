package evalcorpus

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

// CaseResult is what the plane did with one recorded result.
type CaseResult struct {
	ID            string   `json:"id"`
	Tags          []string `json:"tags"`
	Judgment      string   `json:"judgment"`
	System        string   `json:"system"`
	Evidence      string   `json:"evidence,omitempty"`
	Writes        int      `json:"writes"`
	DuplicateJobs int      `json:"duplicate_jobs"`
	Agree         bool     `json:"agree"`
	FalseAction   bool     `json:"false_action"`
	WrongAction   bool     `json:"wrong_action"`
}

// Run replays each split and evaluates the gates. At most one split of
// each kind is accepted.
func Run(revision string, corpora ...*Corpus) (Report, error) {
	r := Report{Format: FormatVersion, Revision: revision, ReplayVersion: ReplayVersion}
	seen := map[string]bool{}
	for _, c := range corpora {
		if seen[c.Split] {
			return Report{}, fmt.Errorf("two %s splits", c.Split)
		}
		seen[c.Split] = true
		results, err := Replay(c)
		if err != nil {
			return Report{}, fmt.Errorf("%s split: %w", c.Split, err)
		}
		r.Splits = append(r.Splits, Score(c, results))
	}
	r.Gates = Gates(r.Splits)
	return r, nil
}

// Replay runs every case through admit, claim, complete, and dry-run
// apply in a throwaway store with a fake GitHub. No live GitHub call is
// made and no model runs: the recorded result stands in for the guest.
func Replay(c *Corpus) ([]CaseResult, error) {
	pol, err := policy.Parse([]byte(c.Policy))
	if err != nil && c.Split == SplitHeldOut {
		// Policy errors name repositories.
		return nil, fmt.Errorf("corpus policy invalid (held-out details withheld)")
	}
	if err != nil {
		return nil, fmt.Errorf("corpus policy: %w", err)
	}
	dir, err := os.MkdirTemp("", "rusui-eval-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	out := make([]CaseResult, 0, len(c.Cases))
	for i, k := range c.Cases {
		r, err := replayIsolated(filepath.Join(dir, fmt.Sprintf("case-%d.db", i)), pol, c, k)
		if err != nil {
			if c.Split == SplitHeldOut {
				// Engine errors can name the repository or item.
				return nil, fmt.Errorf("case %d: replay failed (held-out details withheld)", i+1)
			}
			return nil, fmt.Errorf("case %s: %w", k.ID, err)
		}
		out = append(out, r)
	}
	return out, nil
}

// replayIsolated gives each case its own store, engine, and fake GitHub,
// so no case sees another's items, jobs, or budget.
func replayIsolated(dbPath string, pol *policy.Effective, c *Corpus, k Case) (CaseResult, error) {
	st, err := store.Open(dbPath)
	if err != nil {
		return CaseResult{}, err
	}
	defer func() { _ = st.Close() }()
	f := gh.NewFake()
	e := engine.New(st, pol, f, &clock.Fake{T: c.Clock})
	// Engine messages name repositories and items; the report is the
	// output, and held-out details must not reach stderr.
	e.Log = log.New(io.Discard, "", 0)
	e.Notify = func(string) {}
	e.ReloadPolicy(pol)
	return replayCase(e, f, st, k)
}

func replayCase(e *engine.Engine, f *gh.Fake, st *store.Store, k Case) (CaseResult, error) {
	for _, it := range k.Context {
		f.Put(it)
	}
	f.Put(k.Item)
	cl, err := admitAndClaim(e, k)
	if err != nil {
		return CaseResult{}, err
	}
	if cl == nil {
		return CaseResult{}, fmt.Errorf("item not admitted")
	}
	r := CaseResult{ID: k.ID, Tags: k.Tags, Judgment: k.Judgment.Write}
	if k.Edited != nil {
		// The source changes while the review runs: the result completes
		// against a revision that is no longer pending.
		f.Put(*k.Edited)
		if err := refresh(e, k); err != nil {
			return CaseResult{}, err
		}
	}
	// A stale result completes; apply cancels its writes. Any error here
	// is a broken case, not a refusal.
	if _, err := e.Complete(cl.Job.ID, cl.Job.LeaseGeneration, cl.Job.ClaimedRevision, bind(k.Result, cl)); err != nil {
		return CaseResult{}, err
	}
	if slices.Contains(k.Tags, TagDuplicateEvent) {
		// The same unchanged event arrives again.
		again, err := admitAndClaim(e, k)
		if err != nil {
			return CaseResult{}, err
		}
		if again != nil {
			r.DuplicateJobs++
			if _, err := e.Complete(again.Job.ID, again.Job.LeaseGeneration, again.Job.ClaimedRevision, bind(k.Result, again)); err != nil {
				return CaseResult{}, err
			}
		}
	}
	actions, err := store.ListActions(st, k.Item.Repo, k.Item.Item)
	if err != nil {
		return CaseResult{}, err
	}
	r.Writes = len(actions)
	r.System, r.Evidence = systemWrite(actions)
	r.Agree = r.System == k.Judgment.Write
	r.FalseAction = r.System != WriteNone && !k.Judgment.allows(r.System)
	// ADR 0038 D6: a wrong action is a write on protected or stale work,
	// not merely an unwanted recommendation.
	r.WrongAction = r.FalseAction && (slices.Contains(k.Tags, TagProtectedWork) || slices.Contains(k.Tags, TagStaleEvent))
	return r, nil
}

func refresh(e *engine.Engine, k Case) error {
	if err := e.CatchUpItem(k.Item.Repo, k.Item.Item, k.Item.ItemKind); err != nil {
		return err
	}
	_, err := e.StepRefresh()
	return err
}

func admitAndClaim(e *engine.Engine, k Case) (*engine.Claim, error) {
	if err := refresh(e, k); err != nil {
		return nil, err
	}
	cl, err := e.Claim(k.Item.Repo)
	if err != nil || cl == nil {
		return cl, err
	}
	return cl, sameItem(cl, k)
}

// sameItem keeps one case's result from completing another case's job.
func sameItem(cl *engine.Claim, k Case) error {
	if cl.Job.Item != k.Item.Item {
		return fmt.Errorf("claimed item %d, want %d", cl.Job.Item, k.Item.Item)
	}
	return nil
}

// bind fills the fields a guest copies from its claim.
func bind(a engine.Artifact, cl *engine.Claim) engine.Artifact {
	if a.SchemaVersion == 0 {
		a.SchemaVersion = 1
	}
	a.Repo = cl.Job.Repo
	a.Item = cl.Job.Item
	a.ItemKind = cl.Job.ItemKind
	a.ClaimedRevision = cl.Job.ClaimedRevision
	a.SnapshotHash = cl.ItemHash
	a.ProposedActions = slices.Clone(a.ProposedActions)
	return a
}

// systemWrite names the most consequential dry-run write and its
// evidence class.
func systemWrite(actions []store.Action) (string, string) {
	write, evidence := WriteNone, ""
	for _, a := range actions {
		switch a.Type {
		case WriteClose:
			return WriteClose, a.EvidenceClass
		case WriteComment:
			write, evidence = WriteComment, a.EvidenceClass
		}
	}
	return write, evidence
}
