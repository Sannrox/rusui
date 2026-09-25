package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/store"
)

const previewTTL = engine.GrantTTL

func (s *Server) PreviewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.previewProxy)
	return mux
}

func (s *Server) consoleMintPreview(w http.ResponseWriter, r *http.Request) {
	if !s.consoleRequire(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil || r.FormValue("csrf") != s.consoleCSRF() {
		http.Error(w, "csrf", http.StatusForbidden)
		return
	}
	sess, err := store.GetSession(s.Eng.Store, mustID(r))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	envRow, err := store.GetEnvironment(s.Eng.Store, sess.EnvironmentID)
	if err != nil || envRow.Handle == "" || envRow.State != store.EnvReady {
		http.Error(w, "environment not ready", http.StatusConflict)
		return
	}
	port, err := strconv.Atoi(r.FormValue("port"))
	if err != nil || port < 1024 || port > 65535 {
		http.Error(w, "port", 400)
		return
	}
	if s.PreviewBase == "" {
		http.Error(w, "preview origin unset", http.StatusConflict)
		return
	}
	plane := "http://" + r.Host
	if r.TLS != nil {
		plane = "https://" + r.Host
	}
	if !PreviewOriginIsolated(plane, s.PreviewBase) {
		http.Error(w, "preview origin not isolated", http.StatusConflict)
		return
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	tok := hex.EncodeToString(raw)
	g := store.PreviewGrant{
		TokenHash: store.HashPreviewToken(tok), SessionID: sess.ID, EnvironmentID: envRow.ID,
		Handle: envRow.Handle, Port: port, ExpiresAt: time.Now().UTC().Add(previewTTL),
	}
	if err := store.PutPreviewGrant(s.Eng.Store, g); err != nil {
		if errors.Is(err, store.ErrEnvironmentUnavailable) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	u := strings.TrimRight(s.PreviewBase, "/") + "/?g=" + tok
	s.renderConsole(w, consolePage{
		Title: "Preview", View: "table", Authed: true, CSRF: s.consoleCSRF(),
		Heading: "Preview grant", Head: []string{"session", "environment", "port", "url", "expires"},
		Rows: [][]string{{
			strconv.FormatInt(sess.ID, 10), envRow.Handle, strconv.Itoa(port), u, g.ExpiresAt.Format(time.RFC3339),
		}},
		Notice: "grant is a separate secret; it is not the operator cookie",
		Sess:   sess,
	})
}

func (s *Server) previewProxy(w http.ResponseWriter, r *http.Request) {
	if s.consoleAuthed(r) || s.OperatorBrowserOK(r) {
		http.Error(w, "operator cookie not valid on preview origin", http.StatusForbidden)
		return
	}
	tok := r.URL.Query().Get("g")
	if tok == "" {
		tok = bearerToken(r)
	}
	if tok == "" {
		http.Error(w, "grant required", http.StatusUnauthorized)
		return
	}
	g, ok, err := store.GetPreviewGrant(s.Eng.Store, store.HashPreviewToken(tok))
	if err != nil || !ok {
		http.Error(w, "unknown grant", http.StatusUnauthorized)
		return
	}
	if g.Revoked || time.Now().UTC().After(g.ExpiresAt) {
		http.Error(w, "expired", http.StatusForbidden)
		return
	}
	sess, err := store.GetSession(s.Eng.Store, g.SessionID)
	if err != nil {
		http.Error(w, "session gone", http.StatusConflict)
		return
	}
	envRow, err := store.GetEnvironment(s.Eng.Store, sess.EnvironmentID)
	if err != nil || envRow.Handle != g.Handle || envRow.ID != g.EnvironmentID {
		http.Error(w, "environment replaced", http.StatusConflict)
		return
	}
	if envRow.State != store.EnvReady {
		http.Error(w, "unavailable", http.StatusConflict)
		return
	}
	if want, _ := strconv.Atoi(r.URL.Query().Get("port")); want != 0 && want != g.Port {
		http.Error(w, "port not authorized", http.StatusForbidden)
		return
	}
	target, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", g.Port))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "unavailable", http.StatusBadGateway)
	}
	r.Host = target.Host
	proxy.ServeHTTP(w, r)
}

func mustID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}
