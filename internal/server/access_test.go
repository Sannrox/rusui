package server

import (
	"net/http"
	"testing"
)

func TestClassifyBearerSeparatesOperatorAndWorker(t *testing.T) {
	if ClassifyBearer("op", "op", "wsec") != CredOperator {
		t.Fatal("operator")
	}
	if ClassifyBearer("wsec", "op", "wsec") != CredWorker {
		t.Fatal("worker")
	}
	if ClassifyBearer("wsec", "", "wsec") != CredWorker {
		t.Fatal("worker only")
	}
	if ClassifyBearer("same", "same", "same") != CredNone {
		t.Fatal("identical operator and worker secrets must fail closed")
	}
	if ClassifyBearer("nope", "op", "wsec") != CredNone {
		t.Fatal("unknown")
	}
}

func TestOperatorBrowserRejectsWorkerSecret(t *testing.T) {
	s := &Server{OperatorTok: "op-tok", WorkerSec: "wsec"}
	req, _ := http.NewRequest("GET", "/sessions", nil)
	req.Header.Set("Authorization", "Bearer wsec")
	if s.OperatorBrowserOK(req) {
		t.Fatal("worker accepted as operator browser")
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	if !s.OperatorBrowserOK(req) {
		t.Fatal("operator rejected")
	}
}

func TestPreviewOriginIsolated(t *testing.T) {
	if PreviewOriginIsolated("https://rusui.example:8080", "https://rusui.example:8080/preview/1") {
		t.Fatal("path-only preview must fail closed")
	}
	if PreviewOriginIsolated("https://127.0.0.1:8080", "https://127.0.0.1:8080") {
		t.Fatal("same origin")
	}
	if !PreviewOriginIsolated("https://127.0.0.1:8080", "http://127.0.0.1:3000") {
		t.Fatal("different port is isolated")
	}
	if !PreviewOriginIsolated("https://plane.example", "https://p1.preview.example") {
		t.Fatal("different host")
	}
	if PreviewOriginIsolated("not a url", "https://x") {
		t.Fatal("invalid plane URL")
	}
}
