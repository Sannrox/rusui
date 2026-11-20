package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
)

// TestHookBodyCap checks each inbound hook at its cap: a body of exactly the
// cap is read and reaches verification (so it fails on its bad signature, not
// on size), and one byte more is refused with 413 before verification.
func TestHookBodyCap(t *testing.T) {
	t.Parallel()
	st := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	p, err := policy.Parse([]byte(slackPol))
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(st, p, gh.NewFake(), &clock.Fake{T: time.Unix(1_700_000_000, 0).UTC()})
	e.ReloadPolicy(p)
	h := (&Server{Eng: e, WebhookSec: "whsec", SlackSec: "ssec"}).Handler()

	cases := []struct {
		path string
		cap  int
		// atCap is the status for a cap-sized body with a bad signature.
		atCap int
	}{
		{"/hooks/github", githubHookBodyCap, http.StatusUnauthorized},
		{"/hooks/gitlab", githubHookBodyCap, http.StatusUnauthorized},
		{"/hooks/events", hookBodyCap, http.StatusUnauthorized},
		// No webhook exists for session 1, so a read body ends in 404.
		{"/hooks/sessions/1", hookBodyCap, http.StatusNotFound},
		{"/hooks/slack", hookBodyCap, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			for _, n := range []int{tc.cap, tc.cap + 1} {
				req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(bytes.Repeat([]byte("a"), n)))
				req.Header.Set("X-Hub-Signature-256", "sha256=bad")
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				want := tc.atCap
				if n > tc.cap {
					want = http.StatusRequestEntityTooLarge
				}
				if rec.Code != want {
					t.Fatalf("%d-byte body: status %d, want %d (%s)", n, rec.Code, want, rec.Body.String())
				}
			}
		})
	}
}
