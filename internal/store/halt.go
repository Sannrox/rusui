package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	HaltScopeTenant  = "tenant"
	HaltScopeProject = "project"
	HaltScopeAgent   = "agent"
	HaltSourceOp     = "operator"
	HaltSourcePlane  = "control-plane"
)

// HaltRecord is the overlay payload for one halt target.
type HaltRecord struct {
	Reason   string `json:"reason"`
	Operator string `json:"operator"`
	Source   string `json:"source"`
}

// HaltEvent is one halt or resume audit row.
type HaltEvent struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Action   string    `json:"action"`
	Scope    string    `json:"scope"`
	Target   string    `json:"target"`
	Reason   string    `json:"reason"`
	Operator string    `json:"operator"`
	Source   string    `json:"source"`
}

func HaltOverlayKey(scope, target string) string {
	return "halt:" + NormalizeHaltScope(scope) + ":" + strings.TrimSpace(target)
}

func NormalizeHaltScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case HaltScopeTenant, HaltScopeProject, HaltScopeAgent:
		return strings.ToLower(strings.TrimSpace(scope))
	default:
		return ""
	}
}

func Halted(tx *sql.Tx, tenant, project, agent string) (bool, error) {
	for _, key := range haltKeys(tenant, project, agent) {
		v, err := OverlayGet(tx, key)
		if err != nil {
			return false, err
		}
		if v != "" {
			return true, nil
		}
	}
	return false, nil
}

func SessionHalted(tx *sql.Tx, sess *Session) (bool, error) {
	if sess == nil {
		return false, nil
	}
	return Halted(tx, sess.TenantID, sess.Project, sess.Act)
}

func SetHaltTx(tx *sql.Tx, scope, target string, rec HaltRecord) error {
	scope = NormalizeHaltScope(scope)
	target = strings.TrimSpace(target)
	if scope == "" || target == "" {
		return fmt.Errorf("halt target")
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := OverlaySet(tx, HaltOverlayKey(scope, target), string(b)); err != nil {
		return err
	}
	return insertHaltEventTx(tx, "halt", scope, target, rec)
}

func ClearHaltTx(tx *sql.Tx, scope, target string, rec HaltRecord) error {
	scope = NormalizeHaltScope(scope)
	target = strings.TrimSpace(target)
	if scope == "" || target == "" {
		return fmt.Errorf("halt target")
	}
	if _, err := tx.Exec(`DELETE FROM overlay WHERE key=?`, HaltOverlayKey(scope, target)); err != nil {
		return err
	}
	return insertHaltEventTx(tx, "resume", scope, target, rec)
}

func ListHaltEvents(s *Store) ([]HaltEvent, error) {
	rows, err := s.DB.Query(`SELECT id, at, action, scope, target, reason, operator, source FROM halt_events ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []HaltEvent
	for rows.Next() {
		var ev HaltEvent
		var at string
		if err := rows.Scan(&ev.ID, &at, &ev.Action, &ev.Scope, &ev.Target, &ev.Reason, &ev.Operator, &ev.Source); err != nil {
			return nil, err
		}
		ev.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, ev)
	}
	return out, rows.Err()
}

func ListOpenSessionIDsForHaltTx(tx *sql.Tx, scope, target string, repos []string) ([]int64, error) {
	scope = NormalizeHaltScope(scope)
	target = strings.TrimSpace(target)
	if scope == "" || target == "" {
		return nil, fmt.Errorf("halt target")
	}
	q := `SELECT id FROM sessions WHERE COALESCE(archived,0)=0 AND `
	args := []any{target}
	switch scope {
	case HaltScopeTenant:
		q += `COALESCE(tenant_id,'default')=?`
		args[0] = NormalizeTenantID(target)
	case HaltScopeProject:
		if len(repos) == 0 {
			q += `project=?`
		} else {
			q += `(project=? OR (COALESCE(project,'')='' AND repo IN (` + haltPlaceholders(len(repos)) + `)))`
			for _, r := range repos {
				args = append(args, r)
			}
		}
	case HaltScopeAgent:
		q += `COALESCE(NULLIF(act,''), created_by, tenant_id, 'default')=?`
		args[0] = NormalizeActor(target)
	}
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func haltPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

func haltKeys(tenant, project, agent string) []string {
	var keys []string
	if t := NormalizeTenantID(tenant); t != "" {
		keys = append(keys, HaltOverlayKey(HaltScopeTenant, t))
	}
	if p := strings.TrimSpace(project); p != "" {
		keys = append(keys, HaltOverlayKey(HaltScopeProject, p))
	}
	if a := NormalizeActor(agent); a != "" {
		keys = append(keys, HaltOverlayKey(HaltScopeAgent, a))
	}
	return keys
}

func insertHaltEventTx(tx *sql.Tx, action, scope, target string, rec HaltRecord) error {
	_, err := tx.Exec(
		`INSERT INTO halt_events(at, action, scope, target, reason, operator, source) VALUES(?,?,?,?,?,?,?)`,
		time.Now().UTC().Format(time.RFC3339Nano),
		action,
		scope,
		target,
		rec.Reason,
		rec.Operator,
		rec.Source,
	)
	return err
}
