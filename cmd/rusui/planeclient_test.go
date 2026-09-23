package main

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestPlaneHTTPTrustsThePlaneCA(t *testing.T) {
	var got string
	hs := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(hs.Close)
	ca := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: hs.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("RUSUI_PLANE_CA", "")
	if _, err := planeHTTP(hs.URL); err == nil {
		t.Fatal("https plane accepted without a CA")
	}
	if c, err := planeHTTP("http://127.0.0.1:8080"); err != nil || c != http.DefaultClient {
		t.Fatalf("http plane %v %v", c, err)
	}

	t.Setenv("RUSUI_PLANE_CA", ca)
	res, err := planeClient(hs.URL, "tok", "/sessions")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 || got != "Bearer tok" {
		t.Fatalf("status %d auth %q", res.StatusCode, got)
	}
}
