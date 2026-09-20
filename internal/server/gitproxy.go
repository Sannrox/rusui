package server

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

func (s *Server) githubAuthToken() string {
	if s.GitHubTokens != nil {
		if t, err := s.GitHubTokens.Token(); err == nil && t != "" {
			return t
		}
	}
	return s.GitHubToken
}

func gitProxyBaseURL(r *http.Request, driver string) string {
	u, err := url.Parse(modelBaseURL(r, driver))
	if err != nil {
		return ""
	}
	u.Path = "/git-proxy/github.com/"
	u.RawQuery = ""
	return u.String()
}

func parseGitProxyPath(p string) (owner, repo, rest string, err error) {
	p = strings.TrimPrefix(p, "/git-proxy/github.com/")
	i := strings.Index(p, ".git")
	if i < 0 {
		return "", "", "", fmt.Errorf("git-proxy: missing .git")
	}
	head, rest := p[:i], p[i+4:]
	parts := strings.SplitN(head, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", fmt.Errorf("git-proxy: owner/repo required")
	}
	if strings.Contains(parts[0], "..") || strings.Contains(parts[1], "..") || strings.ContainsAny(parts[0]+parts[1], " \t\n") {
		return "", "", "", fmt.Errorf("git-proxy: invalid repo")
	}
	return parts[0], parts[1], rest, nil
}

func splitReceivePack(r io.Reader) (refs []string, body io.Reader, err error) {
	var head bytes.Buffer
	br := bufio.NewReader(r)
	for {
		hdr := make([]byte, 4)
		if _, err := io.ReadFull(br, hdr); err != nil {
			return nil, nil, err
		}
		head.Write(hdr)
		n, err := strconv.ParseUint(string(hdr), 16, 16)
		if err != nil {
			return nil, nil, err
		}
		if n == 0 {
			break
		}
		if n < 4 {
			return nil, nil, fmt.Errorf("pkt-line: short")
		}
		payload := make([]byte, n-4)
		if _, err := io.ReadFull(br, payload); err != nil {
			return nil, nil, err
		}
		head.Write(payload)
		if ref := refFromReceiveCmd(payload); ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs, io.MultiReader(&head, br), nil
}

func (s *Server) gitProxy(w http.ResponseWriter, r *http.Request) {
	if !s.requireGuestTLS(w, r) {
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/git-proxy/github.com/") {
		http.Error(w, "host not allowed", http.StatusForbidden)
		return
	}
	g, ok := s.grantFromRequest(r)
	if !ok {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	if s.githubAuthToken() == "" {
		http.Error(w, "github token unset", http.StatusServiceUnavailable)
		return
	}
	owner, name, rest, err := parseGitProxyPath(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	full := owner + "/" + name
	if !strings.EqualFold(g.Repo, full) {
		http.Error(w, "repo not allowed", http.StatusForbidden)
		return
	}
	svc := r.URL.Query().Get("service")
	isReceive := strings.Contains(rest, "git-receive-pack") || svc == "git-receive-pack"
	if isReceive {
		if !g.CanPush {
			http.Error(w, "ref not allowed", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			refs, body, err := splitReceivePack(r.Body)
			if err != nil {
				http.Error(w, "pkt-line", http.StatusBadRequest)
				return
			}
			for _, ref := range refs {
				if !sessionRefAllowed(g.SessionID, ref) {
					http.Error(w, "ref not allowed", http.StatusForbidden)
					return
				}
			}
			r.Body = io.NopCloser(body)
			r.ContentLength = -1
			r.Header.Del("Content-Length")
		}
	}
	s.forwardGit(w, r, owner, name, rest)
}

func (s *Server) gitUpstream() *url.URL {
	if s.GitOrigin != nil {
		return s.GitOrigin
	}
	u, _ := url.Parse("https://github.com")
	return u
}

func (s *Server) forwardGit(w http.ResponseWriter, r *http.Request, owner, name, rest string) {
	upstream := s.gitUpstream()
	path := "/" + owner + "/" + name + ".git" + rest
	query := r.URL.RawQuery
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	token := s.githubAuthToken()
	orig := proxy.Director
	proxy.Director = func(req *http.Request) {
		orig(req)
		req.URL.Scheme = upstream.Scheme
		req.URL.Host = upstream.Host
		req.Host = upstream.Host
		req.URL.Path = path
		req.URL.RawQuery = query
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		req.Header.Set("Authorization", "Basic "+basic)
	}
	proxy.ServeHTTP(w, r)
}
