package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

// Inbound hook body caps. The hooks are reachable before any credential is
// checked, and checking a signature means reading the body, so the cap is
// what bounds the memory an unauthenticated caller can make the plane use.
const (
	// githubHookBodyCap is GitHub's documented webhook payload cap. A lower
	// cap would refuse real deliveries for large pushes and pull requests.
	githubHookBodyCap = 25 << 20
	// hookBodyCap covers /hooks/events, session webhooks, and Slack. Their
	// payloads are ids, small sender events, and slash-command forms.
	hookBodyCap = 1 << 20
)

// readHookBody reads at most limit bytes of the request body for a hook.
// A larger body writes 413 and any other read failure writes 400; both
// return ok=false before the caller verifies or parses anything.
func readHookBody(w http.ResponseWriter, r *http.Request, limit int64) (body []byte, ok bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
			return nil, false
		}
		http.Error(w, "bad body", http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

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
	if _, err := s.visibleOwnedSession(r, id); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	path, secret, err := s.Eng.EnsureSessionWebhook(id)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
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
	body, ok := readHookBody(w, r, hookBodyCap)
	if !ok {
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
		if errors.Is(err, store.ErrNoRows) || errors.Is(err, engine.ErrArchived) {
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
