package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/stdlib"
)

// pgDriverName is the database/sql driver the PostgreSQL backend opens. It
// wraps pgx so every store statement keeps one SQL text with SQLite's `?`
// placeholders on both backends (ADR 0089 D2).
const pgDriverName = "rusui-pgx"

func init() {
	sql.Register(pgDriverName, pgDriver{})
}

type pgDriver struct{}

func (pgDriver) Open(name string) (driver.Conn, error) {
	c, err := stdlib.GetDefaultDriver().Open(name)
	if err != nil {
		return nil, err
	}
	pc, ok := c.(*stdlib.Conn)
	if !ok {
		_ = c.Close()
		return nil, fmt.Errorf("store: pgx driver returned %T", c)
	}
	return pgConn{pc}, nil
}

// pgConn rebinds placeholders and converts bool arguments, then forwards to
// pgx. It implements every optional interface pgx's conn does, plus
// Validator, so database/sql keeps its fast paths.
type pgConn struct{ c *stdlib.Conn }

var (
	_ driver.ConnBeginTx        = pgConn{}
	_ driver.ConnPrepareContext = pgConn{}
	_ driver.ExecerContext      = pgConn{}
	_ driver.QueryerContext     = pgConn{}
	_ driver.Pinger             = pgConn{}
	_ driver.SessionResetter    = pgConn{}
	_ driver.Validator          = pgConn{}
	_ driver.NamedValueChecker  = pgConn{}
)

func (p pgConn) Prepare(query string) (driver.Stmt, error) {
	return p.PrepareContext(context.Background(), query)
}

func (p pgConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	q, err := rebind(query)
	if err != nil {
		return nil, err
	}
	return p.c.PrepareContext(ctx, q)
}

func (p pgConn) Close() error { return p.c.Close() }

func (p pgConn) Begin() (driver.Tx, error) {
	return p.c.BeginTx(context.Background(), driver.TxOptions{})
}

func (p pgConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return p.c.BeginTx(ctx, opts)
}

func (p pgConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	q, err := rebind(query)
	if err != nil {
		return nil, err
	}
	return p.c.ExecContext(ctx, q, args)
}

func (p pgConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q, err := rebind(query)
	if err != nil {
		return nil, err
	}
	return p.c.QueryContext(ctx, q, args)
}

func (p pgConn) Ping(ctx context.Context) error { return p.c.Ping(ctx) }

func (p pgConn) ResetSession(ctx context.Context) error { return p.c.ResetSession(ctx) }

func (p pgConn) IsValid() bool { return !p.c.Conn().IsClosed() }

// CheckNamedValue stores Go bools as 0/1: boolean columns are integers in
// both schemas.
func (p pgConn) CheckNamedValue(nv *driver.NamedValue) error {
	if b, ok := nv.Value.(bool); ok {
		nv.Value = int64(0)
		if b {
			nv.Value = int64(1)
		}
		return nil
	}
	return p.c.CheckNamedValue(nv)
}

// rebind turns SQLite placeholders into PostgreSQL ones in one pass: each
// bare `?` becomes the next `$n`, and `?N` becomes `$N`. Quoted strings,
// quoted identifiers and comments are copied unchanged. A query that mixes
// bare `?` with `?N` or with native `$n` is refused, since the numbering
// would be ambiguous. Dollar-quoted and E-prefixed strings are not understood;
// store SQL must not use them.
func rebind(q string) (string, error) {
	if !strings.ContainsRune(q, '?') {
		return q, nil
	}
	var b strings.Builder
	b.Grow(len(q) + 8)
	bare, numbered, native := 0, false, false
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case c == '\'' || c == '"':
			j := skipQuoted(q, i, c)
			b.WriteString(q[i:j])
			i = j - 1
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			j := strings.IndexByte(q[i:], '\n')
			if j < 0 {
				j = len(q) - i
			}
			b.WriteString(q[i : i+j])
			i += j - 1
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			j := strings.Index(q[i+2:], "*/")
			end := len(q)
			if j >= 0 {
				end = i + 2 + j + 2
			}
			b.WriteString(q[i:end])
			i = end - 1
		case c == '$' && i+1 < len(q) && isDigit(q[i+1]):
			native = true
			b.WriteByte(c)
		case c == '?':
			j := i + 1
			for j < len(q) && isDigit(q[j]) {
				j++
			}
			if j > i+1 {
				numbered = true
				b.WriteByte('$')
				b.WriteString(q[i+1 : j])
				i = j - 1
				continue
			}
			bare++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(bare))
		default:
			b.WriteByte(c)
		}
	}
	if bare > 0 && native {
		return "", errors.New("store: query mixes ? with native $n placeholders")
	}
	if bare > 0 && numbered {
		return "", errors.New("store: query mixes ? with ?N placeholders")
	}
	return b.String(), nil
}

// skipQuoted returns the index just past the quoted run that starts at i,
// treating a doubled quote as an escape. An unterminated run ends at len(q).
func skipQuoted(q string, i int, quote byte) int {
	for j := i + 1; j < len(q); j++ {
		if q[j] != quote {
			continue
		}
		if j+1 < len(q) && q[j+1] == quote {
			j++
			continue
		}
		return j + 1
	}
	return len(q)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
