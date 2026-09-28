package eval

import (
	"flag"
	"os"
	"testing"

	"github.com/sannrox/rusui/internal/evalcorpus"
)

var update = flag.Bool("update", false, "rewrite results.md from the development corpus")

// TestDevelopmentCorpus replays the committed development split and
// requires results.md to match. A change in plane behavior, the corpus,
// or scoring shows up as a diff; refresh with `go test ./eval -update`.
func TestDevelopmentCorpus(t *testing.T) {
	c, err := evalcorpus.Load("corpus/development.json", evalcorpus.SplitDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	r, err := evalcorpus.Run("development", c)
	if err != nil {
		t.Fatal(err)
	}
	got := evalcorpus.Markdown(r)
	if *update {
		if err := os.WriteFile("results.md", []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile("results.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("results.md is stale; run `go test ./eval -update` and review the diff\n\n%s", got)
	}
}
