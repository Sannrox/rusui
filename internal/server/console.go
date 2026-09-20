package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sannrox/rusui/internal/store"
)

const (
	consoleCookie  = "rusui_op"
	consoleFileCap = 256 << 10
	consolePollMs  = 200
)

var consoleTmpl = template.Must(template.New("console").Parse(consoleHTML))

const consoleHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
body{font:16px/1.4 system-ui,sans-serif;max-width:72rem;margin:1rem auto;padding:0 1rem}
:focus{outline:2px solid #222;outline-offset:2px}
.skip{position:absolute;left:-999px} .skip:focus{left:1rem;top:1rem;background:#fff}
pre{white-space:pre-wrap;word-break:break-word;background:#f6f6f6;padding:.75rem}
nav a{margin-right:1rem}
.muted{color:#555}
</style>
</head>
<body>
<a class="skip" href="#main">Skip to content</a>
<nav aria-label="Console">
<a href="/console/sessions">Sessions</a>
<a href="/console/approvals">Approvals</a>
<a href="/console/environments">Environments</a>
<a href="/console/runners">Runners</a>
<a href="/console/receipts">Receipts</a>
<a href="/console/budgets">Budgets</a>
<a href="/console/health">Health</a>
{{if .Authed}}<form method="post" action="/console/logout" style="display:inline"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit">Log out</button></form>{{end}}
</nav>
<main id="main">
{{if eq .View "signin"}}
<h1>Sign in</h1>
<form method="post" action="/console/signin">
<label for="token">Operator token</label>
<input id="token" name="token" type="password" autocomplete="current-password" required>
<button type="submit">Sign in</button>
</form>
{{if .Err}}<p role="alert">{{.Err}}</p>{{end}}
{{else if eq .View "sessions"}}
<h1>Sessions</h1>
{{if not .Sessions}}<p class="muted">No sessions.</p>{{end}}
<ul>
{{range .Sessions}}
<li><a href="/console/sessions/{{.ID}}">#{{.ID}} {{.Project}} {{.Kind}}</a>
 — {{.State}}{{if .TurnState}} / {{.TurnState}}{{end}}{{if .Reason}} — {{.Reason}}{{end}}</li>
{{end}}
</ul>
{{else if eq .View "session"}}
<h1>Session {{.Sess.ID}}</h1>
<p>Project {{.Sess.Project}} · {{.Sess.Kind}} · {{.Sess.State}}{{if .TurnState}} · turn {{.TurnState}}{{end}}</p>
<p>{{.Sess.Prompt}}</p>
<p><a href="/console/sessions/{{.Sess.ID}}/terminal">Terminal</a></p>
<form method="post" action="/console/sessions/{{.Sess.ID}}/preview">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<label for="port">Preview port</label>
<input id="port" name="port" type="number" min="1024" max="65535" required>
<button type="submit">Mint preview grant</button>
</form>
{{if .Reason}}<p>Blocked/failed: {{.Reason}}</p>{{end}}
{{if .Artifact}}<p>Artifact {{.Artifact}}</p>{{end}}
{{if .HistoryUnavailable}}<p role="status">Transcript history unavailable.</p>{{end}}
<h2>Transcript</h2>
<div id="transcript" hx-ext="sse" sse-connect="/console/sessions/{{.Sess.ID}}/events" sse-swap="message">
{{range .Entries}}<article id="e-{{.ID}}"><h3>{{.Kind}}</h3><pre>{{.Body}}</pre></article>{{end}}
</div>
<h2>Files</h2>
{{if .FilesErr}}<p>{{.FilesErr}}</p>{{else if not .Files}}<p class="muted">No files in workspace.</p>{{else}}
<ul>{{range .Files}}<li><a href="/console/sessions/{{$.Sess.ID}}/files?path={{.Enc}}">{{.Name}}</a>
 · <a href="/console/sessions/{{$.Sess.ID}}/files?path={{.Enc}}&amp;view=diff">diff</a></li>{{end}}</ul>
{{end}}
<script>
(function(){
  var el=document.getElementById("transcript");
  if(!el||!el.getAttribute("sse-connect"))return;
  var seen={}; el.querySelectorAll("article").forEach(function(a){seen[a.id]=1});
  function connect(){
    var es=new EventSource(el.getAttribute("sse-connect"));
    es.onmessage=function(ev){
      var t=document.createElement("template"); t.innerHTML=ev.data;
      var n=t.content.firstElementChild; if(!n||seen[n.id])return;
      seen[n.id]=1; el.appendChild(n);
    };
    es.onerror=function(){ es.close(); setTimeout(connect,500); };
  }
  connect();
})();
</script>
{{else if eq .View "file"}}
<h1>{{.FileName}}</h1>
<p><a href="/console/sessions/{{.Sess.ID}}">Back to session</a></p>
{{if .FileState}}<p>{{.FileState}}</p>{{else}}<pre>{{.FileBody}}</pre>{{end}}
{{else if eq .View "approvals"}}
<h1>Approvals</h1>
{{if .Notice}}<p role="status">{{.Notice}}</p>{{end}}
{{if not .Approvals}}<p class="muted">No pending approvals.</p>{{end}}
<ul>
{{range .Approvals}}
<li>
<p><a href="/console/sessions/{{.SessionID}}">session {{.SessionID}}</a> · {{.Repo}}#{{.Item}} · {{.Reason}}</p>
<pre>{{.Body}}</pre>
<p class="muted">scope {{.Scope}} · expiry {{.Expiry}}</p>
<form method="post" action="/console/approvals/{{.ID}}">
<input type="hidden" name="csrf" value="{{$.CSRF}}">
<button name="decision" value="allow" type="submit">Allow</button>
<button name="decision" value="deny" type="submit">Deny</button>
</form>
</li>
{{end}}
</ul>
{{else if eq .View "terminal"}}
<h1>Terminal session {{.Sess.ID}}</h1>
<p>Environment {{.TermHandle}} · {{.TermDriver}} · {{.TermState}}</p>
{{if .Notice}}<p role="status">{{.Notice}}</p>{{end}}
{{if .TermWrite}}<p>Write lease generation {{.TermGen}}</p>{{else}}<p class="muted">Observer (no write lease)</p>{{end}}
<div id="terminal" data-xterm="1" data-output="/console/sessions/{{.Sess.ID}}/terminal/output" data-input="/console/sessions/{{.Sess.ID}}/terminal/input" data-gen="{{.TermGen}}" data-write="{{.TermWrite}}">
<pre id="term-out" aria-live="polite"></pre>
</div>
{{if .TermWrite}}
<form method="post" action="/console/sessions/{{.Sess.ID}}/terminal/input">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="hidden" name="generation" value="{{.TermGen}}">
<label for="cmd">Input</label>
<input id="cmd" name="data" type="text" autocomplete="off">
<button type="submit">Send</button>
</form>
<form method="post" action="/console/sessions/{{.Sess.ID}}/terminal/revoke">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<button type="submit">Release write</button>
</form>
{{else}}
<form method="post" action="/console/sessions/{{.Sess.ID}}/terminal/lease">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<button type="submit">Acquire write</button>
</form>
{{end}}
<script>
(function(){
  var el=document.getElementById("terminal"); if(!el) return;
  var out=document.getElementById("term-out");
  var es=new EventSource(el.getAttribute("data-output"));
  es.onmessage=function(ev){ out.textContent += ev.data; };
  es.onerror=function(){ es.close(); setTimeout(function(){ location.reload(); }, 800); };
})();
</script>
{{else if eq .View "table"}}
<h1>{{.Heading}}</h1>
{{if .Notice}}<p class="muted">{{.Notice}}</p>{{end}}
{{if not .Rows}}<p class="muted">None.</p>{{else}}
<table><thead><tr>{{range .Head}}<th>{{.}}</th>{{end}}</tr></thead>
<tbody>{{range .Rows}}<tr>{{range .}}<td>{{.}}</td>{{end}}</tr>{{end}}</tbody></table>
{{end}}
{{end}}
</main>
</body></html>`

type consolePage struct {
	Title              string
	View               string
	Authed             bool
	CSRF               string
	Err                string
	Sessions           []consoleSess
	Sess               *store.Session
	TurnState          string
	Reason             string
	Artifact           string
	HistoryUnavailable bool
	Entries            []consoleEntry
	Files              []consoleFile
	FilesErr           string
	FileName           string
	FileBody           string
	FileState          string
	Approvals          []consoleApproval
	Notice             string
	Heading            string
	Head               []string
	Rows               [][]string
	TermHandle         string
	TermDriver         string
	TermState          string
	TermWrite          bool
	TermGen            int
}

type consoleApproval struct {
	ID        string
	SessionID int64
	Repo      string
	Item      int
	Reason    string
	Body      string
	Scope     string
	Expiry    string
}

type consoleSess struct {
	ID        int64
	Project   string
	Kind      string
	State     string
	TurnState string
	Reason    string
}

type consoleEntry struct {
	ID   string
	Kind string
	Body string
}

type consoleFile struct {
	Name string
	Enc  string
}

func (s *Server) consoleEnabled() bool {
	return s.OperatorTok != "" && s.OperatorTok != s.WorkerSec
}

func (s *Server) consoleCookieValue() string {
	mac := hmac.New(sha256.New, []byte(s.OperatorTok))
	mac.Write([]byte("console-v1"))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) consoleCSRF() string {
	mac := hmac.New(sha256.New, []byte(s.OperatorTok))
	mac.Write([]byte("csrf-v1"))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

func loopbackReq(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) consoleAuthed(r *http.Request) bool {
	if !s.consoleEnabled() {
		return false
	}
	if s.OperatorBrowserOK(r) {
		return true
	}
	c, err := r.Cookie(consoleCookie)
	if err != nil {
		return false
	}
	want := s.consoleCookieValue()
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(want)) == 1
}

func (s *Server) consoleGate(w http.ResponseWriter, r *http.Request) bool {
	if !s.consoleEnabled() {
		http.NotFound(w, r)
		return false
	}
	if r.TLS == nil && !loopbackReq(r) {
		http.Error(w, "console requires loopback or TLS", http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) consoleSignIn(w http.ResponseWriter, r *http.Request) {
	if !s.consoleGate(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		if s.consoleAuthed(r) {
			http.Redirect(w, r, "/console/sessions", http.StatusSeeOther)
			return
		}
		s.renderConsole(w, consolePage{Title: "Sign in", View: "signin"})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form", 400)
		return
	}
	tok := r.FormValue("token")
	if subtle.ConstantTimeCompare([]byte(tok), []byte(s.OperatorTok)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		s.renderConsole(w, consolePage{Title: "Sign in", View: "signin", Err: "denied"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     consoleCookie,
		Value:    s.consoleCookieValue(),
		Path:     "/console",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
	})
	http.Redirect(w, r, "/console/sessions", http.StatusSeeOther)
}

func (s *Server) consoleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.consoleGate(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil || r.FormValue("csrf") != s.consoleCSRF() {
		http.Error(w, "csrf", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: consoleCookie, Value: "", Path: "/console", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "/console/signin", http.StatusSeeOther)
}

func (s *Server) consoleSessions(w http.ResponseWriter, r *http.Request) {
	if !s.consoleGate(w, r) {
		return
	}
	if !s.consoleAuthed(r) {
		http.Redirect(w, r, "/console/signin", http.StatusSeeOther)
		return
	}
	list, err := store.ListSessions(s.Eng.Store, "", 50)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	out := make([]consoleSess, 0, len(list))
	for _, sess := range list {
		cs := consoleSess{ID: sess.ID, Project: sess.Project, Kind: sess.Kind, State: sess.State}
		turns, _ := store.ListTurnsForSession(s.Eng.Store, sess.ID)
		if len(turns) > 0 {
			cs.TurnState = turns[len(turns)-1].State
			if cs.TurnState == "failed" {
				cs.Reason = "turn failed"
			}
		}
		out = append(out, cs)
	}
	s.renderConsole(w, consolePage{Title: "Sessions", View: "sessions", Authed: true, CSRF: s.consoleCSRF(), Sessions: out})
}

func (s *Server) consoleSession(w http.ResponseWriter, r *http.Request) {
	if !s.consoleGate(w, r) {
		return
	}
	if !s.consoleAuthed(r) {
		http.Redirect(w, r, "/console/signin", http.StatusSeeOther)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", 400)
		return
	}
	page, err := s.sessionPage(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	page.Authed = true
	page.CSRF = s.consoleCSRF()
	s.renderConsole(w, page)
}

func (s *Server) sessionPage(id int64) (consolePage, error) {
	sess, err := store.GetSession(s.Eng.Store, id)
	if err != nil {
		return consolePage{}, err
	}
	page := consolePage{Title: fmt.Sprintf("Session %d", id), View: "session", Sess: sess}
	turns, err := store.ListTurnsForSession(s.Eng.Store, id)
	if err != nil {
		page.HistoryUnavailable = true
	} else if len(turns) > 0 {
		t := turns[len(turns)-1]
		page.TurnState = t.State
		if t.State == "failed" {
			page.Reason = "turn failed"
		}
		page.Artifact = fmt.Sprintf("turn %d rev %d", t.ID, t.ClaimedRevision)
	}
	acts, err := store.ListActionsForSession(s.Eng.Store, id)
	if err != nil {
		page.HistoryUnavailable = true
	} else {
		for _, a := range acts {
			page.Entries = append(page.Entries, consoleEntry{ID: a.ID, Kind: a.Type, Body: a.Body})
			if a.ReasonCode != "" && page.Reason == "" {
				page.Reason = a.ReasonCode
			}
		}
	}
	var n int
	_ = s.Eng.Store.DB.QueryRow(`SELECT COUNT(*) FROM review_revisions WHERE job_id IN (SELECT id FROM turns WHERE session_id=?)`, id).Scan(&n)
	if n > 0 {
		var hash string
		if err := s.Eng.Store.DB.QueryRow(`SELECT item_hash FROM review_revisions WHERE job_id IN (SELECT id FROM turns WHERE session_id=?) ORDER BY id DESC LIMIT 1`, id).Scan(&hash); err == nil {
			page.Artifact = hash
		}
	}
	envRow, err := store.GetEnvironment(s.Eng.Store, sess.EnvironmentID)
	if err != nil || envRow.Handle == "" || envRow.State == store.EnvExpired {
		page.FilesErr = "workspace unavailable or expired"
		return page, nil
	}
	if envRow.Driver != "" && envRow.Driver != "process" {
		page.FilesErr = "file inspection is process-workspace only"
		return page, nil
	}
	entries, err := os.ReadDir(envRow.Handle)
	if err != nil {
		page.FilesErr = "workspace missing"
		return page, nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		page.Files = append(page.Files, consoleFile{Name: name, Enc: template.URLQueryEscaper(name)})
	}
	return page, nil
}

func (s *Server) consoleEvents(w http.ResponseWriter, r *http.Request) {
	if !s.consoleGate(w, r) || !s.consoleAuthed(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", 400)
		return
	}
	if _, err := store.GetSession(s.Eng.Store, id); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	seen := map[string]bool{}
	send := func() {
		acts, err := store.ListActionsForSession(s.Eng.Store, id)
		if err != nil {
			return
		}
		for _, a := range acts {
			if seen[a.ID] {
				continue
			}
			seen[a.ID] = true
			body := template.HTMLEscaper(a.Body)
			kind := template.HTMLEscaper(a.Type)
			aid := template.HTMLEscaper(a.ID)
			_, _ = fmt.Fprintf(w, "data: <article id=\"e-%s\"><h3>%s</h3><pre>%s</pre></article>\n\n", aid, kind, body)
			fl.Flush()
		}
	}
	send()
	tick := time.NewTicker(consolePollMs * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			send()
		}
	}
}

func (s *Server) consoleFile(w http.ResponseWriter, r *http.Request) {
	if !s.consoleGate(w, r) {
		return
	}
	if !s.consoleAuthed(r) {
		http.Redirect(w, r, "/console/signin", http.StatusSeeOther)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id", 400)
		return
	}
	sess, err := store.GetSession(s.Eng.Store, id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	rel := r.URL.Query().Get("path")
	clean, ok := safeRel(rel)
	if !ok {
		http.Error(w, "path", 400)
		return
	}
	page := consolePage{Title: clean, View: "file", Authed: true, CSRF: s.consoleCSRF(), Sess: sess, FileName: clean}
	envRow, err := store.GetEnvironment(s.Eng.Store, sess.EnvironmentID)
	if err != nil || envRow.Handle == "" || envRow.State == store.EnvExpired {
		page.FileState = "missing or expired"
		s.renderConsole(w, page)
		return
	}
	full := filepath.Join(envRow.Handle, clean)
	if !strings.HasPrefix(full, filepath.Clean(envRow.Handle)+string(os.PathSeparator)) && full != filepath.Clean(envRow.Handle) {
		http.Error(w, "path", 400)
		return
	}
	st, err := os.Stat(full)
	if err != nil {
		page.FileState = "missing"
		s.renderConsole(w, page)
		return
	}
	if st.Size() > consoleFileCap {
		page.FileState = "oversized"
		s.renderConsole(w, page)
		return
	}
	b, err := os.ReadFile(full)
	if err != nil {
		page.FileState = "missing"
		s.renderConsole(w, page)
		return
	}
	if bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b) {
		page.FileState = "binary"
		s.renderConsole(w, page)
		return
	}
	if r.URL.Query().Get("view") == "diff" {
		page.FileBody = unifiedFromEmpty(clean, string(b))
	} else {
		page.FileBody = string(b)
	}
	s.renderConsole(w, page)
}

func safeRel(p string) (string, bool) {
	if p == "" || strings.Contains(p, "\\") {
		return "", false
	}
	clean := filepath.Clean("/" + p)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

func unifiedFromEmpty(name, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n", name)
	for line := range strings.SplitSeq(body, "\n") {
		b.WriteString("+")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func (s *Server) renderConsole(w http.ResponseWriter, p consolePage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; form-action 'self'")
	if err := consoleTmpl.Execute(w, p); err != nil {
		_, _ = io.WriteString(w, "template error")
	}
}
