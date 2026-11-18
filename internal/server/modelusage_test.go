package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

// streamEvents is a message stream with cache usage, sent in pieces that
// split events and lines across reads.
var streamEvents = []string{
	"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"cache_creation_input_tokens\":7,",
	"\"cache_read_input_tokens\":11,\"output_tokens\":1}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"hi\"}}\n\n",
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":40}}\r\n\r\nevent: message_st",
	"op\ndata: {\"type\":\"message_stop\"}\n\n",
}

// proxiedTurn runs one run turn whose guest sends each path through the
// plane's model proxy to upstream, and returns the turn's measurement and
// the bodies the guest received. With restart, a new engine takes over the
// store after the claim, as after a plane restart.
func proxiedTurn(t *testing.T, upstream http.Handler, restart bool, paths ...string) (store.Measurement, [][]byte, *store.Store) {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	origin, err := url.Parse(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock.Fake{T: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
	f := gh.NewFake()
	st, eng := openMeasurePlane(t, filepath.Join(t.TempDir(), "plane.db"), clk, f)
	srv := &Server{Eng: eng, WorkerSec: "wsec", OperatorTok: "op-tok", Guest: "claude", ModelKey: "plane-key", ModelOrigin: origin}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	postJSON(t, hs.URL+"/projects/test/sessions", "wsec", map[string]any{"kind": "run", "prompt": "p"}, http.StatusCreated)
	claim := claimTurn(t, hs.URL)
	if restart {
		clk.Advance(time.Second)
		pol, err := policy.Parse([]byte(measureFixture))
		if err != nil {
			t.Fatal(err)
		}
		srv.Eng = engine.New(st, pol, f, clk)
		srv.Eng.ReloadPolicy(pol)
	}
	var bodies [][]byte
	for _, path := range paths {
		req, err := http.NewRequest(http.MethodPost, hs.URL+"/model-proxy"+path, bytes.NewReader([]byte(`{}`)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+claim.grant)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body) // an interrupted stream ends in an error
		_ = res.Body.Close()
		bodies = append(bodies, body)
	}
	postJSON(t, hs.URL+"/jobs/"+claim.turnID+"/complete", "wsec", map[string]any{
		"lease_generation": claim.gen, "claimed_revision": claim.rev,
		"artifact": map[string]any{
			"schema_version": 1, "repo": claim.repo, "item": claim.item, "item_kind": claim.kind,
			"claimed_revision": claim.rev, "snapshot_hash": claim.hash,
			"result": map[string]any{
				"schema_version": 1, "source_hash": claim.hash,
				"findings": []map[string]string{{"title": "t", "body": "b"}},
			},
		},
	}, http.StatusOK)
	agg := readAggregates(t, hs.URL, "op-tok")
	if len(agg.Measurements) != 1 {
		t.Fatalf("measurements %+v", agg)
	}
	return agg.Measurements[0], bodies, st
}

func TestModelProxyRecordsTurnTokens(t *testing.T) {
	t.Parallel()
	const chat = `{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":30}}}`
	up := http.NewServeMux()
	up.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chat)
	})
	// Token counting bills nothing, whatever it reports.
	up.HandleFunc("/v1/messages/count_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"input_tokens":999}`)
	})
	up.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, part := range streamEvents {
			_, _ = io.WriteString(w, part)
			w.(http.Flusher).Flush()
		}
	})
	m, bodies, st := proxiedTurn(t, up, false, "/v1/chat/completions", "/v1/messages/count_tokens", "/v1/messages")

	var stream bytes.Buffer
	for _, part := range streamEvents {
		stream.WriteString(part)
	}
	if string(bodies[0]) != chat || !bytes.Equal(bodies[2], stream.Bytes()) {
		t.Fatalf("guest received altered bodies %q", bodies)
	}
	// Input 70+5 uncached plus 7 cache writes; cache reads 30+11 stay out.
	if m.TokensIn == nil || *m.TokensIn != 82 || m.TokensOut == nil || *m.TokensOut != 60 {
		t.Fatalf("tokens in %v out %v", m.TokensIn, m.TokensOut)
	}
	var body string
	if err := st.DB.QueryRow(`SELECT body FROM actions WHERE action_type='model.proxy'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	want := `"usage":{"input_tokens":75,"cache_write_tokens":7,"cache_read_tokens":41,"output_tokens":60}`
	if !bytes.Contains([]byte(body), []byte(want)) {
		t.Fatalf("model.proxy row %s", body)
	}
}

func TestModelProxyTokensUnknownWhenUsageUnseen(t *testing.T) {
	t.Parallel()
	jsonBody := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}
	}
	for name, tc := range map[string]struct {
		upstream http.HandlerFunc
		restart  bool
	}{
		"no usage": {upstream: jsonBody(`{"choices":[]}`)},
		// The transport decodes gzip itself; an encoding it cannot decode
		// stays opaque.
		"compressed": {upstream: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "zstd")
			_, _ = io.WriteString(w, `{"usage":{"prompt_tokens":3,"completion_tokens":4}}`)
		}},
		"interrupted": {upstream: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, streamEvents[0]+streamEvents[1])
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}},
		"stream ended by an error event": {upstream: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, streamEvents[0]+streamEvents[1]+"event: error\ndata: {\"type\":\"error\"}\n\n")
		}},
		"claimed before the plane started": {upstream: jsonBody(`{"usage":{"prompt_tokens":3,"completion_tokens":4}}`), restart: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m, _, _ := proxiedTurn(t, tc.upstream, tc.restart, "/v1/chat/completions")
			if m.TokensIn != nil || m.TokensOut != nil {
				t.Fatalf("partial usage counted: in %v out %v", *m.TokensIn, *m.TokensOut)
			}
		})
	}
}
