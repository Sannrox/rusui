package server

import (
	"net/http"
	"net/url"
	"strings"
)

// CredentialClass is the plane-facing identity of a presented secret.
type CredentialClass int

const (
	CredNone CredentialClass = iota
	CredOperator
	CredWorker
	CredTurn
)

// ClassifyBearer maps a presented bearer to a credential class.
// Worker and operator secrets are never interchangeable.
func ClassifyBearer(tok, operator, worker string) CredentialClass {
	if tok == "" {
		return CredNone
	}
	if operator != "" && worker != "" && operator == worker {
		return CredNone
	}
	if operator != "" && tok == operator {
		return CredOperator
	}
	if worker != "" && tok == worker {
		return CredWorker
	}
	return CredNone
}

func bearerToken(r *http.Request) string {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return tok
}

// OperatorBrowserOK is true only for the operator class. Worker and turn
// secrets cannot mint a console session.
func (s *Server) OperatorBrowserOK(r *http.Request) bool {
	return ClassifyBearer(bearerToken(r), s.OperatorTok, s.WorkerSec) == CredOperator
}

// operatorOrWorkerOK is the session/approval observer path: operator
// console/editor and the worker CLI share it. Claim/complete stay worker-only.
func (s *Server) operatorOrWorkerOK(r *http.Request) bool {
	c := ClassifyBearer(bearerToken(r), s.OperatorTok, s.WorkerSec)
	if c == CredOperator || c == CredWorker {
		return true
	}
	// WorkerOK also accepts X-Worker-Token; keep that for CLI.
	return s.workerOK(r)
}

// PreviewOriginIsolated reports whether previewHost is a different origin
// from the plane. Same host (including portless default) fails closed.
func PreviewOriginIsolated(planeURL, previewURL string) bool {
	p, err1 := url.Parse(planeURL)
	v, err2 := url.Parse(previewURL)
	if err1 != nil || err2 != nil {
		return false
	}
	if p.Scheme == "" || v.Scheme == "" || p.Host == "" || v.Host == "" {
		return false
	}
	return !strings.EqualFold(p.Scheme, v.Scheme) || !strings.EqualFold(p.Host, v.Host)
}
