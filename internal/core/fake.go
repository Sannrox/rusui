package core

import (
	"fmt"
	"sync"
)

// Fake is an in-memory Client for tests. It never publishes or merges.
type Fake struct {
	mu         sync.Mutex
	Denied     error
	FailLint   int
	Creates    []CreateBranchRequest
	Edits      []ApplyEditRequest
	Lints      []lintCall
	Proposals  []CreateProposalRequest
	Merges     int
	Approves   int
	branches   map[string]DefinitionBranch
	createKeys map[string]DefinitionBranch
	editKeys   map[string]editReplay
}

type editReplay struct {
	expected string
	result   ApplyEditResult
}

type lintCall struct {
	Namespace string
	BranchID  string
}

func branchKey(namespace, branchID string) string {
	return namespace + "/" + branchID
}

func (f *Fake) CreateDefinitionBranch(req CreateBranchRequest) (DefinitionBranch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Denied != nil {
		return DefinitionBranch{}, f.Denied
	}
	if f.createKeys == nil {
		f.createKeys = map[string]DefinitionBranch{}
	}
	if f.branches == nil {
		f.branches = map[string]DefinitionBranch{}
	}
	if replayed, ok := f.createKeys[req.IdempotencyKey]; ok {
		return replayed, nil
	}
	key := branchKey(req.Namespace, req.BranchID)
	if _, ok := f.branches[key]; ok {
		return DefinitionBranch{}, AlreadyExistsError{Message: "definition write conflicts with durable state"}
	}
	head := req.ParentRevisionDigest
	if head == "" {
		head = "sha256:parent"
	}
	branch := DefinitionBranch{Namespace: req.Namespace, BranchID: req.BranchID, HeadRevisionDigest: head, BaseRevisionDigest: head}
	f.Creates = append(f.Creates, req)
	f.branches[key] = branch
	f.createKeys[req.IdempotencyKey] = branch
	return branch, nil
}

func (f *Fake) GetDefinitionBranch(namespace, branchID string) (DefinitionBranch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Denied != nil {
		return DefinitionBranch{}, f.Denied
	}
	if branch, ok := f.branches[branchKey(namespace, branchID)]; ok {
		return branch, nil
	}
	return DefinitionBranch{}, NotFoundError{Message: "definition branch unavailable"}
}

func (f *Fake) ApplyDefinitionBranchEdit(req ApplyEditRequest) (ApplyEditResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Denied != nil {
		return ApplyEditResult{}, f.Denied
	}
	if f.branches == nil {
		f.branches = map[string]DefinitionBranch{}
	}
	if f.editKeys == nil {
		f.editKeys = map[string]editReplay{}
	}
	if replayed, ok := f.editKeys[req.IdempotencyKey]; ok {
		if replayed.expected != req.ExpectedHeadDigest {
			return ApplyEditResult{}, AlreadyExistsError{Message: "definition write conflicts with durable state"}
		}
		return replayed.result, nil
	}
	key := branchKey(req.Namespace, req.BranchID)
	prev := f.branches[key]
	if prev.HeadRevisionDigest == "" {
		prev.HeadRevisionDigest = req.ExpectedHeadDigest
		prev.Namespace = req.Namespace
		prev.BranchID = req.BranchID
	}
	head := "sha256:draft-head"
	branch := DefinitionBranch{Namespace: req.Namespace, BranchID: req.BranchID, HeadRevisionDigest: head, BaseRevisionDigest: prev.HeadRevisionDigest}
	result := ApplyEditResult{Branch: branch, PreviousHeadDigest: prev.HeadRevisionDigest}
	f.Edits = append(f.Edits, req)
	f.branches[key] = branch
	f.editKeys[req.IdempotencyKey] = editReplay{expected: req.ExpectedHeadDigest, result: result}
	return result, nil
}

func (f *Fake) GetOntologyLintReport(namespace, branchID string) (LintReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Denied != nil {
		return LintReport{}, f.Denied
	}
	if f.FailLint > 0 {
		f.FailLint--
		return LintReport{}, fmt.Errorf("lint unavailable")
	}
	f.Lints = append(f.Lints, lintCall{Namespace: namespace, BranchID: branchID})
	head := "sha256:draft-head"
	if branch, ok := f.branches[branchKey(namespace, branchID)]; ok && branch.HeadRevisionDigest != "" {
		head = branch.HeadRevisionDigest
	}
	return LintReport{RevisionDigest: head, Findings: []LintFinding{}, Complete: true}, nil
}

func (f *Fake) CreateDefinitionProposal(req CreateProposalRequest) (DefinitionProposal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Denied != nil {
		return DefinitionProposal{}, f.Denied
	}
	f.Proposals = append(f.Proposals, req)
	return DefinitionProposal{
		Namespace: req.Namespace, BranchID: req.BranchID, ProposalID: req.ProposalID,
		BaseDigest: req.BaseDigest, CandidateDigest: req.CandidateDigest,
		ProposalDigest: "sha256:proposal", Status: "open",
	}, nil
}

func (f *Fake) Snapshot() (creates, edits, lints, proposals, merges, approves int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Creates), len(f.Edits), len(f.Lints), len(f.Proposals), f.Merges, f.Approves
}

// SetHead moves the branch independently of this client's edits.
func (f *Fake) SetHead(namespace, branchID, head string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.branches == nil {
		f.branches = map[string]DefinitionBranch{}
	}
	key := branchKey(namespace, branchID)
	branch := f.branches[key]
	branch.Namespace = namespace
	branch.BranchID = branchID
	branch.HeadRevisionDigest = head
	f.branches[key] = branch
}
