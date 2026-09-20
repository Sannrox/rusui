package ops

import "testing"

func TestPlaneBaseURLUsesHTTPSWhenTLSEnvSet(t *testing.T) {
	env := func(k string) string {
		switch k {
		case "RUSUI_TLS_CERT", "RUSUI_TLS_KEY":
			return "/etc/rusui/plane.crt"
		default:
			return ""
		}
	}
	got := PlaneBaseURL("127.0.0.1:8080", env)
	if got != "https://127.0.0.1:8080" {
		t.Fatalf("%s", got)
	}
	if GitProxyURL("127.0.0.1:8080", env) != "https://127.0.0.1:8080/git-proxy/github.com/" {
		t.Fatal(GitProxyURL("127.0.0.1:8080", env))
	}
	clear := func(string) string { return "" }
	if PlaneBaseURL("127.0.0.1:8080", clear) != "http://127.0.0.1:8080" {
		t.Fatal(PlaneBaseURL("127.0.0.1:8080", clear))
	}
}
