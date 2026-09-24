package engine

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

var (
	ErrTaskBlocked = fmt.Errorf("task blocked")
	ErrStaleSource = fmt.Errorf("stale source")
	ErrOutOfScope  = fmt.Errorf("out of scope")
)

// TaskSpec is the immutable pin for an implementation effort (ADR 0013 source+task).
type TaskSpec struct {
	EffortKey        string
	Prompt           string
	Repo             string
	Ref              string
	BaseSHA          string
	AllowedPaths     []string
	ContextRefs      []string
	InstructionPaths []string // repository-offered extra paths; cannot expand scope
	BudgetRepairs    int
}

func (e *Engine) StartTask(project string, spec TaskSpec) (*store.Task, error) {
	pol := e.PolicySnapshot()
	p, ok := pol.Project(project)
	if !ok || !p.AllowsKind(policy.KindRun) {
		return nil, fmt.Errorf("policy")
	}
	if spec.EffortKey == "" || spec.Prompt == "" || spec.Repo == "" || spec.Ref == "" || spec.BaseSHA == "" {
		return nil, fmt.Errorf("%w: missing pin", ErrTaskBlocked)
	}
	if !slices.Contains(p.Repos, spec.Repo) {
		return nil, fmt.Errorf("%w: repo not bound", ErrTaskBlocked)
	}
	if len(spec.AllowedPaths) == 0 {
		return nil, fmt.Errorf("%w: allowed paths required", ErrTaskBlocked)
	}
	for _, extra := range spec.InstructionPaths {
		if !pathAllowed(extra, spec.AllowedPaths) {
			return nil, fmt.Errorf("%w: %v", ErrOutOfScope, extra)
		}
	}
	if spec.BudgetRepairs == 0 {
		spec.BudgetRepairs = 3
	}
	if d, ok := e.GitHub.(interface {
		DefaultSHA(string) (string, error)
	}); ok {
		if live, err := d.DefaultSHA(spec.Repo); err == nil && live != "" && live != spec.BaseSHA {
			return nil, fmt.Errorf("%w", ErrStaleSource)
		}
	}
	paths, err := json.Marshal(spec.AllowedPaths)
	if err != nil {
		return nil, err
	}
	ctxs, err := json.Marshal(spec.ContextRefs)
	if err != nil {
		return nil, err
	}
	if spec.ContextRefs == nil {
		ctxs = []byte("[]")
	}
	hash := specHash(spec, pol.Hash)
	var out *store.Task
	err = e.Store.Tx(func(tx *sql.Tx) error {
		paused, err := store.Paused(tx, project)
		if err != nil {
			return err
		}
		if paused {
			return errPaused
		}
		if existing, ok, err := store.LookupTaskBySpecHashTx(tx, hash); err != nil {
			return err
		} else if ok {
			out = existing
			return nil
		}
		latest, ok, err := store.LatestTaskTx(tx, spec.EffortKey)
		if err != nil {
			return err
		}
		rev := 1
		if ok {
			if err := store.SupersedeTaskTx(tx, latest.ID); err != nil {
				return err
			}
			rev = latest.Revision + 1
		}
		sid, item, err := store.InsertRunSessionTx(tx, project, spec.Repo, spec.Prompt)
		if err != nil {
			return err
		}
		it := snapshot.Item{
			Repo: spec.Repo, Item: item, ItemKind: "run", State: "open",
			Title: "run", Body: spec.Prompt, DefaultBranch: spec.Ref, MainSHA: spec.BaseSHA,
		}
		if err := store.SaveSnapshotTx(tx, spec.Repo, item, 1, it); err != nil {
			return err
		}
		id, err := store.InsertTaskTx(tx, store.Task{
			EffortKey: spec.EffortKey, Revision: rev, SpecHash: hash,
			SessionID: sid, Repo: spec.Repo, Ref: spec.Ref, BaseSHA: spec.BaseSHA,
			PolicyHash: pol.Hash, AllowedPaths: string(paths), ContextRefs: string(ctxs),
			Prompt: spec.Prompt, BudgetRepairs: spec.BudgetRepairs, State: "open",
		})
		if err != nil {
			return err
		}
		out = &store.Task{
			ID: id, EffortKey: spec.EffortKey, Revision: rev, SpecHash: hash,
			SessionID: sid, Repo: spec.Repo, Ref: spec.Ref, BaseSHA: spec.BaseSHA,
			PolicyHash: pol.Hash, AllowedPaths: string(paths), ContextRefs: string(ctxs),
			Prompt: spec.Prompt, BudgetRepairs: spec.BudgetRepairs, State: "open",
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func pathAllowed(p string, allowed []string) bool {
	p = path.Clean("/" + strings.TrimPrefix(p, "/"))
	for _, a := range allowed {
		a = path.Clean("/" + strings.TrimPrefix(a, "/"))
		if p == a || strings.HasPrefix(p, a+"/") {
			return true
		}
	}
	return false
}

func specHash(spec TaskSpec, policyHash string) string {
	h := sha256.New()
	h.Write([]byte(spec.EffortKey))
	h.Write([]byte{0})
	h.Write([]byte(spec.Repo))
	h.Write([]byte{0})
	h.Write([]byte(spec.Ref))
	h.Write([]byte{0})
	h.Write([]byte(spec.BaseSHA))
	h.Write([]byte{0})
	h.Write([]byte(spec.Prompt))
	h.Write([]byte{0})
	h.Write([]byte(policyHash))
	h.Write([]byte{0})
	b, _ := json.Marshal(spec.AllowedPaths)
	h.Write(b)
	h.Write([]byte{0})
	c, _ := json.Marshal(spec.ContextRefs)
	h.Write(c)
	return hex.EncodeToString(h.Sum(nil))
}
