package store

import (
	"database/sql"
	"errors"
	"time"
)

type EffortFeedback struct {
	ID           int64
	EffortKey    string
	Seq          int
	TaskID       int64
	CandidateSHA string
	PromptHash   string
	Prompt       string
}

func GetLatestTask(s *Store, effortKey string) (*Task, error) {
	t, err := scanTask(s.DB.QueryRow(`SELECT id, effort_key, revision, spec_hash, session_id, repo, ref, base_sha, policy_hash, allowed_paths, context_refs, prompt, budget_repairs, state FROM tasks WHERE effort_key=? ORDER BY revision DESC LIMIT 1`, effortKey))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

func SetTaskState(s *Store, id int64, state string) error {
	_, err := s.DB.Exec(`UPDATE tasks SET state=? WHERE id=?`, state, id)
	return err
}

func LatestFeedback(s *Store, effortKey string) (*EffortFeedback, error) {
	var f EffortFeedback
	err := s.DB.QueryRow(`SELECT id, effort_key, seq, task_id, candidate_sha, prompt_hash, prompt FROM effort_feedback WHERE effort_key=? ORDER BY seq DESC LIMIT 1`, effortKey).Scan(
		&f.ID, &f.EffortKey, &f.Seq, &f.TaskID, &f.CandidateSHA, &f.PromptHash, &f.Prompt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func GetFeedback(s *Store, effortKey string, seq int) (*EffortFeedback, error) {
	var f EffortFeedback
	err := s.DB.QueryRow(`SELECT id, effort_key, seq, task_id, candidate_sha, prompt_hash, prompt FROM effort_feedback WHERE effort_key=? AND seq=?`, effortKey, seq).Scan(
		&f.ID, &f.EffortKey, &f.Seq, &f.TaskID, &f.CandidateSHA, &f.PromptHash, &f.Prompt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func InsertFeedback(s *Store, f EffortFeedback) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO effort_feedback (effort_key, seq, task_id, candidate_sha, prompt_hash, prompt, created_at) VALUES (?,?,?,?,?,?,?)`,
		f.EffortKey, f.Seq, f.TaskID, f.CandidateSHA, f.PromptHash, f.Prompt, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func InvalidateProofs(s *Store, sha string) error {
	_, err := s.DB.Exec(`UPDATE proofs SET outcome='source_drift' WHERE candidate_sha=?`, sha)
	return err
}
