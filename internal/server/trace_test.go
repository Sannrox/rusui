package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

func TestTurnSpanJoinsCallerTraceparent(t *testing.T) {
	dir := t.TempDir()
	clk := &clock.Fake{T: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)}
	f := gh.NewFake()
	f.Put(snapshot.Item{Repo: "example/test-repo", Item: 9, ItemKind: "pull", State: "open", HeadSHA: "published-head"})
	st, eng := openMeasurePlane(t, filepath.Join(dir, "plane.db"), clk, f)
	t.Cleanup(func() { _ = st.Close() })
	var mu sync.Mutex
	var traces []map[string]any
	otel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/traces") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("otel decode %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		traces = append(traces, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(otel.Close)
	hs := httptest.NewServer((&Server{Eng: eng, WorkerSec: "wsec", OperatorTok: "op-tok", Guest: "claude", OTelEndpoint: otel.URL}).Handler())
	t.Cleanup(hs.Close)

	const inbound = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	postJSONHeaders(t, hs.URL+"/projects/test/sessions", "wsec", map[string]any{"kind": "run", "prompt": "traced"}, http.StatusCreated, map[string]string{
		"Traceparent": inbound,
		"Tracestate":  "vendor=one",
	})
	body := postJSON(t, hs.URL+"/jobs/claim", "wsec", map[string]any{"repo": "example/test-repo"}, http.StatusOK)
	var raw struct {
		TurnID          int64  `json:"turn_id"`
		LeaseGeneration int    `json:"lease_generation"`
		ClaimedRevision int    `json:"claimed_revision"`
		Repo            string `json:"repo"`
		Item            int    `json:"item"`
		ItemKind        string `json:"item_kind"`
		ItemHash        string `json:"item_hash"`
		Traceparent     string `json:"traceparent"`
		Tracestate      string `json:"tracestate"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || raw.TurnID == 0 {
		t.Fatalf("claim %s %v", body, err)
	}
	claim := claimedTurn{
		turnID: jsonNumber(raw.TurnID), repo: raw.Repo, kind: raw.ItemKind, hash: raw.ItemHash,
		item: raw.Item, gen: raw.LeaseGeneration, rev: raw.ClaimedRevision,
	}
	turn, err := store.GetTurn(st, raw.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Trace.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || turn.Trace.ParentID != "00f067aa0ba902b7" {
		t.Fatalf("stored %+v", turn.Trace)
	}
	if raw.Traceparent != turn.Trace.GuestHeader() {
		t.Fatalf("claim traceparent %q want %s", raw.Traceparent, turn.Trace.GuestHeader())
	}
	if raw.Tracestate != "vendor=one" {
		t.Fatalf("claim tracestate %q", raw.Tracestate)
	}
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
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(traces)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(traces) == 0 {
		t.Fatal("no OTLP trace")
	}
	span := otlpFirstSpan(t, traces[0])
	if span["traceId"] != turn.Trace.TraceID || span["parentSpanId"] != turn.Trace.ParentID || span["spanId"] != turn.Trace.SpanID {
		t.Fatalf("span %+v stored %+v", span, turn.Trace)
	}
}

func TestMalformedTraceparentStillAdmits(t *testing.T) {
	dir := t.TempDir()
	clk := &clock.Fake{T: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)}
	f := gh.NewFake()
	st, eng := openMeasurePlane(t, filepath.Join(dir, "plane.db"), clk, f)
	t.Cleanup(func() { _ = st.Close() })
	hs := httptest.NewServer((&Server{Eng: eng, WorkerSec: "wsec", OperatorTok: "op-tok", Guest: "claude"}).Handler())
	t.Cleanup(hs.Close)
	postJSONHeaders(t, hs.URL+"/projects/test/sessions", "wsec", map[string]any{"kind": "run", "prompt": "fresh"}, http.StatusCreated, map[string]string{
		"Traceparent": "not-a-trace",
	})
	turns, err := store.ListTurnsForSession(st, 1)
	if err != nil || len(turns) != 1 {
		t.Fatalf("turns %+v %v", turns, err)
	}
	turn, err := store.GetTurn(st, turns[0].ID)
	if err != nil || turn.Trace.TraceID == "" || turn.Trace.ParentID != "" {
		t.Fatalf("fresh %+v %v", turn, err)
	}
}

func postJSONHeaders(t *testing.T, url, token string, body any, want int, headers map[string]string) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(mustJSON(t, body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
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

func mustJSON(t *testing.T, body any) string {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func otlpFirstSpan(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	rs, _ := payload["resourceSpans"].([]any)
	if len(rs) == 0 {
		t.Fatalf("payload %+v", payload)
	}
	scope, _ := rs[0].(map[string]any)["scopeSpans"].([]any)
	if len(scope) == 0 {
		t.Fatalf("scope %+v", rs[0])
	}
	spans, _ := scope[0].(map[string]any)["spans"].([]any)
	if len(spans) == 0 {
		t.Fatalf("spans %+v", scope[0])
	}
	span, _ := spans[0].(map[string]any)
	return span
}
