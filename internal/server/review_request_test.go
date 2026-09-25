package server

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

func TestReviewRequestRefusesPolicyGates(t *testing.T) {
	cases := []struct {
		name       string
		repo       string
		putPull    bool
		wantCalls  int
		configure  func(*testing.T, *engine.Engine)
		wantReason string
	}{
		{
			name:       "unbound repository",
			repo:       "other/repo",
			wantReason: "repository is not bound",
		},
		{
			name: "review disabled",
			repo: "example/test-repo",
			configure: func(t *testing.T, e *engine.Engine) {
				p, err := policy.Parse([]byte(strings.Replace(slackPol, "  review: true", "  review: false", 1)))
				if err != nil {
					t.Fatal(err)
				}
				e.ReloadPolicy(p)
			},
			wantReason: "review is disabled",
		},
		{
			name: "paused project",
			repo: "example/test-repo",
			configure: func(t *testing.T, e *engine.Engine) {
				if err := e.SetPause("test", true); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "project is paused",
		},
		{
			name:      "daily budget exhausted",
			repo:      "example/test-repo",
			putPull:   true,
			wantCalls: 1,
			configure: func(t *testing.T, e *engine.Engine) {
				day := e.Clock.Now().UTC().Format("2006-01-02")
				if err := e.Store.Tx(func(tx *sql.Tx) error {
					for range 50 {
						if err := store.IncrReviewsToday(tx, "example/test-repo", day); err != nil {
							return err
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "daily review budget exhausted",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "review.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			p, err := policy.Parse([]byte(slackPol))
			if err != nil {
				t.Fatal(err)
			}
			fake := gh.NewFake()
			e := engine.New(st, p, fake, &clock.Fake{T: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)})
			e.ReloadPolicy(p)
			if tc.putPull {
				fake.Put(snapshot.Item{Repo: tc.repo, Item: 42, ItemKind: "pull", State: "open", HeadSHA: "head-42"})
			}
			if tc.configure != nil {
				tc.configure(t, e)
			}
			handler := (&Server{Eng: e, WorkerSec: "wsec", OperatorTok: "op"}).Handler()
			req := httptest.NewRequest(http.MethodPost, "/reviews", strings.NewReader(`{"repo":"`+tc.repo+`","item":42}`))
			req.Header.Set("Authorization", "Bearer wsec")
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), tc.wantReason) {
				t.Fatalf("response %d %s", rr.Code, rr.Body.String())
			}
			if calls := fake.CallCount(); calls != tc.wantCalls {
				t.Fatalf("policy refusal fetched GitHub %d times, want %d", calls, tc.wantCalls)
			}
		})
	}
}
