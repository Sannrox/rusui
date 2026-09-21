package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/publish"
	"github.com/sannrox/rusui/internal/store"
)

const (
	PubSucceeded = "succeeded"
	PubDenied    = "denied"
	PubCancelled = "cancelled"
	PubUncertain = "uncertain"
	PubInFlight  = "in_flight"
)

type PublishRequest struct {
	Source     publish.Source
	Candidate  publish.Candidate
	ProofID    int64
	Action     publish.Action
	Title      string
	Body       string
	Cancelled  bool
	PolicyHash string
}

type PublishResult struct {
	ActionID string
	State    string
	Gate     publish.Outcome
	PR       *gh.PullRequest
}

func publicationActionID(req PublishRequest) string {
	s := fmt.Sprintf("%s|%d|%s|%s", req.Source.Repo, req.Source.Item, req.Candidate.CommitSHA, req.Action)
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func publicationBody(req PublishRequest, proof *store.ProofRow) string {
	if strings.TrimSpace(req.Body) != "" {
		return req.Body
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Problem/change: verified candidate %s for %s#%d.\n\n", req.Candidate.CommitSHA, req.Source.Repo, req.Source.Item)
	fmt.Fprintf(&b, "Validation: proof id %d command %s exit %d digest %s source %s.\n", proof.ID, proof.Command, proof.ExitCode, proof.LogDigest, proof.SourceHash)
	fmt.Fprintf(&b, "Limits: draft pull request only; unnamed GitHub writes are denied. Human merge is required.\n")
	fmt.Fprintf(&b, "Provenance: session %d ref %s snapshot %s main %s.\n", req.Candidate.SessionID, req.Candidate.Ref, req.Source.SnapshotHash, req.Source.MainSHA)
	return b.String()
}

func (e *Engine) evaluatePublish(req PublishRequest) (publish.Outcome, *store.ProofRow, error) {
	if req.Cancelled {
		return publish.Cancelled, nil, nil
	}
	if !publish.Permitted(req.Action) {
		return publish.Denied, nil, nil
	}
	p, err := store.GetProof(e.Store, req.ProofID)
	if err != nil {
		return publish.Denied, nil, err
	}
	rev, ok, err := store.LatestAcceptForCandidate(e.Store, req.Candidate.CommitSHA)
	if err != nil {
		return publish.Denied, nil, err
	}
	if !ok || rev == nil {
		return publish.Denied, p, nil
	}
	attempt := publish.Attempt{
		Source:    req.Source,
		Candidate: req.Candidate,
		Action:    req.Action,
		Cancelled: req.Cancelled,
		Proof: publish.Proof{
			Command: p.Command, ExitCode: p.ExitCode, LogDigest: p.LogDigest,
			HeadSHA: p.CandidateSHA, BaseSHA: req.Source.MainSHA, Isolated: true,
			AuthorSessionID: rev.AuthorSessionID, ReviewerSessionID: rev.ReviewerSessionID,
			SourceSnapshotHash: p.SourceHash, ChecksAvailable: p.ExitCode == 0,
		},
	}
	return publish.Evaluate(attempt), p, nil
}

func (e *Engine) persistPub(req PublishRequest, actionID, state, errText, intent string, pr *gh.PullRequest) error {
	row := store.PublicationAttempt{
		ActionID: actionID, Repo: req.Source.Repo, Item: req.Source.Item,
		CandidateSHA: req.Candidate.CommitSHA, SourceHash: req.Source.SnapshotHash,
		Ref: req.Candidate.Ref, Action: string(req.Action), ProofID: req.ProofID,
		State: state, Error: errText, Intent: intent, PolicyHash: e.Policy.Hash,
	}
	if pr != nil {
		row.PRNumber = pr.Number
		row.PRHeadSHA = pr.HeadSHA
	}
	return store.UpsertPublication(e.Store, row)
}

// Publish opens or updates one draft PR for a proven candidate. It rechecks
// policy, identities, proof, independent review, ref scope, and cancellation
// immediately before mutation, persists intent first, and reconciles a lost
// response by reading remote state.
func (e *Engine) Publish(req PublishRequest) (*PublishResult, error) {
	actionID := publicationActionID(req)
	res := &PublishResult{ActionID: actionID}

	if existing, err := store.GetPublication(e.Store, actionID); err != nil {
		return nil, err
	} else if existing != nil && existing.State == PubSucceeded && existing.PRHeadSHA == req.Candidate.CommitSHA {
		res.State = PubSucceeded
		res.PR = &gh.PullRequest{Number: existing.PRNumber, HeadSHA: existing.PRHeadSHA, HeadRef: existing.Ref, Draft: true}
		return res, nil
	}

	if req.Cancelled {
		res.State = PubCancelled
		res.Gate = publish.Cancelled
		_ = e.persistPub(req, actionID, PubCancelled, "", "", nil)
		return res, nil
	}
	if e.Publisher == nil {
		res.State = PubDenied
		res.Gate = publish.Denied
		_ = e.persistPub(req, actionID, PubDenied, "publisher required", "", nil)
		return res, nil
	}
	pol, ok := e.Policy.Repo(req.Source.Repo)
	if !ok || !pol.Implement {
		res.State = PubDenied
		res.Gate = publish.Denied
		_ = e.persistPub(req, actionID, PubDenied, "policy", "", nil)
		return res, nil
	}
	if req.PolicyHash != "" && req.PolicyHash != e.Policy.Hash {
		res.State = PubDenied
		res.Gate = publish.Denied
		_ = e.persistPub(req, actionID, PubDenied, "policy changed", "", nil)
		return res, nil
	}

	gate, proof, err := e.evaluatePublish(req)
	if err != nil {
		return nil, err
	}
	res.Gate = gate
	if gate != publish.Proven {
		res.State = PubDenied
		if gate == publish.Cancelled {
			res.State = PubCancelled
		}
		_ = e.persistPub(req, actionID, res.State, string(gate), "", nil)
		return res, nil
	}
	ready, err := e.PublicationReady(req.Candidate.CommitSHA, req.ProofID)
	if err != nil {
		return nil, err
	}
	if !ready {
		res.State = PubDenied
		_ = e.persistPub(req, actionID, PubDenied, "not ready", "", nil)
		return res, nil
	}

	title := req.Title
	if title == "" {
		title = fmt.Sprintf("rusui: %s#%d %s", req.Source.Repo, req.Source.Item, req.Candidate.CommitSHA[:min(12, len(req.Candidate.CommitSHA))])
	}
	intent := publicationBody(req, proof)
	if err := e.persistPub(req, actionID, PubInFlight, "", intent, nil); err != nil {
		return nil, err
	}

	head := strings.TrimPrefix(req.Candidate.Ref, "refs/heads/")
	base, baseErr := e.Publisher.DefaultBranch(req.Source.Repo)
	if baseErr != nil || base == "" {
		res.State = PubUncertain
		errText := "default branch"
		if baseErr != nil {
			errText = baseErr.Error()
		}
		_ = e.persistPub(req, actionID, PubUncertain, errText, intent, nil)
		return res, baseErr
	}
	remote, findErr := e.Publisher.FindDraftPR(req.Source.Repo, head)
	if findErr != nil {
		res.State = PubUncertain
		_ = e.persistPub(req, actionID, PubUncertain, findErr.Error(), intent, nil)
		return res, findErr
	}
	if remote != nil && remote.HeadSHA == req.Candidate.CommitSHA {
		res.State = PubSucceeded
		res.PR = remote
		_ = e.persistPub(req, actionID, PubSucceeded, "", intent, remote)
		return res, nil
	}

	var pr *gh.PullRequest
	var mutErr error
	switch {
	case remote == nil && req.Action == publish.ActionOpenPR:
		pr, mutErr = e.Publisher.OpenDraftPR(req.Source.Repo, title, intent, head, base)
		if pr != nil && pr.HeadSHA == "" {
			pr.HeadSHA = req.Candidate.CommitSHA
		}
	case remote != nil && remote.HeadSHA != req.Candidate.CommitSHA:
		res.State = PubDenied
		_ = e.persistPub(req, actionID, PubDenied, "stale remote head", intent, remote)
		return res, nil
	case remote != nil:
		pr = remote
	default:
		res.State = PubDenied
		_ = e.persistPub(req, actionID, PubDenied, "no matching action", intent, nil)
		return res, nil
	}

	if mutErr != nil {
		again, _ := e.Publisher.FindDraftPR(req.Source.Repo, head)
		if again != nil && (again.HeadSHA == req.Candidate.CommitSHA || again.HeadSHA == "") {
			if again.HeadSHA == "" {
				again.HeadSHA = req.Candidate.CommitSHA
			}
			res.State = PubSucceeded
			res.PR = again
			_ = e.persistPub(req, actionID, PubSucceeded, "", intent, again)
			return res, nil
		}
		res.State = PubUncertain
		_ = e.persistPub(req, actionID, PubUncertain, mutErr.Error(), intent, again)
		return res, mutErr
	}
	if pr != nil && pr.HeadSHA == "" {
		pr.HeadSHA = req.Candidate.CommitSHA
	}
	res.State = PubSucceeded
	res.PR = pr
	_ = e.persistPub(req, actionID, PubSucceeded, "", intent, pr)
	return res, nil
}

func sessionFromRef(ref string) int64 {
	rest, ok := strings.CutPrefix(strings.TrimPrefix(ref, "refs/heads/"), "rusui/")
	if !ok {
		return 0
	}
	idStr, _, _ := strings.Cut(rest, "/")
	n, _ := strconv.ParseInt(idStr, 10, 64)
	return n
}

func (e *Engine) RetryPublications() error {
	items, err := store.ListRetryablePublications(e.Store)
	if err != nil {
		return err
	}
	var first error
	for _, it := range items {
		req := PublishRequest{
			Source: publish.Source{Repo: it.Repo, Item: it.Item, SnapshotHash: it.SourceHash},
			Candidate: publish.Candidate{
				SessionID: sessionFromRef(it.Ref), CommitSHA: it.CandidateSHA, Ref: it.Ref,
			},
			ProofID: it.ProofID,
			Action:  publish.Action(it.Action),
			Body:    it.Intent,
		}
		if _, err := e.Publish(req); err != nil && first == nil {
			first = err
		}
	}
	return first
}
