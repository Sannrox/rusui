package engine_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/policy"
)

func TestClaimIncludesPermissions(t *testing.T) {
	h := setup(t)
	p, err := policy.Parse([]byte(fixture + `
    permissions:
      - tool: shell
        kind: execute
`))
	if err != nil {
		t.Fatal(err)
	}
	h.e.ReloadPolicy(p)
	if _, err := h.e.StartRun("test", "hi", ""); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", h.http.URL+"/jobs/claim", strings.NewReader(`{"repo":"example/test-repo"}`))
	req.Header.Set("Authorization", "Bearer wsec")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), `"tool":"shell"`) {
		t.Fatalf("permissions %s", b)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
}
