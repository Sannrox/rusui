package store

import (
	"database/sql"
	"errors"
	"time"
)

type PublicationAttempt struct {
	ActionID     string
	Repo         string
	Item         int
	CandidateSHA string
	SourceHash   string
	Ref          string
	Action       string
	ProofID      int64
	State        string
	PRNumber     int
	PRHeadSHA    string
	Error        string
	Intent       string
	PolicyHash   string
}

func UpsertPublication(s *Store, p PublicationAttempt) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.Exec(`INSERT INTO publication_attempts
		(action_id, repo, item, candidate_sha, source_hash, ref, action, proof_id, state, pr_number, pr_head_sha, error, intent, policy_hash, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(action_id) DO UPDATE SET
			state=excluded.state,
			pr_number=excluded.pr_number,
			pr_head_sha=excluded.pr_head_sha,
			error=excluded.error,
			intent=excluded.intent,
			policy_hash=excluded.policy_hash,
			updated_at=excluded.updated_at`,
		p.ActionID, p.Repo, p.Item, p.CandidateSHA, p.SourceHash, p.Ref, p.Action, p.ProofID, p.State,
		p.PRNumber, p.PRHeadSHA, p.Error, p.Intent, p.PolicyHash, now, now)
	return err
}

func GetPublication(s *Store, actionID string) (*PublicationAttempt, error) {
	var p PublicationAttempt
	err := s.DB.QueryRow(`SELECT action_id, repo, item, candidate_sha, source_hash, ref, action, proof_id, state, pr_number, pr_head_sha, error, intent, policy_hash
		FROM publication_attempts WHERE action_id=?`, actionID).Scan(
		&p.ActionID, &p.Repo, &p.Item, &p.CandidateSHA, &p.SourceHash, &p.Ref, &p.Action, &p.ProofID,
		&p.State, &p.PRNumber, &p.PRHeadSHA, &p.Error, &p.Intent, &p.PolicyHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func ListRetryablePublications(s *Store) ([]PublicationAttempt, error) {
	rows, err := s.DB.Query(`SELECT action_id, repo, item, candidate_sha, source_hash, ref, action, proof_id, state, pr_number, pr_head_sha, error, intent, policy_hash
		FROM publication_attempts WHERE state IN ('planned','in_flight','uncertain')`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PublicationAttempt
	for rows.Next() {
		var p PublicationAttempt
		if err := rows.Scan(&p.ActionID, &p.Repo, &p.Item, &p.CandidateSHA, &p.SourceHash, &p.Ref, &p.Action, &p.ProofID,
			&p.State, &p.PRNumber, &p.PRHeadSHA, &p.Error, &p.Intent, &p.PolicyHash); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
