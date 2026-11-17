package store

import (
	"bytes"
	"testing"
	"time"
)

func captureFixture(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	s := openTestStore(t)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	envID, err := InsertEnvironment(s, Environment{
		Name: "cap-env", Driver: "container", State: EnvReady,
		Handle: "ctr-cap", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := insertID(s.DB, `INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at)
VALUES (?, 'run', 'example/test-repo', 1, 'issue', 'open', ?) RETURNING id`, envID, now.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	return s, envID, sessionID
}

// captureWrites fingerprints writes to environment_captures: SQLite's
// change counter, or each PostgreSQL row version (xmin). It moves when any
// capture row is rewritten.
func captureWrites(t *testing.T, s *Store) string {
	t.Helper()
	q := `SELECT CAST(total_changes() AS TEXT)`
	if s.pg {
		q = `SELECT string_agg(xmin::text, ',' ORDER BY kind, name) FROM environment_captures`
	}
	var v string
	if err := s.DB.QueryRow(q).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestReplaceEnvironmentCapturesSkipsUnchangedBlobs(t *testing.T) {
	s, envID, sessionID := captureFixture(t)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	body := bytes.Repeat([]byte("web listening\n"), 4096)
	row := CaptureRow{Name: "web", Output: body}
	if err := ReplaceEnvironmentCaptures(s, envID, "service", []CaptureRow{row, {Name: "api", Output: []byte("api ok\n")}}, now); err != nil {
		t.Fatal(err)
	}
	before := captureWrites(t, s)
	later := now.Add(time.Minute)
	if err := ReplaceEnvironmentCaptures(s, envID, "service", []CaptureRow{row, {Name: "api", Output: []byte("api ok\n")}}, later); err != nil {
		t.Fatal(err)
	}
	if after := captureWrites(t, s); after != before {
		t.Fatalf("rewrote unchanged captures: %s -> %s", before, after)
	}
	caps, err := ListSessionCaptures(s, sessionID, CaptureListFilter{Kind: "service", Name: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 1 || caps[0].Output != string(body) || !caps[0].RecordedAt.Equal(now) {
		t.Fatalf("skip moved or dropped body %+v", caps)
	}

	changed := CaptureRow{Name: "web", Output: append(append([]byte{}, body...), []byte("changed\n")...)}
	if err := ReplaceEnvironmentCaptures(s, envID, "service", []CaptureRow{changed}, later); err != nil {
		t.Fatal(err)
	}
	caps, err = ListSessionCaptures(s, sessionID, CaptureListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 1 || caps[0].Name != "web" || caps[0].Output != string(changed.Output) || caps[0].RecordedAt.Equal(now) {
		t.Fatalf("content change %+v", caps)
	}
	if err := ReplaceEnvironmentCaptures(s, envID, "service", nil, later.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	caps, err = ListSessionCaptures(s, sessionID, CaptureListFilter{Kind: "service"})
	if err != nil || len(caps) != 0 {
		t.Fatalf("empty replace leftover %+v err=%v", caps, err)
	}
}

func TestListSessionCapturesOrdersPlaneHooks(t *testing.T) {
	s, envID, sessionID := captureFixture(t)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		kind, out string
	}{
		{"service", "svc\n"},
		{"resume", "resume\n"},
		{"setup", "setup\n"},
		{"pre-setup", "pre-setup\n"},
		{"pre-clone", "pre-clone\n"},
	} {
		if err := ReplaceEnvironmentCaptures(s, envID, row.kind, []CaptureRow{{Output: []byte(row.out)}}, now); err != nil {
			t.Fatal(err)
		}
	}
	caps, err := ListSessionCaptures(s, sessionID, CaptureListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 5 {
		t.Fatalf("caps %+v", caps)
	}
	want := []string{"pre-clone", "pre-setup", "setup", "resume", "service"}
	for i, kind := range want {
		if caps[i].Kind != kind {
			t.Fatalf("order[%d]=%s want %s (%+v)", i, caps[i].Kind, kind, caps)
		}
	}
}

func TestListSessionCapturesFiltersAndOmitsBodies(t *testing.T) {
	s, envID, sessionID := captureFixture(t)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	if err := ReplaceEnvironmentCaptures(s, envID, "setup", []CaptureRow{{Output: []byte("setup ok\n")}}, now); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceEnvironmentCaptures(s, envID, "service", []CaptureRow{
		{Name: "web", Output: []byte("web body\n"), Truncated: true},
		{Name: "api", Output: []byte("api body\n"), Failed: true},
	}, now); err != nil {
		t.Fatal(err)
	}
	web, err := ListSessionCaptures(s, sessionID, CaptureListFilter{Kind: "service", Name: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if len(web) != 1 || web[0].Name != "web" || web[0].Output != "web body\n" || !web[0].Truncated {
		t.Fatalf("kind+name filter %+v", web)
	}
	omitted, err := ListSessionCaptures(s, sessionID, CaptureListFilter{Kind: "service", OmitBody: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(omitted) != 2 {
		t.Fatalf("omit list %+v", omitted)
	}
	for _, c := range omitted {
		if c.Output != "" {
			t.Fatalf("omit-body loaded output %+v", c)
		}
		if c.Kind != "service" || c.RecordedAt.IsZero() {
			t.Fatalf("omit-body dropped metadata %+v", c)
		}
	}
	var stored []byte
	if err := s.DB.QueryRow(`SELECT output FROM environment_captures WHERE environment_id=? AND kind='service' AND name='web'`, envID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) != "web body\n" {
		t.Fatalf("stored body %q", stored)
	}
}
