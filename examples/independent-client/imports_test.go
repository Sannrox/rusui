package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndependentClientImportsOnlyStdlib(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		src, err := parser.ParseFile(fset, e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range src.Imports {
			path := strings.Trim(im.Path.Value, `"`)
			if strings.Contains(path, ".") {
				t.Errorf("%s imports %s; independent client may use the Go standard library only", e.Name(), path)
			}
			if strings.Contains(path, "sannrox/rusui") {
				t.Errorf("%s imports rusui %s", e.Name(), path)
			}
		}
	}
	abs, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.ToSlash(abs), "/examples/independent-client") {
		t.Fatalf("test must run in examples/independent-client, got %s", abs)
	}
}
