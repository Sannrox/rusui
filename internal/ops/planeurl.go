package ops

import (
	"os"
	"strings"
)

// TLSEnabled reports whether the operator provisioned plane TLS.
func TLSEnabled(getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	return getenv("RUSUI_TLS_CERT") != "" && getenv("RUSUI_TLS_KEY") != ""
}

// PlaneBaseURL is the operator URL for the live plane. Container topology
// uses HTTPS.
func PlaneBaseURL(addr string, getenv func(string) string) string {
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	addr = strings.TrimSpace(addr)
	if strings.Contains(addr, "://") {
		return addr
	}
	scheme := "http"
	if TLSEnabled(getenv) {
		scheme = "https"
	}
	return scheme + "://" + addr
}

// GitProxyURL is the host prepare endpoint on the plane.
// The plane cert SAN must include this host (typically 127.0.0.1) and rusui.plane.
func GitProxyURL(addr string, getenv func(string) string) string {
	return strings.TrimRight(PlaneBaseURL(addr, getenv), "/") + "/git-proxy/github.com/"
}

func PlaneCAFile(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	return getenv("RUSUI_PLANE_CA")
}
