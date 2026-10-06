package server

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/sannrox/rusui/internal/engine"
)

func (s *Server) sessionWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOrWorkerOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	path, secret, err := s.Eng.EnsureSessionWebhook(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, engine.ErrArchived) {
			http.Error(w, "archived", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"url": path, "secret": secret})
}

func (s *Server) sessionWebhookHook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", http.StatusBadRequest)
		return
	}
	deliveryID := r.Header.Get("X-Rusui-Delivery")
	if deliveryID == "" {
		deliveryID = r.Header.Get("X-GitHub-Delivery")
	}
	if deliveryID == "" {
		sum := sha256.Sum256(body)
		deliveryID = hex.EncodeToString(sum[:])
	}
	sig := r.Header.Get("X-Rusui-Signature-256")
	if sig == "" {
		sig = r.Header.Get("X-Hub-Signature-256")
	}
	if err := s.Eng.DeliverSessionWebhook(id, sig, body, deliveryID); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, engine.ErrArchived) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, engine.ErrBadWebhookSignature) {
			http.Error(w, "bad sig", http.StatusUnauthorized)
			return
		}
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
