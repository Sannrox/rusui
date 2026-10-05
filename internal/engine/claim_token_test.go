package engine_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/engine"
)

func TestClaimTokenReplaysTheLeaseItWasGranted(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("test", "first", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StartRun("test", "second", ""); err != nil {
		t.Fatal(err)
	}
	first, err := h.e.ClaimToken("example/test-repo", "tok-a")
	if err != nil || first == nil {
		t.Fatalf("claim: %+v %v", first, err)
	}
	// The runner never saw the response and resends the same claim.
	again, err := h.e.ClaimToken("example/test-repo", "tok-a")
	if err != nil || again == nil {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if again.Job.ID != first.Job.ID || again.Job.LeaseGeneration != first.Job.LeaseGeneration || again.Snapshot.Body != first.Snapshot.Body {
		t.Fatalf("replay returned turn %d gen %d, want turn %d gen %d", again.Job.ID, again.Job.LeaseGeneration, first.Job.ID, first.Job.LeaseGeneration)
	}
	// A different token never receives that lease; with the lease cap at one
	// it gets no turn at all.
	other, err := h.e.ClaimToken("example/test-repo", "tok-b")
	if other != nil || !errors.Is(err, engine.ErrBudget) {
		t.Fatalf("other token claimed %+v %v", other, err)
	}
}

func TestClaimTokenDoesNotReplayAnEndedLease(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("test", "first", ""); err != nil {
		t.Fatal(err)
	}
	c, err := h.e.ClaimToken("example/test-repo", "tok-a")
	if err != nil || c == nil {
		t.Fatalf("claim: %+v %v", c, err)
	}
	h.clk.Advance(engine.Liveness)
	again, err := h.e.ClaimToken("example/test-repo", "tok-a")
	if err != nil {
		t.Fatal(err)
	}
	if again != nil && again.Job.LeaseGeneration == c.Job.LeaseGeneration {
		t.Fatalf("expired lease generation %d was replayed", c.Job.LeaseGeneration)
	}
	if again == nil || again.Job.RetryCount != 1 {
		t.Fatalf("expired lease should requeue with one retry and be claimed afresh: %+v", again)
	}
}

func TestClaimWithoutTokenNeverReplays(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("test", "first", ""); err != nil {
		t.Fatal(err)
	}
	if c, err := h.e.Claim("example/test-repo"); err != nil || c == nil {
		t.Fatalf("claim: %+v %v", c, err)
	}
	if c, err := h.e.Claim("example/test-repo"); err != nil || c != nil {
		t.Fatalf("second untokened claim got %+v %v", c, err)
	}
}

func TestClaimHTTPReplaysLeaseWithFreshTurnToken(t *testing.T) {
	h := setup(t)
	if _, err := h.e.StartRun("test", "first", ""); err != nil {
		t.Fatal(err)
	}
	claim := func() map[string]any {
		t.Helper()
		req, _ := http.NewRequest("POST", h.http.URL+"/jobs/claim", strings.NewReader(`{"repo":"example/test-repo","claim_token":"tok-a"}`))
		req.Header.Set("Authorization", "Bearer wsec")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("claim %d %s", res.StatusCode, b)
		}
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first, again := claim(), claim()
	for _, k := range []string{"turn_id", "lease_generation", "claimed_revision"} {
		if first[k] != again[k] {
			t.Fatalf("replay %s = %v, want %v", k, again[k], first[k])
		}
	}
	tok, _ := again["turn_token"].(string)
	if tok == "" || tok == first["turn_token"] {
		t.Fatalf("replay should carry a fresh turn token")
	}
	body := fmt.Sprintf(`{"lease_generation":%v,"claimed_revision":%v}`, again["lease_generation"], again["claimed_revision"])
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/jobs/%v/heartbeat", h.http.URL, again["turn_id"]), strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("heartbeat with replayed turn token: %d", res.StatusCode)
	}
}
