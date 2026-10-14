package engine_test

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func TestShortLivedRetentionExpiresSessionData(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "secret-prompt", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil || sess.Retention != store.RetentionShort || sess.RetentionExpiresAt == nil {
		t.Fatalf("default retention %+v %v", sess, err)
	}
	if sess.Prompt != "secret-prompt" {
		t.Fatalf("prompt %q", sess.Prompt)
	}
	if err := h.e.ExpireRetainedSessions(); err != nil {
		t.Fatal(err)
	}
	still, err := store.GetSession(h.st, sid)
	if err != nil || still.Prompt != "secret-prompt" || still.RetentionExpired {
		t.Fatalf("expired early %+v %v", still, err)
	}
	h.clk.T = sess.RetentionExpiresAt.Add(time.Second)
	if err := h.e.ExpireRetainedSessions(); err != nil {
		t.Fatal(err)
	}
	gone, err := store.GetSession(h.st, sid)
	if err != nil || gone.Prompt != "" || !gone.Archived || !gone.RetentionExpired {
		t.Fatalf("after ttl %+v %v", gone, err)
	}
	acts, err := store.ListActionsForSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range acts {
		if a.Type == "retention.expire" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing expire action %+v", acts)
	}
	var payload string
	if err := h.st.DB.QueryRow(`SELECT payload FROM snapshots WHERE repo=? AND item=? ORDER BY revision DESC LIMIT 1`, gone.Repo, gone.Item).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "secret-prompt") {
		t.Fatalf("snapshot kept prompt %s", payload)
	}
}

func TestExpireSessionRechecksClass(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "keep-me", "")
	if err != nil {
		t.Fatal(err)
	}
	err = h.st.Tx(func(tx *sql.Tx) error {
		return store.SetSessionRetentionTx(tx, sid, store.RetentionIndefinite, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	h.clk.T = h.clk.T.Add(store.EnvTTL + time.Hour)
	if err := h.e.ExpireSession(sid); err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil || sess.Prompt != "keep-me" || sess.RetentionExpired || sess.Archived {
		t.Fatalf("stale expiry %+v %v", sess, err)
	}
}

func TestIndefiniteRetentionDoesNotExpire(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "keep-me", "")
	if err != nil {
		t.Fatal(err)
	}
	err = h.st.Tx(func(tx *sql.Tx) error {
		return store.SetSessionRetentionTx(tx, sid, store.RetentionIndefinite, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	h.clk.T = h.clk.T.Add(store.EnvTTL + time.Hour)
	if err := h.e.ExpireRetainedSessions(); err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil || sess.Prompt != "keep-me" || sess.RetentionExpired {
		t.Fatalf("indefinite expired %+v %v", sess, err)
	}
}

func TestExpiredSessionCannotUnarchive(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "secret-prompt", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	h.clk.T = sess.RetentionExpiresAt.Add(time.Second)
	if err := h.e.ExpireRetainedSessions(); err != nil {
		t.Fatal(err)
	}
	if err := h.e.UnarchiveSession(sid); !errors.Is(err, store.ErrRetentionExpired) {
		t.Fatalf("unarchive expired %v", err)
	}
}

func TestDeleteSessionRetentionClearsData(t *testing.T) {
	h := setup(t)
	sid, err := h.e.StartRun("test", "delete-me", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.e.DeleteSessionRetention(sid); err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(h.st, sid)
	if err != nil || sess.Prompt != "" || !sess.RetentionExpired {
		t.Fatalf("delete %+v %v", sess, err)
	}
	acts, err := store.ListActionsForSession(h.st, sid)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range acts {
		if a.Type == "retention.delete" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing delete action %+v", acts)
	}
}
