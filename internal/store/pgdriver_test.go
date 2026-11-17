package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestRebind(t *testing.T) {
	for _, tc := range []struct {
		name, in, want, err string
	}{
		{name: "no placeholders", in: `SELECT 1`, want: `SELECT 1`},
		{name: "bare", in: `SELECT a FROM t WHERE x=? AND y=?`, want: `SELECT a FROM t WHERE x=$1 AND y=$2`},
		{name: "numbered", in: `SELECT ?1, ?2, ?1`, want: `SELECT $1, $2, $1`},
		{name: "numbered multi-digit", in: `SELECT ?12`, want: `SELECT $12`},
		{name: "string", in: `SELECT '?', ?`, want: `SELECT '?', $1`},
		{name: "doubled quote in string", in: `SELECT 'it''s ?', ?`, want: `SELECT 'it''s ?', $1`},
		{name: "json path string", in: `SELECT json_extract(p, '$.a') WHERE k=?`, want: `SELECT json_extract(p, '$.a') WHERE k=$1`},
		{name: "identifier", in: `SELECT "a?b", ?`, want: `SELECT "a?b", $1`},
		{name: "doubled quote in identifier", in: `SELECT "a""?", ?`, want: `SELECT "a""?", $1`},
		{name: "line comment", in: "SELECT ? -- why?\n, ?", want: "SELECT $1 -- why?\n, $2"},
		{name: "line comment at end", in: "SELECT ? -- why?", want: "SELECT $1 -- why?"},
		{name: "block comment", in: `SELECT ? /* a ? b */, ?`, want: `SELECT $1 /* a ? b */, $2`},
		{name: "unterminated string", in: `SELECT ?, 'x?`, want: `SELECT $1, 'x?`},
		{name: "native only", in: `SELECT $1`, want: `SELECT $1`},
		{name: "mixed native", in: `SELECT ?, $1`, err: "native"},
		{name: "mixed numbered", in: `SELECT ?, ?1`, err: "?N"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := rebind(tc.in)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("rebind(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}

// rebind does not understand dollar-quoted or E-prefixed strings, and store SQL
// keeps SQLite placeholders. Refuse them in every store SQL literal.
func TestStoreSQLAvoidsFormsRebindCannotRead(t *testing.T) {
	sqlLike := regexp.MustCompile(`(?i)\b(SELECT|INSERT|UPDATE|DELETE|CREATE|ALTER|DROP)\b`)
	bad := map[string]*regexp.Regexp{
		"dollar quote":       regexp.MustCompile(`\$\$`),
		"E'' string":         regexp.MustCompile(`(?i)(^|[^[:alnum:]_])E'`),
		"native placeholder": regexp.MustCompile(`\$[0-9]`),
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil || !sqlLike.MatchString(s) {
				return true
			}
			for what, re := range bad {
				if re.MatchString(s) {
					t.Errorf("%s: %s in SQL literal", fset.Position(lit.Pos()), what)
				}
			}
			return true
		})
	}
}
