package server

import (
	"fmt"
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

// Model providers (ADR 0017 D2). The guest picks the provider; the
// operator picks the upstream and credential.
const (
	ProviderXAI       = "xai"
	ProviderAnthropic = "anthropic"
)

var defaultModelOrigins = map[string]string{
	ProviderXAI:       "https://api.x.ai",
	ProviderAnthropic: "https://api.anthropic.com",
}

// ModelConfig is the plane side of the model proxy: which provider the
// guest speaks, the upstream to forward to, and the key sent upstream.
type ModelConfig struct {
	Guest    string
	Provider string
	Key      string
	Origin   *url.URL // operator upstream (gateway or CLI proxy); nil uses the provider default
}

// ModelConfigFromEnv reads RUSUI_GUEST, RUSUI_MODEL_UPSTREAM, and the
// provider key. An unknown guest or a malformed upstream is an error.
func ModelConfigFromEnv(getenv func(string) string) (ModelConfig, error) {
	var c ModelConfig
	switch g := getenv("RUSUI_GUEST"); g {
	case "", "grok":
		c.Guest, c.Provider = "grok", ProviderXAI
		c.Key = firstNonEmpty(getenv("XAI_API_KEY"), getenv("RUSUI_XAI_API_KEY"))
	case "claude":
		c.Guest, c.Provider = "claude", ProviderAnthropic
		c.Key = firstNonEmpty(getenv("RUSUI_ANTHROPIC_API_KEY"), getenv("ANTHROPIC_API_KEY"))
	default:
		return c, fmt.Errorf("RUSUI_GUEST %q: want grok or claude", g)
	}
	if raw := getenv("RUSUI_MODEL_UPSTREAM"); raw != "" {
		// The URL may carry a gateway credential; errors end up in service
		// logs, so they never echo it.
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return c, fmt.Errorf("RUSUI_MODEL_UPSTREAM: want an http(s) URL with a host")
		}
		c.Origin = u
	}
	return c, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
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
	// A key alone uses the provider's API; an operator upstream (gateway or
	// CLI proxy) may run without one. Neither fails closed.
	if s.ModelKey == "" && s.ModelOrigin == nil {
		http.Error(w, "model upstream unset", http.StatusServiceUnavailable)
		return
	}
	upstream := s.modelOrigin()
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	orig := proxy.Director
	proxy.Director = func(req *http.Request) {
		orig(req)
		req.Host = upstream.Host
		// The guest's grant never goes upstream.
		req.Header.Del("Authorization")
		req.Header.Del("X-Api-Key")
		if s.ModelKey != "" {
			if s.modelProvider() == ProviderAnthropic {
				req.Header.Set("X-Api-Key", s.ModelKey)
			} else {
				req.Header.Set("Authorization", "Bearer "+s.ModelKey)
			}
		}
		path := strings.TrimPrefix(req.URL.Path, "/model-proxy")
		if path == "" {
			path = "/"
		}
		req.URL.Path = path
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) modelProvider() string {
	if s.ModelProvider == "" {
		return ProviderXAI
	}
	return s.ModelProvider
}

func (s *Server) modelOrigin() *url.URL {
	if s.ModelOrigin != nil {
		return s.ModelOrigin
	}
	u, _ := url.Parse(defaultModelOrigins[s.modelProvider()])
	return u
}
