package server

import (
	"encoding/json"
	"net/http"

	"github.com/sannrox/rusui/internal/store"
)

// inboxRepo is the review budget and open work of one repository in the
// inbox (ADR 0038 D5: exhaustion stays visible).
type inboxRepo struct {
	Repo            string `json:"repo"`
	ReviewsToday    int    `json:"reviews_today"`
	ReviewCap       int    `json:"review_cap,omitempty"`
	BudgetExhausted bool   `json:"budget_exhausted"`
	Actionable      int    `json:"actionable"`
}

type inboxResponse struct {
	Items []store.InboxItem `json:"items"`
	Repos []inboxRepo       `json:"repos"`
}

// inbox lists the latest review result per item for the caller's tenant
// (`rusui inbox`). By default only items that still need a disposition;
// `?all=1` lists every item. Read-only: it grants and approves nothing.
func (s *Server) inbox(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorAPIOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	tenant, ok := s.operatorTenant(r)
	if !ok {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	all := false
	switch r.URL.Query().Get("all") {
	case "", "0":
	case "1":
		all = true
	default:
		http.Error(w, "all must be 0 or 1", http.StatusBadRequest)
		return
	}
	items, err := store.InboxItems(s.Eng.Store, tenant)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	day := s.Eng.Clock.Now().UTC().Format("2006-01-02")
	pol := s.Eng.PolicySnapshot()
	resp := inboxResponse{Items: []store.InboxItem{}, Repos: []inboxRepo{}}
	byRepo := map[string]int{}
	for _, it := range items {
		i, seen := byRepo[it.Repo]
		if !seen {
			n, err := store.ReviewsOnDay(s.Eng.Store, it.Repo, day)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			rep := inboxRepo{Repo: it.Repo, ReviewsToday: n}
			if p, ok := pol.Repo(it.Repo); ok {
				rep.ReviewCap = p.MaxReviewsPerRepoPerUTCDay
				rep.BudgetExhausted = n >= rep.ReviewCap
			}
			i = len(resp.Repos)
			byRepo[it.Repo] = i
			resp.Repos = append(resp.Repos, rep)
		}
		if it.Actionable() {
			resp.Repos[i].Actionable++
		}
		if all || it.Actionable() {
			resp.Items = append(resp.Items, it)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
