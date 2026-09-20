package store

import (
	"database/sql"
	"errors"
	"time"
)

type Task struct {
	ID            int64
	EffortKey     string
	Revision      int
	SpecHash      string
	SessionID     int64
	Repo          string
	Ref           string
	BaseSHA       string
	PolicyHash    string
	AllowedPaths  string
	ContextRefs   string
	Prompt        string
	BudgetRepairs int
	State         string
}

func LookupTaskBySpecHashTx(tx *sql.Tx, specHash string) (*Task, bool, error) {
	t, err := scanTask(tx.QueryRow(`SELECT id, effort_key, revision, spec_hash, session_id, repo, ref, base_sha, policy_hash, allowed_paths, context_refs, prompt, budget_repairs, state FROM tasks WHERE spec_hash=?`, specHash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return t, true, nil
}

func LatestTaskTx(tx *sql.Tx, effortKey string) (*Task, bool, error) {
	t, err := scanTask(tx.QueryRow(`SELECT id, effort_key, revision, spec_hash, session_id, repo, ref, base_sha, policy_hash, allowed_paths, context_refs, prompt, budget_repairs, state FROM tasks WHERE effort_key=? ORDER BY revision DESC LIMIT 1`, effortKey))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return t, true, nil
}

func InsertTaskTx(tx *sql.Tx, t Task) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.Exec(`INSERT INTO tasks (effort_key, revision, spec_hash, session_id, repo, ref, base_sha, policy_hash, allowed_paths, context_refs, prompt, budget_repairs, state, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.EffortKey, t.Revision, t.SpecHash, t.SessionID, t.Repo, t.Ref, t.BaseSHA, t.PolicyHash, t.AllowedPaths, t.ContextRefs, t.Prompt, t.BudgetRepairs, t.State, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func SupersedeTaskTx(tx *sql.Tx, id int64) error {
	_, err := tx.Exec(`UPDATE tasks SET state='superseded' WHERE id=?`, id)
	return err
}

func GetTask(s *Store, id int64) (*Task, error) {
	return scanTask(s.DB.QueryRow(`SELECT id, effort_key, revision, spec_hash, session_id, repo, ref, base_sha, policy_hash, allowed_paths, context_refs, prompt, budget_repairs, state FROM tasks WHERE id=?`, id))
}

func scanTask(row interface{ Scan(...any) error }) (*Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.EffortKey, &t.Revision, &t.SpecHash, &t.SessionID, &t.Repo, &t.Ref, &t.BaseSHA, &t.PolicyHash, &t.AllowedPaths, &t.ContextRefs, &t.Prompt, &t.BudgetRepairs, &t.State)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
