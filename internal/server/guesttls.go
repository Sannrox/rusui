package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"

	"github.com/sannrox/rusui/internal/store"
)

func (s *Server) requireGuestTLS(w http.ResponseWriter, r *http.Request) bool {
	if !s.GuestHTTPSOnly {
		return true
	}
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return true
		}
	}
	http.Error(w, "https required", http.StatusBadRequest)
	return false
}

func (s *Server) grantFromRequest(r *http.Request) (*store.Grant, bool) {
	tok := bearer(r)
	if tok == "" || s.Eng == nil || s.Eng.Store == nil {
		return nil, false
	}
	sum := sha256.Sum256([]byte(tok))
	g, ok, err := store.LookupGrant(s.Eng.Store, hex.EncodeToString(sum[:]), s.Eng.Clock.Now())
	if err != nil || !ok {
		return nil, false
	}
	return g, true
}
