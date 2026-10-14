package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/sannrox/rusui/internal/guest"
)

// conformanceTimeout bounds one in-image conformance run.
const conformanceTimeout = 90 * time.Second

// qualifyGuest runs the ACP conformance suite for the posted guest entry on
// the plane and returns the signed receipt to store beside it in policy.
func (s *Server) qualifyGuest(w http.ResponseWriter, r *http.Request) {
	if !s.workerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	var entry guest.Entry
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entry); err != nil {
		http.Error(w, "invalid guest entry", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), conformanceTimeout)
	defer cancel()
	receipt, err := s.Eng.QualifyGuest(ctx, entry)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(receipt)
}
