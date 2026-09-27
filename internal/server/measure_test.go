package server

import (
	"bytes"
	"encoding/json"
	"io"
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

const measureFixture = `version: 2
defaults:
  never_release: true
  never_leak_private_to_public: true
  session_kinds: [review, run, scheduled]
  egress: trusted
  review: true
  comments: true
  close: true
  implement: false
  land: false
projects:
  test:
    repos:
      example/test-repo:
        visibility: public
        review: true
`

func TestTurnMeasurementHTTP(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "plane.db")
	clk := &clock.Fake{T: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)}
	f := gh.NewFake()
	f.Put(snapshot.Item{Repo: "example/test-repo", Item: 9, ItemKind: "pull", State: "open", HeadSHA: "published-head"})
	st, eng := openMeasurePlane(t, dbPath, clk, f)
	hs := httptest.NewServer((&Server{Eng: eng, WorkerSec: "wsec", OperatorTok: "op-tok", Guest: "claude"}).Handler())

	postJSON(t, hs.URL+"/projects/test/sessions", "wsec", map[string]any{
		"kind": "run", "prompt": "secret-prompt-text",
	}, http.StatusCreated)
	claim := claimTurn(t, hs.URL)
	clk.Advance(time.Second)
	action := postJSON(t, hs.URL+"/turns/"+claim.turnID+"/actions", "wsec", map[string]any{
		"type":   "permission.request",
		"reason": "recorded",
		"body": map[string]any{
			"tool_argument": "tool-arg-secret",
			"diff":          "diff --git a/secret",
			"credential":    "ghp_credentialvalue",
		},
	}, http.StatusAccepted)
	var actionID struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(action, &actionID); err != nil || actionID.ID == "" {
		t.Fatalf("action %s", action)
	}
	postJSON(t, hs.URL+"/approvals/"+actionID.ID, "op-tok", map[string]string{"decision": "deny"}, http.StatusNoContent)
	clk.Advance(4 * time.Second)
	postJSON(t, hs.URL+"/jobs/"+claim.turnID+"/complete", "wsec", map[string]any{
		"lease_generation": claim.gen,
		"claimed_revision": claim.rev,
		"artifact": map[string]any{
			"schema_version": 1, "repo": claim.repo, "item": claim.item, "item_kind": claim.kind,
			"claimed_revision": claim.rev, "snapshot_hash": claim.hash,
			"result": map[string]any{
				"schema_version": 1, "source_hash": claim.hash,
				"pull_request": 9, "candidate_sha": "not-the-head",
			},
		},
	}, http.StatusOK)

	worker := getAuth(t, hs.URL+"/projects/test/measurements", "wsec")
	if worker.StatusCode != http.StatusUnauthorized {
		t.Fatalf("worker read %d", worker.StatusCode)
	}
	_ = worker.Body.Close()
	agg := readAggregates(t, hs.URL, "op-tok")
	if agg.Turns != 1 || agg.Denies != 1 || agg.OmittedTokens != 1 || agg.TerminalStates["completed"] != 1 {
		t.Fatalf("aggregates %+v", agg)
	}
	if agg.MedianDurationMS == nil || *agg.MedianDurationMS != 5000 {
		t.Fatalf("median %+v", agg.MedianDurationMS)
	}
	if len(agg.Measurements) != 1 {
		t.Fatalf("rows %d", len(agg.Measurements))
	}
	m := agg.Measurements[0]
	if m.Provider != "claude" || m.ProviderVersion != "" || m.TerminalState != "completed" || m.Publication != "uncertain" || m.PermissionDecision != "deny" || m.Resume != "" {
		t.Fatalf("measurement %+v", m)
	}
	if m.WakeMS != nil || m.TokensIn != nil || m.TokensOut != nil {
		t.Fatalf("unknown stored as a number: %+v", m)
	}
	if m.FirstEventMS == nil || *m.FirstEventMS != 1000 || m.DurationMS == nil || *m.DurationMS != 5000 {
		t.Fatalf("times %+v", m)
	}
	raw, _ := json.Marshal(agg)
	for _, secret := range []string{"secret-prompt-text", "tool-arg-secret", "diff --git a/secret", "ghp_credentialvalue"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("measurement leaked %s", secret)
		}
	}
	blob := measurementBlob(t, st)
	for _, secret := range []string{"secret-prompt-text", "tool-arg-secret", "diff --git", "ghp_credentialvalue"} {
		if strings.Contains(blob, secret) {
			t.Fatalf("store leaked %s in %s", secret, blob)
		}
	}

	_ = st.Close()
	hs.Close()
	st2, eng2 := openMeasurePlane(t, dbPath, clk, f)
	defer func() { _ = st2.Close() }()
	hs2 := httptest.NewServer((&Server{Eng: eng2, WorkerSec: "wsec", OperatorTok: "op-tok", Guest: "claude", OTelEndpoint: "http://127.0.0.1:1/v1/traces"}).Handler())
	defer hs2.Close()
	again := readAggregates(t, hs2.URL, "op-tok")
	if len(again.Measurements) != 1 || again.Measurements[0].Publication != "uncertain" {
		t.Fatalf("after restart %+v", again)
	}
	postJSON(t, hs2.URL+"/projects/test/sessions", "wsec", map[string]any{"kind": "run", "prompt": "second"}, http.StatusCreated)
	claim2 := claimTurn(t, hs2.URL)
	denied := postJSON(t, hs2.URL+"/turns/"+claim2.turnID+"/actions", "wsec", map[string]any{"type": "permission.request", "reason": "recorded", "body": map[string]any{}}, http.StatusAccepted)
	var deniedID struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(denied, &deniedID); err != nil || deniedID.ID == "" {
		t.Fatalf("deny action %s", denied)
	}
	postJSON(t, hs2.URL+"/approvals/"+deniedID.ID, "op-tok", map[string]string{"decision": "deny"}, http.StatusNoContent)
	allowed := postJSON(t, hs2.URL+"/turns/"+claim2.turnID+"/actions", "wsec", map[string]any{"type": "permission.request", "reason": "recorded", "body": map[string]any{}}, http.StatusAccepted)
	var allowedID struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(allowed, &allowedID); err != nil || allowedID.ID == "" {
		t.Fatalf("allow action %s", allowed)
	}
	postJSON(t, hs2.URL+"/approvals/"+allowedID.ID, "op-tok", map[string]string{"decision": "allow"}, http.StatusNoContent)
	postJSON(t, hs2.URL+"/turns/"+claim2.turnID+"/actions", "wsec", map[string]any{"type": "resume", "reason": "refused", "body": map[string]any{}}, http.StatusAccepted)
	postJSON(t, hs2.URL+"/jobs/"+claim2.turnID+"/complete", "wsec", map[string]any{
		"lease_generation": claim2.gen,
		"claimed_revision": claim2.rev,
		"artifact": map[string]any{
			"schema_version": 1, "repo": claim2.repo, "item": claim2.item, "item_kind": claim2.kind,
			"claimed_revision": claim2.rev, "snapshot_hash": claim2.hash,
			"result": map[string]any{
				"schema_version": 1, "source_hash": claim2.hash,
				"findings": []any{map[string]string{"title": "noted", "body": "bounded"}},
			},
		},
	}, http.StatusOK)
	final := readAggregates(t, hs2.URL, "op-tok")
	if final.Turns != 2 || final.Denies != 2 || final.Measurements[0].Publication != "uncertain" || final.Measurements[0].PermissionDecision != "deny" {
		t.Fatalf("final %+v", final)
	}
	var second *store.Measurement
	for i := range final.Measurements {
		if final.Measurements[i].Resume == "refused" {
			second = &final.Measurements[i]
		}
	}
	if second == nil || second.PermissionDecision != "allow" {
		t.Fatalf("later allow hid the deny or dropped resume: %+v", final.Measurements)
	}
}

type claimedTurn struct {
	turnID, repo, kind, hash string
	item, gen, rev           int
}

func claimTurn(t *testing.T, base string) claimedTurn {
	t.Helper()
	body := postJSON(t, base+"/jobs/claim", "wsec", map[string]any{"repo": "example/test-repo"}, http.StatusOK)
	var raw struct {
		TurnID          int64  `json:"turn_id"`
		LeaseGeneration int    `json:"lease_generation"`
		ClaimedRevision int    `json:"claimed_revision"`
		Repo            string `json:"repo"`
		Item            int    `json:"item"`
		ItemKind        string `json:"item_kind"`
		ItemHash        string `json:"item_hash"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || raw.TurnID == 0 {
		t.Fatalf("claim %s %v", body, err)
	}
	return claimedTurn{
		turnID: jsonNumber(raw.TurnID), repo: raw.Repo, kind: raw.ItemKind, hash: raw.ItemHash,
		item: raw.Item, gen: raw.LeaseGeneration, rev: raw.ClaimedRevision,
	}
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func openMeasurePlane(t *testing.T, dbPath string, clk *clock.Fake, f *gh.Fake) (*store.Store, *engine.Engine) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	pol, err := policy.Parse([]byte(measureFixture))
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(st, pol, f, clk)
	eng.ReloadPolicy(pol)
	return st, eng
}

func postJSON(t *testing.T, url, token string, body any, want int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		t.Fatalf("%s %d %s", url, res.StatusCode, out)
	}
	return out
}

func getAuth(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func readAggregates(t *testing.T, base, token string) store.Aggregates {
	t.Helper()
	res := getAuth(t, base+"/projects/test/measurements", token)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("aggregates %d %s", res.StatusCode, b)
	}
	var agg store.Aggregates
	if err := json.NewDecoder(res.Body).Decode(&agg); err != nil {
		t.Fatal(err)
	}
	return agg
}

func measurementBlob(t *testing.T, st *store.Store) string {
	t.Helper()
	rows, err := st.DB.Query(`SELECT turn_id, session_id, project, provider, provider_version, terminal_state, ifnull(duration_ms,''), ifnull(first_event_ms,''), ifnull(wake_ms,''), resume, permission_decision, ifnull(tokens_in,''), ifnull(tokens_out,''), publication FROM turn_measurements`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	for rows.Next() {
		cols := make([]string, 14)
		ptrs := make([]any, len(cols))
		for i := range cols {
			ptrs[i] = &cols[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		b.WriteString(strings.Join(cols, " "))
	}
	return b.String()
}
