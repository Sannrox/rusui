package server

import (
	"net/http"
	"strconv"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func (s *Server) consoleRequire(w http.ResponseWriter, r *http.Request) bool {
	if !s.consoleGate(w, r) {
		return false
	}
	if !s.consoleAuthed(r) {
		http.Redirect(w, r, "/console/signin", http.StatusSeeOther)
		return false
	}
	return true
}

func (s *Server) consoleApprovals(w http.ResponseWriter, r *http.Request) {
	if !s.consoleRequire(w, r) {
		return
	}
	list, err := store.ListPendingApprovalsTenant(s.Eng.Store, store.DefaultTenantID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	out := make([]consoleApproval, 0, len(list))
	for _, a := range list {
		if !s.actionSessionVisible(r, &a) {
			continue
		}
		item := consoleApproval{ID: a.ID, Repo: a.Repo, Item: a.Item, Reason: a.ReasonCode, Body: a.Body, Scope: a.Repo, Expiry: "turn lease"}
		if a.SessionID != nil {
			item.SessionID = *a.SessionID
		}
		if a.TurnID != nil {
			if turn, err := store.GetTurn(s.Eng.Store, *a.TurnID); err == nil {
				item.Expiry = "turn " + turn.State
			}
		}
		out = append(out, item)
	}
	s.renderConsole(w, consolePage{Title: "Approvals", View: "approvals", Authed: true, CSRF: s.consoleCSRF(), Approvals: out})
}

func (s *Server) consoleDecide(w http.ResponseWriter, r *http.Request) {
	if !s.consoleRequire(w, r) {
		return
	}
	if r.PathValue("id") == "" {
		http.Error(w, "missing id", 400)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil || r.FormValue("csrf") != s.consoleCSRF() {
		http.Error(w, "csrf", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	if act, err := store.GetAction(s.Eng.Store, id); err != nil || !s.actionSessionVisible(r, act) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	dec := r.FormValue("decision")
	if dec != "allow" && dec != "deny" {
		http.Error(w, "decision", 400)
		return
	}
	notice := ""
	if _, found, err := store.GetApprovalDecision(s.Eng.Store, id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	} else if found {
		notice = "stale: already decided"
	} else if err := store.PutApprovalDecision(s.Eng.Store, id, dec); err != nil {
		notice = "could not record decision"
	} else if dec == "allow" && !s.allowStillValid(id) {
		notice = "recorded allow but not valid for resume (expired, paused, or policy)"
	} else {
		notice = "decision " + dec
		if act, err := store.GetAction(s.Eng.Store, id); err == nil && act.SessionID != nil {
			http.Redirect(w, r, "/console/sessions/"+strconv.FormatInt(*act.SessionID, 10)+"?notice="+notice, http.StatusSeeOther)
			return
		}
	}
	list, _ := store.ListPendingApprovalsTenant(s.Eng.Store, store.DefaultTenantID)
	out := make([]consoleApproval, 0, len(list))
	for _, a := range list {
		sid := int64(0)
		if a.SessionID != nil {
			sid = *a.SessionID
		}
		out = append(out, consoleApproval{ID: a.ID, SessionID: sid, Repo: a.Repo, Item: a.Item, Reason: a.ReasonCode, Body: a.Body, Scope: a.Repo, Expiry: "turn lease"})
	}
	s.renderConsole(w, consolePage{Title: "Approvals", View: "approvals", Authed: true, CSRF: s.consoleCSRF(), Approvals: out, Notice: notice})
}

func (s *Server) consoleTable(w http.ResponseWriter, r *http.Request, title string, head []string, rows [][]string, notice string) {
	s.renderConsole(w, consolePage{Title: title, View: "table", Authed: true, CSRF: s.consoleCSRF(), Heading: title, Head: head, Rows: rows, Notice: notice})
}

func (s *Server) consoleEnvironments(w http.ResponseWriter, r *http.Request) {
	if !s.consoleRequire(w, r) {
		return
	}
	rows, err := s.Eng.Store.DB.Query(`SELECT DISTINCT e.id, e.name, e.driver, e.state, IFNULL(e.source_hash,'') FROM environments e JOIN sessions s ON s.environment_id=e.id WHERE COALESCE(s.tenant_id,'default')=? ORDER BY e.id DESC LIMIT 50`, store.DefaultTenantID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer func() { _ = rows.Close() }()
	var out [][]string
	for rows.Next() {
		var id int64
		var name, driver, state, hash string
		if err := rows.Scan(&id, &name, &driver, &state, &hash); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		out = append(out, []string{strconv.FormatInt(id, 10), name, driver, state, hash})
	}
	s.consoleTable(w, r, "Environments", []string{"id", "name", "driver", "state", "source_hash"}, out, "")
}

func (s *Server) consoleRunners(w http.ResponseWriter, r *http.Request) {
	if !s.consoleRequire(w, r) {
		return
	}
	rows, err := s.Eng.Store.DB.Query(`SELECT id, name, kind, state FROM runners ORDER BY id`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer func() { _ = rows.Close() }()
	var out [][]string
	for rows.Next() {
		var id int64
		var name, kind, state string
		if err := rows.Scan(&id, &name, &kind, &state); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		out = append(out, []string{strconv.FormatInt(id, 10), name, kind, state})
	}
	s.consoleTable(w, r, "Runners", []string{"id", "name", "kind", "state"}, out, "")
}

func (s *Server) consoleReceipts(w http.ResponseWriter, r *http.Request) {
	if !s.consoleRequire(w, r) {
		return
	}
	rows, err := s.Eng.Store.DB.Query(`SELECT r.job_id, r.kind FROM receipts r JOIN turns t ON t.id=r.job_id JOIN sessions s ON s.id=t.session_id WHERE COALESCE(s.tenant_id,'default')=? ORDER BY r.job_id DESC LIMIT 50`, store.DefaultTenantID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer func() { _ = rows.Close() }()
	var out [][]string
	for rows.Next() {
		var id int64
		var kind string
		if err := rows.Scan(&id, &kind); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		out = append(out, []string{strconv.FormatInt(id, 10), kind})
	}
	s.consoleTable(w, r, "Receipts", []string{"job_id", "kind"}, out, "payloads omitted")
}

func (s *Server) consoleBudgets(w http.ResponseWriter, r *http.Request) {
	if !s.consoleRequire(w, r) {
		return
	}
	var out [][]string
	for slug, p := range s.Eng.PolicySnapshot().Projects {
		if store.NormalizeTenantID(p.Tenant) != store.DefaultTenantID {
			continue
		}
		cap := 0
		if p.Budgets != nil {
			cap = p.Budgets[policy.BudgetMaxConcurrentLeases]
		}
		var n int
		_ = s.Eng.Store.DB.QueryRow(`SELECT COUNT(*) FROM turns t JOIN sessions s ON s.id=t.session_id WHERE s.project=? AND t.state='leased' AND COALESCE(s.tenant_id,'default')=?`, slug, store.DefaultTenantID).Scan(&n)
		limit := "unset (not an enforced estimate)"
		if cap > 0 {
			limit = strconv.Itoa(cap)
		}
		out = append(out, []string{slug, strconv.Itoa(n), limit})
	}
	s.consoleTable(w, r, "Budgets", []string{"project", "leased_turns", "max_concurrent_leases"}, out, "leased_turns is enforced; other spend is not shown")
}

func (s *Server) consoleHealth(w http.ResponseWriter, r *http.Request) {
	if !s.consoleRequire(w, r) {
		return
	}
	hash := ""
	if pol := s.Eng.PolicySnapshot(); pol != nil {
		hash = pol.Hash
	}
	rows := [][]string{
		{"policy_hash", hash},
		{"schema", strconv.Itoa(store.CurrentSchema)},
	}
	s.consoleTable(w, r, "Health", []string{"key", "value"}, rows, "operational secrets are not displayed")
}
