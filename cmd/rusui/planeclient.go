package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/sannrox/rusui/internal/ops"
)

// planeHTTP trusts the plane CA (RUSUI_PLANE_CA) for an https plane URL;
// setup-generated TLS is signed by a local CA the system does not know.
func planeHTTP(url string) (*http.Client, error) {
	if !strings.HasPrefix(url, "https://") {
		return http.DefaultClient, nil
	}
	ca := os.Getenv("RUSUI_PLANE_CA")
	if ca == "" {
		return nil, fmt.Errorf("set RUSUI_PLANE_CA for an https plane URL")
	}
	return ops.TLSClient(ca)
}
