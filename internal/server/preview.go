package server

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
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

// previewHTMLCap is how much of a text/html response the proxy will buffer
// to attach comment chrome. Larger pages pass through unchanged.
const previewHTMLCap = 2 << 20

var previewCommentChromeTmpl = template.Must(template.New("preview-comment").Parse(`<iframe name="rusui-preview-comment-out" hidden title="Comment result"></iframe>
<form id="rusui-preview-comment" method="post" action="{{.Action}}" target="rusui-preview-comment-out" style="position:fixed;bottom:0;left:0;right:0;z-index:2147483647;margin:0;padding:.5rem;background:#000;color:#fff;font:14px/1.3 system-ui,sans-serif;display:flex;gap:.5rem;align-items:end">
<input type="hidden" name="url" value="{{.URL}}">
<label>Comment <textarea name="text" required rows="2"></textarea></label>
<label>selector <input name="selector" placeholder="optional"></label>
<button type="submit">Comment</button>
</form>
`))

func (s *Server) PreviewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /comment", s.previewComment)
	mux.HandleFunc("/", s.previewProxy)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(&previewHeaderWriter{ResponseWriter: w}, r)
	})
}

// previewHeaderWriter stamps crawl-refusal headers on every preview-origin
// response, including reverse-proxied bodies (ADR 0096 D2).
type previewHeaderWriter struct {
	http.ResponseWriter
	wrote bool
}

func (w *previewHeaderWriter) WriteHeader(code int) {
	w.mark()
	w.ResponseWriter.WriteHeader(code)
}

func (w *previewHeaderWriter) Write(b []byte) (int, error) {
	w.mark()
	return w.ResponseWriter.Write(b)
}

func (w *previewHeaderWriter) Flush() {
	w.mark()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *previewHeaderWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *previewHeaderWriter) mark() {
	if w.wrote {
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.wrote = true
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
	sess, err := s.visibleOwnedSession(r, mustID(r))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	port, err := strconv.Atoi(r.FormValue("port"))
	if err != nil {
		http.Error(w, "port", 400)
		return
	}
	u, envRow, expires, ok := s.createPreviewGrant(w, r, sess, previewMint{Port: port})
	if !ok {
		return
	}
	s.renderConsole(w, consolePage{
		Title: "Preview", View: "table", Authed: true, CSRF: s.consoleCSRF(),
		Heading: "Preview grant", Head: []string{"session", "environment", "port", "url", "expires"},
		Rows: [][]string{{
			strconv.FormatInt(sess.ID, 10), envRow.Handle, strconv.Itoa(port), u, expires.Format(time.RFC3339),
		}},
		Notice: "grant is a separate secret; it is not the operator cookie",
		Sess:   sess,
	})
}

// mintPreview is the operator HTTP mint (`rusui preview`). It uses the
// static operator token, not OIDC (ADR 0074).
func (s *Server) mintPreview(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorBrowserOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	sess, err := s.visibleOwnedSession(r, mustID(r))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var req struct {
		Port    int    `json:"port"`
		Service string `json:"service"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		http.Error(w, "port", http.StatusBadRequest)
		return
	}
	if req.Service != "" && req.Port != 0 {
		http.Error(w, "service and port are mutually exclusive", http.StatusBadRequest)
		return
	}
	u, _, _, ok := s.createPreviewGrant(w, r, sess, previewMint{Port: req.Port, Service: req.Service})
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"url": u})
}

func (s *Server) mintShare(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorBrowserOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	sess, err := s.visibleOwnedSession(r, mustID(r))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var req struct {
		Port    int    `json:"port"`
		Service string `json:"service"`
		TTL     string `json:"ttl"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		http.Error(w, "body", http.StatusBadRequest)
		return
	}
	if req.Port != 0 {
		http.Error(w, "raw-port mint is operator-grant-only", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Service) == "" {
		http.Error(w, "service", http.StatusBadRequest)
		return
	}
	ttl, ok := store.ParseShareTTL(req.TTL)
	if !ok {
		http.Error(w, "ttl", http.StatusBadRequest)
		return
	}
	u, _, _, ok := s.createPreviewGrant(w, r, sess, previewMint{
		Service: req.Service, Class: store.PreviewClassShare, TTL: ttl,
	})
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"url": u})
}

func (s *Server) listShare(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorBrowserOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	sess, err := s.visibleOwnedSession(r, mustID(r))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := s.FenceOperatorToken(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	list, err := store.ListShareGrants(s.Eng.Store, sess.ID, s.nowUTC())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, g := range list {
		out = append(out, map[string]any{
			"token_hash": g.TokenHash,
			"service":    g.ServiceName,
			"port":       g.Port,
			"expires_at": g.ExpiresAt.UTC().Format(time.RFC3339Nano),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) revokeShare(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorBrowserOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	sess, err := s.visibleOwnedSession(r, mustID(r))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	hash := strings.TrimSpace(r.PathValue("hash"))
	if hash == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := store.RevokeShareGrant(s.Eng.Store, sess.ID, hash); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type previewMint struct {
	Port    int
	Service string
	Class   string
	TTL     time.Duration
}

// createPreviewGrant wakes the session environment and stores one preview
// grant. On refusal it writes the response and returns ok=false.
// A non-empty service name resolves the declared port and requires health
// at mint. A raw port records no service name and is operator-grant-only.
func (s *Server) createPreviewGrant(w http.ResponseWriter, r *http.Request, sess *store.Session, mint previewMint) (string, *store.Environment, time.Time, bool) {
	class := mint.Class
	if class == "" {
		class = store.PreviewClassOperator
	}
	if class == store.PreviewClassShare {
		proj, ok := s.Eng.PolicySnapshot().Project(sess.Project)
		if !ok || !proj.Share {
			http.Error(w, "sharing disabled", http.StatusConflict)
			return "", nil, time.Time{}, false
		}
		if strings.TrimSpace(mint.Service) == "" || mint.Port != 0 {
			http.Error(w, "service", http.StatusBadRequest)
			return "", nil, time.Time{}, false
		}
	}
	envRow, ok := s.wakeForOperator(w, sess.ID, "operator preview")
	if !ok {
		return "", nil, time.Time{}, false
	}
	if envRow.Handle == "" || envRow.State != store.EnvReady {
		http.Error(w, "environment not ready", http.StatusConflict)
		return "", nil, time.Time{}, false
	}
	service := strings.TrimSpace(mint.Service)
	port := mint.Port
	if service != "" {
		resolved, ok := s.resolvePreviewService(w, envRow, service)
		if !ok {
			return "", nil, time.Time{}, false
		}
		port = resolved.Port
	} else if port < 1024 || port > 65535 {
		http.Error(w, "port", 400)
		return "", nil, time.Time{}, false
	}
	if _, err := s.previewDial(envRow, port); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return "", nil, time.Time{}, false
	}
	if s.PreviewBase == "" {
		http.Error(w, "preview origin unset", http.StatusConflict)
		return "", nil, time.Time{}, false
	}
	plane := "http://" + r.Host
	if r.TLS != nil {
		plane = "https://" + r.Host
	}
	if !PreviewOriginIsolated(plane, s.PreviewBase) {
		http.Error(w, "preview origin not isolated", http.StatusConflict)
		return "", nil, time.Time{}, false
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		http.Error(w, err.Error(), 500)
		return "", nil, time.Time{}, false
	}
	tok := hex.EncodeToString(raw)
	ttl := mint.TTL
	if ttl <= 0 {
		ttl = previewTTL
	}
	exp := s.nowUTC().Add(ttl)
	if class == store.PreviewClassShare && envRow.ExpiresAt != nil && envRow.ExpiresAt.Before(exp) {
		exp = envRow.ExpiresAt.UTC()
	}
	var gen int
	if class == store.PreviewClassShare {
		var err error
		gen, err = s.operatorGeneration()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return "", nil, time.Time{}, false
		}
	}
	g := store.PreviewGrant{
		TokenHash: store.HashPreviewToken(tok), SessionID: sess.ID, EnvironmentID: envRow.ID,
		Handle: envRow.Handle, Port: port, ServiceName: service, ExpiresAt: exp,
		Class: class, OperatorGeneration: gen,
	}
	if err := store.PutPreviewGrant(s.Eng.Store, g); err != nil {
		if errors.Is(err, store.ErrEnvironmentUnavailable) {
			http.Error(w, err.Error(), http.StatusConflict)
			return "", nil, time.Time{}, false
		}
		http.Error(w, err.Error(), 500)
		return "", nil, time.Time{}, false
	}
	receipt := map[string]any{
		"session_id": sess.ID,
		"port":       port,
		"expires_at": g.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	if service != "" {
		receipt["service"] = service
	}
	action := actionPreviewMint
	limit := "The operator minted a preview grant for this session."
	if class == store.PreviewClassShare {
		action = actionPreviewShare
		limit = "The operator minted a share grant for this session."
		receipt["class"] = store.PreviewClassShare
	}
	if err := recordOperatorEnvAction(s.Eng.Store, sess, action, limit, receipt); err != nil {
		http.Error(w, err.Error(), 500)
		return "", nil, time.Time{}, false
	}
	u := strings.TrimRight(s.PreviewBase, "/") + "/?g=" + tok
	return u, envRow, g.ExpiresAt, true
}

// previewComment records a preview.comment action (url, selector, text;
// never the grant token) and turns the same fields into a session
// follow-up. It fails closed on origin: a POST must carry the preview
// origin, and nothing posts while PreviewBase is unset.
func (s *Server) previewComment(w http.ResponseWriter, r *http.Request) {
	if s.PreviewBase == "" || !previewOriginOK(r.Header.Get("Origin"), s.PreviewBase) {
		http.Error(w, "origin", http.StatusForbidden)
		return
	}
	g, envRow, ok := s.previewGrant(w, r)
	if !ok {
		return
	}
	if g.Class == store.PreviewClassShare {
		http.Error(w, "share grant is read-only", http.StatusForbidden)
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
	sess, err := store.GetSession(s.Eng.Store, g.SessionID)
	if err != nil {
		http.Error(w, "session gone", http.StatusConflict)
		return
	}
	if err := recordOperatorEnvAction(s.Eng.Store, sess, actionPreviewComment,
		"The operator posted a preview comment for this session.",
		map[string]string{"url": page, "selector": sel, "text": text}); err != nil {
		http.Error(w, err.Error(), 500)
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
	if g.Revoked || s.nowUTC().After(g.ExpiresAt) {
		http.Error(w, "expired", http.StatusForbidden)
		return nil, nil, false
	}
	if g.Class == store.PreviewClassShare {
		gen, err := s.operatorGeneration()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil, nil, false
		}
		if g.OperatorGeneration != gen {
			http.Error(w, "expired", http.StatusForbidden)
			return nil, nil, false
		}
	}
	sess, err := store.GetSession(s.Eng.Store, g.SessionID)
	if err != nil {
		http.Error(w, "session gone", http.StatusConflict)
		return nil, nil, false
	}
	if g.Class == store.PreviewClassShare {
		cancelled, err := store.SessionCancelled(s.Eng.Store, sess.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil, nil, false
		}
		if cancelled {
			http.Error(w, "session cancelled", http.StatusConflict)
			return nil, nil, false
		}
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
	// Drop Accept-Encoding so the proxy transport can decode gzip and
	// the plane can attach comment chrome to the HTML.
	r.Header.Del("Accept-Encoding")
	page := r.URL.RequestURI()
	if page == "" {
		page = "/"
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "unavailable", http.StatusBadGateway)
	}
	proxy.ModifyResponse = func(res *http.Response) error {
		if g.Class == store.PreviewClassShare {
			return nil
		}
		return injectPreviewCommentChrome(res, grantTok, page)
	}
	r.Host = target.Host
	proxy.ServeHTTP(w, r)
}

// injectPreviewCommentChrome appends plane-owned comment chrome to a
// text/html preview response. Non-HTML, compressed, attachment, and
// oversized bodies are left unchanged. The form POSTs /comment with the
// grant query; it does not use the operator cookie.
func injectPreviewCommentChrome(res *http.Response, grant, page string) error {
	if grant == "" || res == nil || res.Body == nil {
		return nil
	}
	if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusNotModified {
		return nil
	}
	if strings.Contains(strings.ToLower(res.Header.Get("Content-Disposition")), "attachment") {
		return nil
	}
	enc := strings.ToLower(strings.TrimSpace(res.Header.Get("Content-Encoding")))
	if enc != "" && enc != "identity" {
		return nil
	}
	media, params, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(media, "text/html") {
		return nil
	}
	if cs := params["charset"]; cs != "" && !strings.EqualFold(cs, "utf-8") {
		return nil
	}
	if res.ContentLength > previewHTMLCap {
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, previewHTMLCap+1))
	if err != nil {
		_ = res.Body.Close()
		return err
	}
	if len(body) > previewHTMLCap {
		res.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), res.Body), res.Body}
		return nil
	}
	if err := res.Body.Close(); err != nil {
		return err
	}

	snippet, err := renderPreviewCommentChrome(grant, page)
	if err != nil {
		res.Body = io.NopCloser(bytes.NewReader(body))
		return nil
	}
	out := insertBeforeCloseBody(body, snippet)
	res.Body = io.NopCloser(bytes.NewReader(out))
	res.ContentLength = int64(len(out))
	res.Header.Set("Content-Length", strconv.Itoa(len(out)))
	res.Header.Del("Transfer-Encoding")
	return nil
}

func renderPreviewCommentChrome(grant, page string) ([]byte, error) {
	if page == "" {
		page = "/"
	}
	var b bytes.Buffer
	err := previewCommentChromeTmpl.Execute(&b, struct {
		Action template.URL
		URL    string
	}{
		Action: template.URL("/comment?" + url.Values{"g": {grant}}.Encode()),
		URL:    page,
	})
	if err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func insertBeforeCloseBody(html, snippet []byte) []byte {
	lower := bytes.ToLower(html)
	for _, tag := range [][]byte{[]byte("</body>"), []byte("</html>")} {
		if i := bytes.LastIndex(lower, tag); i >= 0 {
			out := make([]byte, 0, len(html)+len(snippet))
			out = append(out, html[:i]...)
			out = append(out, snippet...)
			out = append(out, html[i:]...)
			return out
		}
	}
	out := make([]byte, 0, len(html)+len(snippet))
	out = append(out, html...)
	return append(out, snippet...)
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

func (s *Server) resolvePreviewService(w http.ResponseWriter, envRow *store.Environment, name string) (env.Service, bool) {
	d := s.sessionEnvDriver(envRow)
	if d == nil {
		http.Error(w, "environment driver unavailable", http.StatusConflict)
		return env.Service{}, false
	}
	svcs, err := env.WorkspaceServices(d, envRow.Handle)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return env.Service{}, false
	}
	svc, ok := env.ServiceNamed(svcs, name)
	if !ok {
		http.Error(w, "service not declared", http.StatusConflict)
		return env.Service{}, false
	}
	if svc.Port == 0 {
		http.Error(w, "service has no port", http.StatusConflict)
		return env.Service{}, false
	}
	if env.ProbeService(d, envRow.Handle, svc) != env.ServiceHealthy {
		http.Error(w, "service not healthy", http.StatusConflict)
		return env.Service{}, false
	}
	return svc, true
}

func (s *Server) sessionEnvDriver(envRow *store.Environment) env.Driver {
	if envRow.Driver == env.KindContainer {
		return s.Eng.Container
	}
	return s.Eng.Env
}

func (s *Server) previewDial(envRow *store.Environment, port int) (string, error) {
	d := s.sessionEnvDriver(envRow)
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

func (s *Server) nowUTC() time.Time {
	if s != nil && s.Eng != nil && s.Eng.Clock != nil {
		return s.Eng.Clock.Now().UTC()
	}
	return time.Now().UTC()
}

// FenceOperatorToken binds the current operator token to a generation so
// rotation ends outstanding share grants before TTL.
func (s *Server) FenceOperatorToken() error {
	_, err := s.operatorGeneration()
	return err
}

func (s *Server) operatorGeneration() (int, error) {
	if s == nil || s.Eng == nil || s.Eng.Store == nil || s.OperatorTok == "" {
		return 0, nil
	}
	return store.EnsureOperatorGeneration(s.Eng.Store, store.HashOperatorToken(s.OperatorTok))
}
