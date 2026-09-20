package store

import (
	"time"
)

type ProofRow struct {
	ID           int64
	CandidateSHA string
	SourceHash   string
	LogDigest    string
	Command      string
	ExitCode     int
	Outcome      string
}

func InsertProof(s *Store, p ProofRow) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO proofs (candidate_sha, source_hash, log_digest, command, exit_code, outcome, created_at) VALUES (?,?,?,?,?,?,?)`,
		p.CandidateSHA, p.SourceHash, p.LogDigest, p.Command, p.ExitCode, p.Outcome, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func GetProof(s *Store, id int64) (*ProofRow, error) {
	var p ProofRow
	err := s.DB.QueryRow(`SELECT id, candidate_sha, source_hash, log_digest, command, exit_code, outcome FROM proofs WHERE id=?`, id).Scan(
		&p.ID, &p.CandidateSHA, &p.SourceHash, &p.LogDigest, &p.Command, &p.ExitCode, &p.Outcome)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
