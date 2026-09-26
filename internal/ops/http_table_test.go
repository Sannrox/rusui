package ops

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigurationHTTPTableDocumentsPostReviews(t *testing.T) {
	cfg := readRepoFile(t, filepath.Join("..", ".."), "docs", "configuration.md")
	_, rest, ok := strings.Cut(cfg, "## HTTP")
	if !ok {
		t.Fatal("docs/configuration.md missing ## HTTP")
	}
	table, _, _ := strings.Cut(rest, "## ")
	if !strings.Contains(table, "`POST` | `/reviews` | operator or worker token") {
		t.Fatal("HTTP table missing POST /reviews with operator or worker auth")
	}
}
