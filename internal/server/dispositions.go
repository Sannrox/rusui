package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/sannrox/rusui/internal/store"
)

// dispositionNoteCap bounds the free-text note on one disposition.
const dispositionNoteCap = 4 << 10

// recordDisposition stores the operator's judgment of one review result
// (`rusui disposition`). Operator only: the worker never grades its own
// output. Recording grants nothing.
func (s *Server) recordDisposition(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorBrowserOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	var req struct {
		Disposition  string `json:"disposition"`
		WrongFinding bool   `json:"wrong_finding"`
		Note         string `json:"note"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, dispositionNoteCap+1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || len(req.Note) > dispositionNoteCap {
		http.Error(w, "body", http.StatusBadRequest)
		return
	}
	d, err := store.RecordDisposition(s.Eng.Store, store.Disposition{
		ReviewRevisionID: id, Value: req.Disposition, WrongFinding: req.WrongFinding, Note: req.Note,
	})
	switch {
	case errors.Is(err, store.ErrUnknownResult):
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	case errors.Is(err, store.ErrInvalidDisposition):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(d)
}

// commentGate reports trailing shadow-result dispositions against the
// ADR 0038 D6 comment thresholds. Reading it promotes nothing.
func (s *Server) commentGate(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorBrowserOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	g, err := store.CommentGateReport(s.Eng.Store)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(g)
}
