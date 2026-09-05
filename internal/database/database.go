// Package database opens the one connection pool the server uses and hides
// the two differences that would otherwise leak into every module: parameter
// placeholders (`?` vs `$1`) and a handful of column types.
//
// Every query in this project is written with `?` placeholders and goes
// through Exec/Query/QueryRow here, which rebind for the active dialect. No
// module imports a driver, and no module branches on which database it is
// talking to.
//
// One invariant the rest of the codebase depends on: a transaction never
// spans a network call to an AI provider. Transactions here are short — read
// some rows, write some rows, commit — which is what keeps a small pool from
// becoming a queue.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

type Dialect string

const (
	SQLite   Dialect = "sqlite"
	Postgres Dialect = "postgres"
)

// ErrNoRows is re-exported so stores do not have to import database/sql just
// to tell "not found" apart from a real failure.
var ErrNoRows = sql.ErrNoRows

// Queryer is what every store method takes. Both *DB and *Tx satisfy it, so
// the same method works standalone or inside a transaction and there is no
// second "…Tx" copy of any query.
type Queryer interface {
	Dialect() Dialect
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
	Query(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) *sql.Row
}

type rawQueryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// binder supplies the Queryer methods to both *DB and *Tx.
type binder struct {
	raw     rawQueryer
	dialect Dialect
}

func (b binder) Dialect() Dialect { return b.dialect }

func (b binder) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return b.raw.ExecContext(ctx, Rebind(b.dialect, query), args...)
}

func (b binder) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return b.raw.QueryContext(ctx, Rebind(b.dialect, query), args...)
}

func (b binder) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return b.raw.QueryRowContext(ctx, Rebind(b.dialect, query), args...)
}

type DB struct {
	binder
	pool *sql.DB
}

type Tx struct {
	binder
	tx *sql.Tx
}

// Open connects, applies the pool limits, and verifies the connection. It
// does not run migrations; main calls Migrate explicitly so a failure there
// is distinguishable from a failure to connect.
func Open(ctx context.Context, cfg config.Database) (*DB, error) {
	var (
		driver  string
		dsn     string
		dialect Dialect
	)

	switch cfg.Driver {
	case "sqlite":
		if !sqliteEnabled {
			return nil, errors.New("this build was compiled without SQLite support (build tag `nosqlite`); use OBSIDIAN_DB_DRIVER=postgres")
		}
		driver, dialect = sqliteDriverName, SQLite
		dsn = sqliteDSN(cfg.DSN)
	case "postgres":
		driver, dialect = "pgx", Postgres
		dsn = cfg.DSN
	default:
		return nil, fmt.Errorf("database: unknown driver %q", cfg.Driver)
	}

	pool, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("database: open %s: %w", cfg.Driver, err)
	}
	pool.SetMaxOpenConns(cfg.MaxOpenConns)
	pool.SetMaxIdleConns(cfg.MaxIdleConns)
	pool.SetConnMaxIdleTime(cfg.ConnMaxIdle)

	if err := pool.PingContext(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: connect %s: %w", cfg.Driver, err)
	}

	return &DB{binder: binder{raw: pool, dialect: dialect}, pool: pool}, nil
}

func (db *DB) Close() error { return db.pool.Close() }

// Pool exposes the standard handle for the few things that genuinely need it
// (stats, a driver-specific escape hatch). Queries should not use it: they
// would skip rebinding.
func (db *DB) Pool() *sql.DB { return db.pool }

// Tx runs fn inside a transaction, committing when it returns nil and rolling
// back on an error or a panic. Nested calls are not supported: pass the *Tx
// down instead.
func (db *DB) Tx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := db.pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("database: begin: %w", err)
	}
	wrapped := &Tx{binder: binder{raw: tx, dialect: db.dialect}, tx: tx}

	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback()
			panic(r)
		}
	}()

	if err := fn(wrapped); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("database: commit: %w", err)
	}
	return nil
}

// Rebind converts the `?` placeholders every query in this project is written
// with into whatever the dialect wants. Postgres gets `$1`, `$2`, …
//
// It is a straight scan, not a SQL parser: a literal `?` inside a string
// literal would be rewritten too. No query here contains one, and none should
// — a `?` in a value belongs in a bound parameter.
func Rebind(dialect Dialect, query string) string {
	if dialect != Postgres || !strings.Contains(query, "?") {
		return query
	}
	var out strings.Builder
	out.Grow(len(query) + 8)
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] != '?' {
			out.WriteByte(query[i])
			continue
		}
		n++
		out.WriteByte('$')
		out.WriteString(strconv.Itoa(n))
	}
	return out.String()
}

// IsNotFound reports whether err means "no such row", including when it has
// been wrapped on the way up.
func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// sqliteDSN turns a plain file path into a driver DSN carrying the pragmas
// this server needs. A DSN the caller already wrote as `file:…` is passed
// through untouched, so an unusual setup stays possible.
//
//   - WAL lets readers run while a write is in flight, which is the whole
//     reason concurrent requests do not serialise on the file.
//   - busy_timeout turns "database is locked" from an error into a wait.
//   - foreign_keys is off by default in SQLite; the schema relies on it.
//   - synchronous=NORMAL is the standard pairing with WAL: durable across a
//     process crash, at risk only from an OS-level crash mid-write.
//   - txlock=immediate takes the write lock when a transaction begins rather
//     than when it first writes, which is what removes the deadlock two
//     upgrading readers would otherwise hit.
func sqliteDSN(path string) string {
	if strings.HasPrefix(path, "file:") {
		return path
	}
	pragmas := []string{
		"journal_mode(WAL)",
		"busy_timeout(5000)",
		"foreign_keys(1)",
		"synchronous(NORMAL)",
	}
	query := url.Values{}
	for _, pragma := range pragmas {
		query.Add("_pragma", pragma)
	}
	query.Set("_txlock", "immediate")
	return "file:" + path + "?" + query.Encode()
}
