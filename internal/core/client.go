// Package core is the plane-held sekai-chisei client for definition drafts.
// The guest never holds this credential (ADR 0009, ADR 0084).
package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const serviceSekai = "sekai.SekaiService"

// Client is the plane-side draft authoring surface. Tests use Fake.
type Client interface {
	CreateDefinitionBranch(CreateBranchRequest) (DefinitionBranch, error)
	GetDefinitionBranch(namespace, branchID string) (DefinitionBranch, error)
	ApplyDefinitionBranchEdit(ApplyEditRequest) (ApplyEditResult, error)
	GetOntologyLintReport(namespace, branchID string) (LintReport, error)
	CreateDefinitionProposal(CreateProposalRequest) (DefinitionProposal, error)
}

// DeniedError is a core PermissionDenied mapped for session visibility.
type DeniedError struct {
	Message string
}

func (e DeniedError) Error() string {
	if e.Message == "" {
		return "core: permission denied"
	}
	return "core: permission denied: " + e.Message
}

// IsPermissionDenied reports a core PermissionDenied.
func IsPermissionDenied(err error) bool {
	var d DeniedError
	return errors.As(err, &d)
}

// NotFoundError is a core NotFound (missing branch).
type NotFoundError struct {
	Message string
}

func (e NotFoundError) Error() string {
	if e.Message == "" {
		return "core: not found"
	}
	return "core: not found: " + e.Message
}

// IsNotFound reports a core NotFound.
func IsNotFound(err error) bool {
	var n NotFoundError
	return errors.As(err, &n)
}

// AlreadyExistsError is a core AlreadyExists (branch identity in use).
type AlreadyExistsError struct {
	Message string
}

func (e AlreadyExistsError) Error() string {
	if e.Message == "" {
		return "core: already exists"
	}
	return "core: already exists: " + e.Message
}

// IsAlreadyExists reports a core AlreadyExists.
func IsAlreadyExists(err error) bool {
	var a AlreadyExistsError
	return errors.As(err, &a)
}

type CreateBranchRequest struct {
	Namespace            string `json:"namespace"`
	BranchID             string `json:"branch_id"`
	ParentRevisionDigest string `json:"parent_revision_digest"`
	IdempotencyKey       string `json:"idempotency_key"`
}

type DefinitionBranch struct {
	Namespace          string `json:"namespace"`
	BranchID           string `json:"branch_id"`
	HeadRevisionDigest string `json:"head_revision_digest"`
	BaseRevisionDigest string `json:"base_revision_digest,omitempty"`
}

type MemberInput struct {
	MemberKind     string `json:"member_kind"`
	MemberID       string `json:"member_id"`
	DefinitionJSON string `json:"definition_json"`
}

type ApplyEditRequest struct {
	Namespace          string        `json:"namespace"`
	BranchID           string        `json:"branch_id"`
	ExpectedHeadDigest string        `json:"expected_head_digest"`
	Upserts            []MemberInput `json:"upserts"`
	IdempotencyKey     string        `json:"idempotency_key"`
}

type ApplyEditResult struct {
	Branch             DefinitionBranch `json:"branch"`
	PreviousHeadDigest string           `json:"previous_head_digest,omitempty"`
}

type LintFinding struct {
	Rule        string `json:"rule"`
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`
	Severity    string `json:"severity"`
	Detail      string `json:"detail,omitempty"`
}

type LintReport struct {
	RevisionDigest string        `json:"revision_digest"`
	Findings       []LintFinding `json:"findings"`
	Complete       bool          `json:"complete"`
}

type CreateProposalRequest struct {
	Namespace       string `json:"namespace"`
	BranchID        string `json:"branch_id"`
	ProposalID      string `json:"proposal_id"`
	BaseDigest      string `json:"base_digest"`
	CandidateDigest string `json:"candidate_digest"`
	IdempotencyKey  string `json:"idempotency_key"`
}

type DefinitionProposal struct {
	Namespace       string `json:"namespace"`
	BranchID        string `json:"branch_id"`
	ProposalID      string `json:"proposal_id"`
	BaseDigest      string `json:"base_digest"`
	CandidateDigest string `json:"candidate_digest"`
	ProposalDigest  string `json:"proposal_digest,omitempty"`
	Status          string `json:"status,omitempty"`
}

// HTTP calls the core HTTP/JSON projection with a plane-held bearer.
// Draft RPCs are experimental on core; the server must admit them.
// Hosting those methods on the projection is core's surface.
type HTTP struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

func NewHTTP(baseURL, token string) *HTTP {
	return &HTTP{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		Client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (h *HTTP) CreateDefinitionBranch(req CreateBranchRequest) (DefinitionBranch, error) {
	var out struct {
		Branch wireBranch `json:"branch"`
	}
	if err := h.call("CreateDefinitionBranch", wireCreateBranch(req), &out); err != nil {
		return DefinitionBranch{}, err
	}
	return out.Branch.domain(), nil
}

func (h *HTTP) GetDefinitionBranch(namespace, branchID string) (DefinitionBranch, error) {
	var out struct {
		Branch wireBranch `json:"branch"`
	}
	if err := h.call("GetDefinitionBranch", wireGetBranch{Namespace: namespace, BranchID: branchID}, &out); err != nil {
		return DefinitionBranch{}, err
	}
	return out.Branch.domain(), nil
}

func (h *HTTP) ApplyDefinitionBranchEdit(req ApplyEditRequest) (ApplyEditResult, error) {
	upserts := make([]wireMember, 0, len(req.Upserts))
	for _, m := range req.Upserts {
		upserts = append(upserts, wireMember(m))
	}
	var out struct {
		Branch             wireBranch `json:"branch"`
		PreviousHeadDigest string     `json:"previousHeadDigest"`
	}
	if err := h.call("ApplyDefinitionBranchEdit", wireApplyEdit{
		Namespace:          req.Namespace,
		BranchID:           req.BranchID,
		ExpectedHeadDigest: req.ExpectedHeadDigest,
		Upserts:            upserts,
		IdempotencyKey:     req.IdempotencyKey,
	}, &out); err != nil {
		return ApplyEditResult{}, err
	}
	return ApplyEditResult{Branch: out.Branch.domain(), PreviousHeadDigest: out.PreviousHeadDigest}, nil
}

func (h *HTTP) GetOntologyLintReport(namespace, branchID string) (LintReport, error) {
	var out struct {
		RevisionDigest string            `json:"revisionDigest"`
		Findings       []wireLintFinding `json:"findings"`
		Complete       bool              `json:"complete"`
	}
	if err := h.call("GetOntologyLintReport", wireLint{Namespace: namespace, BranchID: branchID}, &out); err != nil {
		return LintReport{}, err
	}
	findings := make([]LintFinding, 0, len(out.Findings))
	for _, f := range out.Findings {
		findings = append(findings, LintFinding(f))
	}
	return LintReport{RevisionDigest: out.RevisionDigest, Findings: findings, Complete: out.Complete}, nil
}

func (h *HTTP) CreateDefinitionProposal(req CreateProposalRequest) (DefinitionProposal, error) {
	var out struct {
		Proposal wireProposal `json:"proposal"`
	}
	if err := h.call("CreateDefinitionProposal", wireCreateProposal(req), &out); err != nil {
		return DefinitionProposal{}, err
	}
	return out.Proposal.domain(), nil
}

func (h *HTTP) call(rpc string, body, out any) error {
	if h == nil || h.BaseURL == "" || h.Token == "" {
		return fmt.Errorf("core: url and token required")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	cli := h.Client
	if cli == nil {
		cli = http.DefaultClient
	}
	req, err := http.NewRequest(http.MethodPost, h.BaseURL+"/"+serviceSekai+"/"+rpc, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.Token)
	res, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var fail struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(payload, &fail)
		if fail.Message == "" {
			fail.Message = strings.TrimSpace(string(payload))
		}
		if fail.Message == "" {
			fail.Message = res.Status
		}
		switch {
		case fail.Code == "permission_denied" || (fail.Code == "" && res.StatusCode == http.StatusForbidden):
			return DeniedError{Message: fail.Message}
		case fail.Code == "not_found" || (fail.Code == "" && res.StatusCode == http.StatusNotFound):
			return NotFoundError{Message: fail.Message}
		case fail.Code == "already_exists" || (fail.Code == "" && res.StatusCode == http.StatusConflict):
			return AlreadyExistsError{Message: fail.Message}
		default:
			return fmt.Errorf("core: %s: %s", rpc, fail.Message)
		}
	}
	if out == nil || len(payload) == 0 {
		return nil
	}
	return json.Unmarshal(payload, out)
}

type wireCreateBranch struct {
	Namespace            string `json:"namespace"`
	BranchID             string `json:"branchId"`
	ParentRevisionDigest string `json:"parentRevisionDigest"`
	IdempotencyKey       string `json:"idempotencyKey"`
}

type wireGetBranch struct {
	Namespace string `json:"namespace"`
	BranchID  string `json:"branchId"`
}

type wireMember struct {
	MemberKind     string `json:"memberKind"`
	MemberID       string `json:"memberId"`
	DefinitionJSON string `json:"definitionJson"`
}

type wireApplyEdit struct {
	Namespace          string       `json:"namespace"`
	BranchID           string       `json:"branchId"`
	ExpectedHeadDigest string       `json:"expectedHeadDigest"`
	Upserts            []wireMember `json:"upserts"`
	IdempotencyKey     string       `json:"idempotencyKey"`
}

type wireLint struct {
	Namespace string `json:"namespace"`
	BranchID  string `json:"branchId"`
}

type wireLintFinding struct {
	Rule        string `json:"rule"`
	SubjectKind string `json:"subjectKind"`
	SubjectID   string `json:"subjectId"`
	Severity    string `json:"severity"`
	Detail      string `json:"detail,omitempty"`
}

type wireCreateProposal struct {
	Namespace       string `json:"namespace"`
	BranchID        string `json:"branchId"`
	ProposalID      string `json:"proposalId"`
	BaseDigest      string `json:"baseDigest"`
	CandidateDigest string `json:"candidateDigest"`
	IdempotencyKey  string `json:"idempotencyKey"`
}

type wireBranch struct {
	Namespace          string `json:"namespace"`
	BranchID           string `json:"branchId"`
	HeadRevisionDigest string `json:"headRevisionDigest"`
	BaseRevisionDigest string `json:"baseRevisionDigest"`
}

func (b wireBranch) domain() DefinitionBranch {
	return DefinitionBranch(b)
}

type wireProposal struct {
	Namespace       string `json:"namespace"`
	BranchID        string `json:"branchId"`
	ProposalID      string `json:"proposalId"`
	BaseDigest      string `json:"baseDigest"`
	CandidateDigest string `json:"candidateDigest"`
	ProposalDigest  string `json:"proposalDigest"`
	Status          string `json:"status"`
}

func (p wireProposal) domain() DefinitionProposal {
	return DefinitionProposal(p)
}
