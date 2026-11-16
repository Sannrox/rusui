package compat

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

func TestSurfacesCoverContractAndStayOptionalWhenExcluded(t *testing.T) {
	seen := map[string]Surface{}
	for _, s := range Surfaces() {
		if s.Name == "" || s.Class == "" {
			t.Fatalf("incomplete %#v", s)
		}
		if _, dup := seen[s.Name]; dup {
			t.Fatalf("duplicate %s", s.Name)
		}
		seen[s.Name] = s
		switch s.Class {
		case ClassSupported:
			if s.Positive == "" {
				t.Fatalf("%s supported without a positive check", s.Name)
			}
			if s.Negative == "" {
				t.Fatalf("%s supported without an incompatible-input check", s.Name)
			}
		case ClassExcluded, ClassExperimental, ClassAuthorValidated:
			if s.Class == ClassExcluded && s.Positive != "" {
				t.Fatalf("%s excluded but has a positive check that would make it mandatory", s.Name)
			}
		default:
			t.Fatalf("%s unknown class %s", s.Name, s.Class)
		}
	}
	for _, name := range []string{"sqlite-schema", "policy", "operator-api", "independent-client", "runner-protocol", "restore", "diagnostics-export", "review-receipt-export", "postgres", "ha-plane"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing surface %s", name)
		}
	}
	if seen["postgres"].Class != ClassExcluded || seen["ha-plane"].Class != ClassExcluded {
		t.Fatal("postgres/HA must stay excluded")
	}
	if seen["sqlite-schema"].Version != strconv.Itoa(store.CurrentSchema) {
		t.Fatalf("schema version %s want %d", seen["sqlite-schema"].Version, store.CurrentSchema)
	}
}

func TestPolicyV1AndUnknownFieldsFailClosed(t *testing.T) {
	if _, err := policy.Parse([]byte("version: 1\nprojects: {}\n")); err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("v1: %v", err)
	}
	if _, err := policy.Parse([]byte("version: 2\nprojects:\n  test:\n    extra: true\n    repos: {}\n")); err == nil || !strings.Contains(err.Error(), "field extra not found") {
		t.Fatalf("unknown field: %v", err)
	}
}

func TestCurrentSchemaOpensAndReportsIdentity(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "now.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ver, err := store.SchemaVersion(st)
	if err != nil || ver != store.CurrentSchema {
		t.Fatalf("schema %d %v", ver, err)
	}
}

func TestSelfCheckPasses(t *testing.T) {
	rep, err := SelfCheck(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Schema != store.CurrentSchema || rep.Policy != PolicyVersion || len(rep.Checks) == 0 {
		t.Fatalf("%+v", rep)
	}
	for _, c := range rep.Checks {
		if !c.OK {
			t.Fatalf("%s: %s", c.Name, c.Err)
		}
	}
}

// TestMappedTestsExist fails when a test named in a surface's Positive or
// Negative is renamed or deleted: `go test -run` would otherwise report
// "no tests to run" and the surface would lose its check silently.
func TestMappedTestsExist(t *testing.T) {
	root := filepath.Join("..", "..")
	defined := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", "_output", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				defined[fn.Name.Name] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range MappedTests() {
		if !defined[name] {
			t.Errorf("surface test %s does not exist", name)
		}
	}
}
