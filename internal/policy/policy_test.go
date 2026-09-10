package policy

import (
	"os"
	"testing"
)

func TestParseExampleAndFixture(t *testing.T) {
	for _, p := range []string{"../../policy.example.yaml", "../../policy.fixture.yaml"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		e, err := Parse(b)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if len(e.Repos) == 0 {
			t.Fatal("no repos")
		}
	}
}
