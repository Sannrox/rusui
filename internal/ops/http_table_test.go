package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readRepoFile(t *testing.T, root string, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestConfigurationHTTPTableDocumentsPostReviews(t *testing.T) {
	cfg := readRepoFile(t, filepath.Join("..", ".."), "docs", "configuration.md")
	_, rest, ok := strings.Cut(cfg, "## HTTP")
	if !ok {
		t.Fatal("docs/configuration.md missing ## HTTP")
	}
	table, _, _ := strings.Cut(rest, "## ")
	if !strings.Contains(table, "`GET` | `/sessions/{id}/receipt` | operator or worker token") {
		t.Fatal("HTTP table missing GET /sessions/{id}/receipt with operator or worker auth")
	}
	if !strings.Contains(table, "`POST` | `/reviews` | operator or worker token") {
		t.Fatal("HTTP table missing POST /reviews with operator or worker auth")
	}
	if !strings.Contains(table, "`POST` | `/sessions/{id}/turns` | operator or worker token") {
		t.Fatal("HTTP table missing POST /sessions/{id}/turns with operator or worker auth")
	}
	if !strings.Contains(table, "`POST` | `/sessions/{id}/cancel` | operator or worker token") {
		t.Fatal("HTTP table missing POST /sessions/{id}/cancel with operator or worker auth")
	}
	if !strings.Contains(table, "`POST` | `/sessions/{id}/archive` | operator or worker token") {
		t.Fatal("HTTP table missing POST /sessions/{id}/archive with operator or worker auth")
	}
	if !strings.Contains(table, "`POST` | `/sessions/{id}/unarchive` | operator or worker token") {
		t.Fatal("HTTP table missing POST /sessions/{id}/unarchive with operator or worker auth")
	}
	if !strings.Contains(table, "`GET` | `/sessions/{id}/grants` | operator token") {
		t.Fatal("HTTP table missing GET /sessions/{id}/grants with operator auth")
	}
	if !strings.Contains(table, "`POST` | `/sessions/{id}/grants` | operator token") {
		t.Fatal("HTTP table missing POST /sessions/{id}/grants with operator auth")
	}
	if !strings.Contains(table, "`DELETE` | `/sessions/{id}/grants/{tenant}` | operator token") {
		t.Fatal("HTTP table missing DELETE /sessions/{id}/grants/{tenant} with operator auth")
	}
	if !strings.Contains(table, "`PUT` | `/sessions/{id}/execution-mode` | operator token") {
		t.Fatal("HTTP table missing PUT /sessions/{id}/execution-mode with operator auth")
	}
	if !strings.Contains(table, "`GET` | `/sessions/{id}/tools` | operator or worker token") {
		t.Fatal("HTTP table missing GET /sessions/{id}/tools")
	}
	if !strings.Contains(table, "`GET` | `/sessions/{id}/tool-grants` | operator token") {
		t.Fatal("HTTP table missing GET /sessions/{id}/tool-grants with operator auth")
	}
	if !strings.Contains(table, "`POST` | `/sessions/{id}/tool-grants` | operator token") {
		t.Fatal("HTTP table missing POST /sessions/{id}/tool-grants with operator auth")
	}
	if !strings.Contains(table, "`DELETE` | `/sessions/{id}/tool-grants/{tool}` | operator token") {
		t.Fatal("HTTP table missing DELETE /sessions/{id}/tool-grants/{tool} with operator auth")
	}
	if !strings.Contains(table, "`PUT` | `/sessions/{id}/retention` | operator token") {
		t.Fatal("HTTP table missing PUT /sessions/{id}/retention with operator auth")
	}
	if !strings.Contains(table, "`POST` | `/sessions/{id}/retention/write-override` | operator token") {
		t.Fatal("HTTP table missing POST /sessions/{id}/retention/write-override with operator auth")
	}
	if !strings.Contains(table, "`POST` | `/sessions/{id}/retention/delete` | operator token") {
		t.Fatal("HTTP table missing POST /sessions/{id}/retention/delete with operator auth")
	}
	if !strings.Contains(table, "`POST` | `/sessions/{id}/write-actions` | operator token") {
		t.Fatal("HTTP table missing POST /sessions/{id}/write-actions with operator auth")
	}
	if !strings.Contains(table, "`POST` | `/sessions/{id}/draft-actions` | operator token") {
		t.Fatal("HTTP table missing POST /sessions/{id}/draft-actions with operator auth")
	}
	if !strings.Contains(table, "`DELETE` | `/sessions/{id}/queued` | operator or worker token") {
		t.Fatal("HTTP table missing DELETE /sessions/{id}/queued with operator or worker auth")
	}
	if strings.Contains(table, "`POST` | `/comment`") {
		t.Fatal("plane HTTP table must not list preview-origin POST /comment")
	}
}

func TestConfigurationDocumentsPreviewOrigin(t *testing.T) {
	cfg := readRepoFile(t, filepath.Join("..", ".."), "docs", "configuration.md")
	_, envRest, ok := strings.Cut(cfg, "## Environment variables")
	if !ok {
		t.Fatal("docs/configuration.md missing ## Environment variables")
	}
	envTable, _, _ := strings.Cut(envRest, "## ")
	if !strings.Contains(envTable, "`RUSUI_PREVIEW_BASE`") {
		t.Fatal("env table missing RUSUI_PREVIEW_BASE")
	}
	_, rest, ok := strings.Cut(cfg, "## Preview origin")
	if !ok {
		t.Fatal("docs/configuration.md missing ## Preview origin")
	}
	table, _, _ := strings.Cut(rest, "## ")
	if !strings.Contains(table, "`*` | `/`") {
		t.Fatal("preview-origin table missing * / proxy")
	}
	if !strings.Contains(table, "`POST` | `/comment`") {
		t.Fatal("preview-origin table missing POST /comment")
	}
	if !strings.Contains(table, "`?g=`") {
		t.Fatal("preview-origin table missing grant query ?g=")
	}
}

func TestPlaneBinaryServesPreviewBeside(t *testing.T) {
	src := readRepoFile(t, filepath.Join("..", ".."), "cmd", "rusui", "main.go")
	if !strings.Contains(src, "PreviewListener()") {
		t.Fatal("cmd/rusui must bind PreviewHandler via PreviewListener")
	}
	if !strings.Contains(src, "ListenAndServe(previewAddr, previewHandler)") {
		t.Fatal("cmd/rusui must ListenAndServe preview beside the plane")
	}
	if strings.Contains(src, "ListenAndServe(*addr, srv.PreviewHandler()") {
		t.Fatal("cmd/rusui must not mount PreviewHandler on the plane listener")
	}
}
