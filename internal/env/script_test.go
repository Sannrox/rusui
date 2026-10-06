package env

import (
	"strings"
	"testing"
)

func TestProcessExecScriptOmitsOperatorTokenAndProviderKey(t *testing.T) {
	t.Setenv("RUSUI_OPERATOR_TOKEN", "op-secret-value")
	t.Setenv("XAI_API_KEY", "provider-secret-value")
	t.Setenv("GITHUB_TOKEN", "github-secret-value")
	t.Setenv("GH_TOKEN", "gh-secret-value")
	root := t.TempDir()
	d := Process{Root: root}
	handle, err := d.Create("box")
	if err != nil {
		t.Fatal(err)
	}
	out, truncated, err := d.ExecScript(handle, "echo HOME=$HOME; env")
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatal("truncated")
	}
	s := string(out)
	for _, secret := range []string{"op-secret-value", "provider-secret-value", "github-secret-value", "gh-secret-value"} {
		if strings.Contains(s, secret) {
			t.Fatalf("secret in hook env: %s", s)
		}
	}
	if !strings.Contains(s, "HOME="+handle) {
		t.Fatalf("HOME %s", s)
	}
}
