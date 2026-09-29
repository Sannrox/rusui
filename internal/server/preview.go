package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

const previewTTL = engine.GrantTTL

const previewCommentCap = 8 << 10

func (s *Server) PreviewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /comment", s.previewComment)
	mux.HandleFunc("/", s.previewProxy)
	return mux
}

// PreviewListener is the bind address and mux for the isolated preview
// origin. Empty PreviewBase returns "", nil, nil (mint already refuses).
// A non-loopback host, missing port, or origin that is not isolated from
// the plane fails closed.
func (s *Server) PreviewListener() (string, http.Handler, error) {
	if strings.TrimSpace(s.PreviewBase) == "" {
		return "", nil, nil
	}
	listen, err := previewListenAddr(s.PreviewBase)
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(s.Addr) == "" || listen == s.Addr {
		return "", nil, fmt.Errorf("preview origin not isolated")
	}
	if err := s.denyPlaneDest(listen); err != nil {
		return "", nil, fmt.Errorf("preview origin not isolated")
	}
	plane := "http://" + s.Addr
	if s.GuestHTTPSOnly {
		plane = "https://" + s.Addr
	}
	if !PreviewOriginIsolated(plane, s.PreviewBase) {
		return "", nil, fmt.Errorf("preview origin not isolated")
	}
	return listen, s.PreviewHandler(), nil
}

func previewListenAddr(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return "", fmt.Errorf("preview origin unset")
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host == "" || port == "" {
		return "", fmt.Errorf("preview origin requires host:port")
	}
	if !planeLoopback(host) {
		return "", fmt.Errorf("preview origin must be loopback")
	}
	return net.JoinHostPort(host, port), nil
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
	envRow, ok := s.wakeForOperator(w, sess.ID, "operator preview")
	if !ok {
		return
	}
	if envRow.Handle == "" || envRow.State != store.EnvReady {
		http.Error(w, "environment not ready", http.StatusConflict)
		return
	}
	port, err := strconv.Atoi(r.FormValue("port"))
	if err != nil || port < 1024 || port > 65535 {
		http.Error(w, "port", 400)
		return
	}
	if _, err := s.previewDial(envRow, port); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
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

// previewComment turns a preview-page comment into a session follow-up.
// It fails closed on origin: a POST must carry the preview origin, and
// nothing posts while PreviewBase is unset.
func (s *Server) previewComment(w http.ResponseWriter, r *http.Request) {
	if s.PreviewBase == "" || !previewOriginOK(r.Header.Get("Origin"), s.PreviewBase) {
		http.Error(w, "origin", http.StatusForbidden)
		return
	}
	g, envRow, ok := s.previewGrant(w, r)
	if !ok {
		return
	}
	// Sleep keeps the grant's binding; a follow-up resumes the session.
	if envRow.State != store.EnvReady && envRow.State != store.EnvSleeping {
		http.Error(w, "unavailable", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, previewCommentCap+1)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "oversized", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(r.FormValue("text"))
	page := strings.TrimSpace(r.FormValue("url"))
	sel := strings.TrimSpace(r.FormValue("selector"))
	if text == "" && page == "" {
		http.Error(w, "text", http.StatusBadRequest)
		return
	}
	var b strings.Builder
	b.WriteString("preview comment")
	if page != "" {
		b.WriteString("\nurl: ")
		b.WriteString(page)
	}
	if sel != "" {
		b.WriteString("\nselector: ")
		b.WriteString(sel)
	}
	if text != "" {
		b.WriteByte('\n')
		b.WriteString(text)
	}
	if b.Len() > previewCommentCap {
		http.Error(w, "oversized", http.StatusBadRequest)
		return
	}
	if _, _, err := s.Eng.PromptFollowUp(g.SessionID, b.String()); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func previewOriginOK(got, base string) bool {
	g, err1 := url.Parse(got)
	b, err2 := url.Parse(base)
	if err1 != nil || err2 != nil || g.Host == "" || b.Host == "" {
		return false
	}
	return strings.EqualFold(g.Scheme, b.Scheme) && strings.EqualFold(g.Host, b.Host)
}

// previewGrant resolves the request's preview grant and the environment it
// is bound to. Every preview endpoint goes through it, so a grant acts
// only while its session still holds the environment id and handle it
// was minted for; expiry clears the handle and replacement changes the id.
// On refusal it writes the response and returns false.
func (s *Server) previewGrant(w http.ResponseWriter, r *http.Request) (*store.PreviewGrant, *store.Environment, bool) {
	if s.consoleAuthed(r) || s.OperatorBrowserOK(r) {
		http.Error(w, "operator cookie not valid on preview origin", http.StatusForbidden)
		return nil, nil, false
	}
	tok := r.URL.Query().Get("g")
	if tok == "" {
		tok = bearerToken(r)
	}
	if tok == "" {
		http.Error(w, "grant required", http.StatusUnauthorized)
		return nil, nil, false
	}
	g, ok, err := store.GetPreviewGrant(s.Eng.Store, store.HashPreviewToken(tok))
	if err != nil || !ok {
		http.Error(w, "unknown grant", http.StatusUnauthorized)
		return nil, nil, false
	}
	if g.Revoked || time.Now().UTC().After(g.ExpiresAt) {
		http.Error(w, "expired", http.StatusForbidden)
		return nil, nil, false
	}
	sess, err := store.GetSession(s.Eng.Store, g.SessionID)
	if err != nil {
		http.Error(w, "session gone", http.StatusConflict)
		return nil, nil, false
	}
	envRow, err := store.GetEnvironment(s.Eng.Store, sess.EnvironmentID)
	if err != nil || envRow.Handle != g.Handle || envRow.ID != g.EnvironmentID {
		http.Error(w, "environment replaced", http.StatusConflict)
		return nil, nil, false
	}
	return g, envRow, true
}

func (s *Server) previewProxy(w http.ResponseWriter, r *http.Request) {
	g, envRow, ok := s.previewGrant(w, r)
	if !ok {
		return
	}
	// Sleep keeps the bound id and handle, so a live grant wakes the same
	// environment rather than needing a new mint.
	if envRow.State == store.EnvSleeping {
		var ok bool
		if envRow, ok = s.wakeForOperator(w, g.SessionID, "preview grant"); !ok {
			return
		}
		if envRow.Handle != g.Handle || envRow.ID != g.EnvironmentID {
			http.Error(w, "environment replaced", http.StatusConflict)
			return
		}
	}
	if envRow.State != store.EnvReady {
		http.Error(w, "unavailable", http.StatusConflict)
		return
	}
	if want, _ := strconv.Atoi(r.URL.Query().Get("port")); want != 0 && want != g.Port {
		http.Error(w, "port not authorized", http.StatusForbidden)
		return
	}
	dial, err := s.previewDial(envRow, g.Port)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	target, err := url.Parse("http://" + dial)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	grantTok := r.URL.Query().Get("g")
	if grantTok == "" {
		grantTok = bearerToken(r)
	}
	q := r.URL.Query()
	q.Del("g")
	r.URL.RawQuery = q.Encode()
	if grantTok != "" && bearerToken(r) == grantTok {
		r.Header.Del("Authorization")
	}
	scrubRefererGrant(r.Header)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "unavailable", http.StatusBadGateway)
	}
	r.Host = target.Host
	proxy.ServeHTTP(w, r)
}

// scrubRefererGrant removes the grant query from a browser Referer, which
// carries the minted `?g=` document URL onto every follow-up request. An
// unparsable Referer is dropped rather than forwarded unchecked.
func scrubRefererGrant(h http.Header) {
	ref := h.Get("Referer")
	if ref == "" {
		return
	}
	u, err := url.Parse(ref)
	if err != nil {
		h.Del("Referer")
		return
	}
	q := u.Query()
	if !q.Has("g") {
		return
	}
	q.Del("g")
	u.RawQuery = q.Encode()
	h.Set("Referer", u.String())
}

func (s *Server) previewDial(envRow *store.Environment, port int) (string, error) {
	var d env.Driver
	if envRow.Driver == env.KindContainer {
		d = s.Eng.Container
	} else {
		d = s.Eng.Env
	}
	pf, ok := d.(env.PortForwarder)
	if !ok || d == nil {
		return "", fmt.Errorf("port forward unavailable")
	}
	dial, err := pf.PortForward(envRow.Handle, port)
	if err != nil {
		return "", err
	}
	if err := s.denyPlaneDest(dial); err != nil {
		return "", err
	}
	return dial, nil
}

func (s *Server) denyPlaneDest(dial string) error {
	if s == nil || s.Addr == "" {
		return fmt.Errorf("plane listener not a preview target")
	}
	wantHost, wantPort, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return fmt.Errorf("plane listener not a preview target")
	}
	gotHost, gotPort, err := net.SplitHostPort(dial)
	if err != nil {
		return err
	}
	if gotPort != wantPort {
		return nil
	}
	if planeUnspecified(wantHost) && (planeLoopback(gotHost) || planeUnspecified(gotHost)) {
		return fmt.Errorf("plane listener not a preview target")
	}
	if planeLoopback(wantHost) && planeLoopback(gotHost) {
		return fmt.Errorf("plane listener not a preview target")
	}
	if wantHost != "" && strings.EqualFold(gotHost, wantHost) {
		return fmt.Errorf("plane listener not a preview target")
	}
	return nil
}

func planeLoopback(host string) bool {
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func planeUnspecified(host string) bool {
	if host == "" || host == "*" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

func mustID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}
