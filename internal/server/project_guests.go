package server

import (
	"encoding/json"
	"net/http"

	"github.com/sannrox/rusui/internal/guest"
)

type projectGuest struct {
	Name string `json:"name"`
	Pin  string `json:"pin"`
}

type projectGuestsResponse struct {
	Default string         `json:"default"`
	Allowed []projectGuest `json:"allowed"`
}

func (s *Server) projectGuests(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOrWorkerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	slug := r.PathValue("slug")
	pol := s.Eng.PolicySnapshot()
	project, ok := pol.Project(slug)
	if !ok || !s.projectAllowed(r, slug) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	out := projectGuestsResponse{Allowed: []projectGuest{}}
	if project.Guests != nil {
		out.Default = project.Guests.Default
		for _, name := range project.Guests.Allowed {
			pin := ""
			if entry, ok := pol.Guests[name]; ok {
				pin = entry.Pin
			}
			out.Allowed = append(out.Allowed, projectGuest{Name: name, Pin: pin})
		}
	} else {
		name := s.Eng.DefaultGuest
		if name == "" {
			name = guest.KindGrok
		}
		pin := ""
		if entry, ok := pol.Guests[name]; ok {
			pin = entry.Pin
		}
		out.Default = name
		out.Allowed = []projectGuest{{Name: name, Pin: pin}}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
