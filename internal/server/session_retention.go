package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func (s *Server) putSessionRetention(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorAPIOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	if _, err := s.visibleOwnedSession(r, id); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var req struct {
		Class string `json:"class"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		http.Error(w, "body", http.StatusBadRequest)
		return
	}
	class := store.NormalizeRetention(req.Class)
	if !store.ValidRetention(class) {
		http.Error(w, store.ErrInvalidRetention.Error(), http.StatusBadRequest)
		return
	}
	actor := s.operatorActor(s.identify(r))
	now := s.Eng.Clock.Now().UTC()
	err = s.Eng.Store.Tx(func(tx *sql.Tx) error {
		var exp *time.Time
		if class == store.RetentionShort {
			t := store.DefaultRetentionExpiry(now)
			exp = &t
		}
		if err := store.SetSessionRetentionTx(tx, id, class, exp); err != nil {
			return err
		}
		body, err := json.Marshal(map[string]any{"class": class, "actor": actor})
		if err != nil {
			return err
		}
		return store.InsertActionTx(tx, store.Action{
			ID: fmt.Sprintf("retention-class-%d-%s-%d", id, class, now.UnixNano()), SessionID: &id,
			Type: "retention.class", ReasonCode: "recorded", EvidenceClass: "plane_observed",
			LimitSentence: "Session retention class was changed.",
			Body:          string(body),
		})
	})
	if errors.Is(err, store.ErrInvalidRetention) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, store.ErrRetentionExpired) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sess, err := store.GetSession(s.Eng.Store, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sess)
}

func (s *Server) postSessionWriteOverride(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorAPIOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	if _, err := s.visibleOwnedSession(r, id); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		http.Error(w, "body", http.StatusBadRequest)
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		http.Error(w, "reason", http.StatusBadRequest)
		return
	}
	actor := s.operatorActor(s.identify(r))
	now := s.Eng.Clock.Now().UTC()
	err = s.Eng.Store.Tx(func(tx *sql.Tx) error {
		if err := store.SetRetentionWriteOverrideTx(tx, id, actor, now); err != nil {
			return err
		}
		body, err := json.Marshal(map[string]any{"reason": reason, "actor": actor})
		if err != nil {
			return err
		}
		return store.InsertActionTx(tx, store.Action{
			ID: fmt.Sprintf("retention-write-override-%d-%d", id, now.UnixNano()), SessionID: &id,
			Type: "retention.write_override", ReasonCode: "recorded", EvidenceClass: "plane_observed",
			LimitSentence: "An audited override allowed write-capable tools on an indefinite session.",
			Body:          string(body),
		})
	})
	switch {
	case errors.Is(err, store.ErrRetentionNotIndefinite):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, store.ErrWriteOverrideExists), errors.Is(err, store.ErrRetentionExpired):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sess, err := store.GetSession(s.Eng.Store, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(sess)
}

func (s *Server) postSessionRetentionDelete(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorAPIOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	if _, err := s.visibleOwnedSession(r, id); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := s.Eng.DeleteSessionRetention(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
