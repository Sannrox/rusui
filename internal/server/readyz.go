package server

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/ops"
)

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	look := s.DiagnoseLookRuntime
	if look == nil {
		look = env.LookRuntime
	}
	getenv := s.DiagnoseEnv
	if getenv == nil {
		getenv = os.Getenv
	}
	rep := ops.Diagnose(ops.Options{
		PolicyPath:  s.PolicyPath,
		Addr:        s.Addr,
		PlaneURL:    s.DiagnosePlaneURL,
		LookRuntime: look,
		Env:         getenv,
		HTTP:        s.DiagnoseHTTP,
	})
	w.Header().Set("Content-Type", "application/json")
	if !rep.Ready {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(rep)
}
