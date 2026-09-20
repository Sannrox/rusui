package store

import (
	"database/sql"
	"errors"
	"time"
)

type IndependentReview struct {
	ID                int64
	ProofID           int64
	AuthorSessionID   int64
	ReviewerSessionID int64
	CandidateSHA      string
	Disposition       string
	Findings          string
}

func InsertIndependentReview(s *Store, r IndependentReview) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO independent_reviews (proof_id, author_session_id, reviewer_session_id, candidate_sha, disposition, findings, created_at) VALUES (?,?,?,?,?,?,?)`,
		r.ProofID, r.AuthorSessionID, r.ReviewerSessionID, r.CandidateSHA, r.Disposition, r.Findings, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func CountReviewsForProof(s *Store, proofID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM independent_reviews WHERE proof_id=?`, proofID).Scan(&n)
	return n, err
}

func LatestAcceptForCandidate(s *Store, sha string) (*IndependentReview, bool, error) {
	var r IndependentReview
	err := s.DB.QueryRow(`SELECT id, proof_id, author_session_id, reviewer_session_id, candidate_sha, disposition, findings FROM independent_reviews WHERE candidate_sha=? AND disposition='accept' ORDER BY id DESC LIMIT 1`, sha).Scan(
		&r.ID, &r.ProofID, &r.AuthorSessionID, &r.ReviewerSessionID, &r.CandidateSHA, &r.Disposition, &r.Findings)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &r, true, nil
}
