package server

import (
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/store"
)

func TestSessionTrailerFollowsTheConfiguredURL(t *testing.T) {
	sess := &store.Session{ID: 42, Kind: store.SessionKindRun}
	// Unconfigured: the id.
	if got := (&Server{NoCoauthorTrailer: true}).commitTrailers(sess); len(got) != 1 || got[0] != "Rusui-Session: 42" {
		t.Fatalf("unconfigured trailers %q", got)
	}
	url, err := ParseSessionURL("https://plane.example/console/sessions/{session}")
	if err != nil {
		t.Fatal(err)
	}
	// Configured: the expanded URL, nothing else.
	got := (&Server{NoCoauthorTrailer: true, SessionURL: url}).commitTrailers(sess)
	if len(got) != 1 || got[0] != "Rusui-Session: https://plane.example/console/sessions/42" {
		t.Fatalf("configured trailers %q", got)
	}
	// Disabled: no trailer, URL or not.
	if got := (&Server{NoCoauthorTrailer: true, NoSessionTrailer: true, SessionURL: url}).commitTrailers(sess); len(got) != 0 {
		t.Fatalf("disabled trailers %q", got)
	}
}

func TestSessionURLTemplateRefusesAnythingButAnHTTPSPathWithOneSession(t *testing.T) {
	if url, err := ParseSessionURL(""); url != nil || err != nil {
		t.Fatalf("empty: %v %v", url, err)
	}
	for template, reason := range map[string]string{
		"http://plane.example/s/{session}":            "https",
		"/s/{session}":                                "https",
		"https://user:pass@plane.example/s/{session}": "userinfo",
		"https://plane.example/s/{session}?token=x":   "query",
		"https://plane.example/s/{session}#top":       "fragment",
		"https://plane.example/s/":                    "exactly one",
		"https://plane.example/{session}/{session}":   "exactly one",
		"https://{session}.plane.example/s":           "path",
	} {
		_, err := ParseSessionURL(template)
		if err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("%s: %v, want %q", template, err, reason)
		}
	}
}
