package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// Action rows are append-only: the store refuses a rewrite, except the
// retention prompt scrub (ADR 0081), which may only blank a steer prompt or a
// message chunk's text.
func TestActionRowsRefuseRewriteExceptRetentionScrub(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at) VALUES (1,'run','o/r',1,'issue','open','2026-11-17T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	sid := int64(1)
	for _, a := range []Action{
		{ID: "summary", Type: "model.proxy", Body: `{"count":1}`},
		{ID: "steer", Type: "operator.steer", Body: `{"prompt":"secret-prompt","at":"t"}`},
		{ID: "chunk", Type: "acp.session.update", Body: `{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"secret-reply"}}}`},
	} {
		a.SessionID, a.Repo, a.Item, a.ReasonCode, a.EvidenceClass, a.LimitSentence = &sid, "o/r", 1, "recorded", "plane_observed", "limit"
		if err := InsertAction(st, a); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`UPDATE actions SET body='{"count":2}' WHERE action_id='summary'`,
		`UPDATE actions SET action_type='model.proxy' WHERE action_id='steer'`,
		`UPDATE actions SET reason_code='forged', body=json_set(body, '$.prompt', '') WHERE action_id='steer'`,
		`UPDATE actions SET body='{"prompt":"forged"}' WHERE action_id='steer'`,
		`UPDATE actions SET body=json_set(body, '$.at', 'forged', '$.prompt', '') WHERE action_id='steer'`,
		`UPDATE actions SET body=json_set(body, '$.update.content.text', 'forged') WHERE action_id='chunk'`,
	} {
		if _, err := st.DB.Exec(q); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s: err %v, want append-only refusal", q, err)
		}
	}
	if err := st.Tx(func(tx *sql.Tx) error { return scrubPromptBearingActionsTx(tx, sid) }); err != nil {
		t.Fatalf("retention scrub refused: %v", err)
	}
	for id, secret := range map[string]string{"steer": "secret-prompt", "chunk": "secret-reply"} {
		var body string
		if err := st.DB.QueryRow(`SELECT body FROM actions WHERE action_id=?`, id).Scan(&body); err != nil || strings.Contains(body, secret) {
			t.Fatalf("%s scrubbed body %q %v", id, body, err)
		}
	}
}
