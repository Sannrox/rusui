package main

import (
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Attach and transcript bodies stay open for the session; the https
// client must not cut them off the way the diagnose client does (#398).
func TestPlaneHTTPStreamsPastTheDiagnoseTimeout(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for range 7 {
			_, _ = io.WriteString(w, "x")
			w.(http.Flusher).Flush()
			time.Sleep(500 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUSUI_PLANE_CA", ca)
	client, err := planeHTTP(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil || string(body) != "xxxxxxx" {
		t.Fatalf("stream cut off after %q: %v", body, err)
	}
}
