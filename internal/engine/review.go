package engine

import (
	"encoding/json"
	"fmt"

	"github.com/sannrox/rusui/internal/publish"
	"github.com/sannrox/rusui/internal/store"
)

var (
	ErrSelfReview  = fmt.Errorf("self review")
	ErrNoProof     = fmt.Errorf("proof required")
	ErrStaleReview = fmt.Errorf("stale candidate")
	ErrRepairLimit = fmt.Errorf("repair limit")
)

type ReviewInput struct {
	ProofID           int64
	AuthorSessionID   int64
	ReviewerSessionID int64
	CandidateSHA      string
	Disposition       string // accept, reject, block
	Findings          []Finding
	RepairCount       int
}

// IndependentReview records a second-session judgment of a proven candidate.
// Self-attestation and missing proof cannot make publication ready.
func (e *Engine) IndependentReview(in ReviewInput) (int64, error) {
	if in.ReviewerSessionID == 0 || in.ReviewerSessionID == in.AuthorSessionID {
		return 0, ErrSelfReview
	}
	if in.Disposition != "accept" && in.Disposition != "reject" && in.Disposition != "block" {
		return 0, fmt.Errorf("disposition")
	}
	if in.RepairCount > publish.MaxRepair {
		return 0, ErrRepairLimit
	}
	p, err := store.GetProof(e.Store, in.ProofID)
	if err != nil {
		return 0, ErrNoProof
	}
	if p.CandidateSHA != in.CandidateSHA || p.Outcome != string(publish.Proven) {
		return 0, ErrStaleReview
	}
	if in.Disposition == "accept" && len(in.Findings) > 0 {
		return 0, fmt.Errorf("unresolved findings")
	}
	if in.Disposition != "accept" && len(in.Findings) == 0 {
		return 0, fmt.Errorf("findings required")
	}
	b, err := json.Marshal(in.Findings)
	if err != nil {
		return 0, err
	}
	return store.InsertIndependentReview(e.Store, store.IndependentReview{
		ProofID: in.ProofID, AuthorSessionID: in.AuthorSessionID, ReviewerSessionID: in.ReviewerSessionID,
		CandidateSHA: in.CandidateSHA, Disposition: in.Disposition, Findings: string(b),
	})
}

// PublicationReady is true only with a proven proof and an independent accept
// on the same candidate SHA. It does not perform a GitHub write.
func (e *Engine) PublicationReady(candidateSHA string, proofID int64) (bool, error) {
	p, err := store.GetProof(e.Store, proofID)
	if err != nil || p.Outcome != string(publish.Proven) || p.CandidateSHA != candidateSHA {
		return false, nil
	}
	_, ok, err := store.LatestAcceptForCandidate(e.Store, candidateSHA)
	if err != nil {
		return false, err
	}
	return ok, nil
}
