package server

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/sannrox/rusui/internal/store"
)

func modelBaseURL(r *http.Request, driver string) string {
	scheme := "http"
	if r.TLS != nil || driver == "container" {
		scheme = "https"
	}
	u := &url.URL{Scheme: scheme, Host: r.Host, Path: "/model-proxy"}
	if driver == "container" {
		host, port, err := net.SplitHostPort(u.Host)
		_ = host
		if err != nil {
			u.Host = "rusui.plane"
		} else {
			u.Host = net.JoinHostPort("rusui.plane", port)
		}
	}
	return u.String()
}

func (s *Server) modelProxy(w http.ResponseWriter, r *http.Request) {
	if !s.requireGuestTLS(w, r) {
		return
	}
	g, ok := s.grantFromRequest(r)
	if !ok || g.Kind != store.GrantTurn {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	if s.ModelKey == "" {
		http.Error(w, "model key unset", http.StatusServiceUnavailable)
		return
	}
	upstream := s.modelOrigin()
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	orig := proxy.Director
	proxy.Director = func(req *http.Request) {
		orig(req)
		req.Host = upstream.Host
		req.Header.Set("Authorization", "Bearer "+s.ModelKey)
		path := strings.TrimPrefix(req.URL.Path, "/model-proxy")
		if path == "" {
			path = "/"
		}
		req.URL.Path = path
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) modelOrigin() *url.URL {
	if s.ModelOrigin != nil {
		return s.ModelOrigin
	}
	u, _ := url.Parse("https://api.x.ai")
	return u
}
