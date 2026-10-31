package store

import (
	"database/sql"
	"errors"
	"time"
)

const (
	RepairSelected = "selected"
	RepairDeclined = "declined"
)

// RepairDecision is one selected or declined automatic-repair candidate.
type RepairDecision struct {
	ID        int64
	Repo      string
	Item      int
	ItemHash  string
	Decision  string
	Reason    string
	CreatedAt string
}

func InsertRepairDecisionTx(tx *sql.Tx, d RepairDecision) (int64, error) {
	if d.CreatedAt == "" {
		d.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	res, err := tx.Exec(`INSERT INTO repair_decisions (repo, item, item_hash, decision, reason, created_at) VALUES (?,?,?,?,?,?)`,
		d.Repo, d.Item, d.ItemHash, d.Decision, d.Reason, d.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func DeleteRepairDecisionTx(tx *sql.Tx, id int64) error {
	_, err := tx.Exec(`DELETE FROM repair_decisions WHERE id=?`, id)
	return err
}

func CountRepairsTodayTx(tx *sql.Tx, repo, day string) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(DISTINCT item) FROM repair_decisions WHERE repo=? AND decision=? AND substr(created_at,1,10)=?`,
		repo, RepairSelected, day).Scan(&n)
	return n, err
}

// LatestDeclineTx returns the first competing-effort decline of this
// item hash. That timestamp starts the seven-day retry window. Policy-off,
// pause, and budget declines do not start the window; a later refresh
// must not insert another declined row merely because the window is open.
func LatestDeclineTx(tx *sql.Tx, repo string, item int, itemHash string) (createdAt string, ok bool, err error) {
	err = tx.QueryRow(`SELECT created_at FROM repair_decisions WHERE repo=? AND item=? AND item_hash=? AND decision=? AND reason IN ('competing_branch','competing_pr') ORDER BY id ASC LIMIT 1`,
		repo, item, itemHash, RepairDeclined).Scan(&createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return createdAt, err == nil, err
}

func LatestSelected(s *Store, repo string, item int) (itemHash string, ok bool, err error) {
	err = s.DB.QueryRow(`SELECT item_hash FROM repair_decisions WHERE repo=? AND item=? AND decision=? ORDER BY id DESC LIMIT 1`,
		repo, item, RepairSelected).Scan(&itemHash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return itemHash, err == nil, err
}

func LatestSelectedRowTx(tx *sql.Tx, repo string, item int) (id int64, itemHash string, ok bool, err error) {
	err = tx.QueryRow(`SELECT id, item_hash FROM repair_decisions WHERE repo=? AND item=? AND decision=? ORDER BY id DESC LIMIT 1`,
		repo, item, RepairSelected).Scan(&id, &itemHash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	return id, itemHash, err == nil, err
}
