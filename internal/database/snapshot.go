package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

var schemaIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ReadSnapshot gives a callback one point-in-time view of the database.
// PostgreSQL pins the view with repeatable read; SQLite reads a VACUUM INTO
// copy so the server's immediate-write transaction mode does not hold writers
// behind a long export.
func (db *DB) ReadSnapshot(ctx context.Context, fn func(Queryer) error) error {
	return db.ReadSnapshotBounded(ctx, 0, fn)
}

// ReadSnapshotBounded rejects SQLite snapshots whose allocated database size
// exceeds maxBytes before VACUUM copies them. The copy and caller's output
// staging file can coexist, so a backup caller can budget both on disk.
func (db *DB) ReadSnapshotBounded(ctx context.Context, maxBytes int64, fn func(Queryer) error) error {
	if fn == nil {
		return errors.New("database: snapshot callback is nil")
	}
	if db.Dialect() == SQLite {
		return db.readSQLiteSnapshot(ctx, maxBytes, fn)
	}
	if db.Dialect() != Postgres {
		return fmt.Errorf("database: snapshots are unsupported for dialect %q", db.Dialect())
	}

	tx, err := db.pool.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return fmt.Errorf("database: begin consistent snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	wrapped := &Tx{binder: binder{raw: tx, dialect: db.Dialect()}, tx: tx}
	if err := fn(wrapped); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("database: finish consistent snapshot: %w", err)
	}
	return nil
}

func (db *DB) readSQLiteSnapshot(ctx context.Context, maxBytes int64, fn func(Queryer) error) error {
	if maxBytes > 0 {
		var pages, pageSize int64
		if err := db.QueryRow(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
			return fmt.Errorf("database: read SQLite page count: %w", err)
		}
		if err := db.QueryRow(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
			return fmt.Errorf("database: read SQLite page size: %w", err)
		}
		if pages < 0 || pageSize < 1 || pages > maxBytes/pageSize {
			return fmt.Errorf("database: SQLite snapshot exceeds the %d-byte staging limit", maxBytes)
		}
	}
	file, err := os.CreateTemp("", "obsidian-arc-snapshot-*.db")
	if err != nil {
		return fmt.Errorf("database: create snapshot path: %w", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("database: close snapshot path: %w", err)
	}
	defer func() {
		_ = os.Remove(path)
		_ = os.Remove(path + "-wal")
		_ = os.Remove(path + "-shm")
	}()

	if _, err := db.Exec(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("database: copy SQLite snapshot: %w", err)
	}

	if !sqliteEnabled {
		return errors.New("database: SQLite snapshot unavailable in a nosqlite build")
	}
	uri := (&url.URL{Scheme: "file", Path: path}).String() + "?mode=ro&immutable=1"
	snapshot, err := Open(ctx, config.Database{
		Driver: "sqlite", DSN: uri, MaxOpenConns: 1, MaxIdleConns: 1,
	})
	if err != nil {
		return fmt.Errorf("database: open SQLite snapshot: %w", err)
	}
	defer snapshot.Close()
	if err := fn(snapshot); err != nil {
		return err
	}
	return nil
}

// Tables lists tables in the active SQLite database or PostgreSQL search
// schema. System tables owned by the database engine are omitted.
func Tables(ctx context.Context, q Queryer) ([]string, error) {
	query := `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`
	if q.Dialect() == Postgres {
		query = `SELECT table_name FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'
			ORDER BY table_name`
	}
	rows, err := q.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("database: list tables: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("database: scan table name: %w", err)
		}
		if !schemaIdentifier.MatchString(name) {
			return nil, fmt.Errorf("database: unexpected table identifier %q", name)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: read table names: %w", err)
	}
	return names, nil
}

// Columns returns the ordered column names of a table found by Tables.
func Columns(ctx context.Context, q Queryer, table string) ([]string, error) {
	if !schemaIdentifier.MatchString(table) {
		return nil, fmt.Errorf("database: invalid table identifier %q", table)
	}
	rows, err := q.Query(ctx, `SELECT * FROM "`+table+`" WHERE 1 = 0`)
	if err != nil {
		return nil, fmt.Errorf("database: inspect columns for %s: %w", table, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("database: read columns for %s: %w", table, err)
	}
	for _, column := range columns {
		if !schemaIdentifier.MatchString(column) {
			return nil, fmt.Errorf("database: unexpected column identifier %q", column)
		}
	}
	return columns, nil
}

type ForeignKey struct {
	Table     string
	Column    string
	RefTable  string
	RefColumn string
}

// ForeignKeys lists a table's foreign-key columns. It is kept beside the
// dialect boundary because SQLite exposes this metadata through PRAGMA while
// PostgreSQL exposes it through information_schema.
func ForeignKeys(ctx context.Context, q Queryer, table string) ([]ForeignKey, error) {
	if !schemaIdentifier.MatchString(table) {
		return nil, fmt.Errorf("database: invalid table identifier %q", table)
	}
	var rows *sql.Rows
	var err error
	if q.Dialect() == SQLite {
		rows, err = q.Query(ctx, `PRAGMA foreign_key_list("`+strings.ReplaceAll(table, `"`, `""`)+`")`)
	} else {
		rows, err = q.Query(ctx, `SELECT kcu.table_name, kcu.column_name, ccu.table_name, ccu.column_name
			FROM information_schema.table_constraints AS tc
			JOIN information_schema.key_column_usage AS kcu
			  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
			JOIN information_schema.constraint_column_usage AS ccu
			  ON ccu.constraint_name = tc.constraint_name AND ccu.table_schema = tc.table_schema
			WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = current_schema()
			  AND tc.table_name = ? ORDER BY kcu.ordinal_position`, table)
	}
	if err != nil {
		return nil, fmt.Errorf("database: inspect foreign keys for %s: %w", table, err)
	}
	defer rows.Close()

	var out []ForeignKey
	for rows.Next() {
		var key ForeignKey
		if q.Dialect() == SQLite {
			var id, sequence int
			var onUpdate, onDelete, match string
			if err := rows.Scan(&id, &sequence, &key.RefTable, &key.Column, &key.RefColumn, &onUpdate, &onDelete, &match); err != nil {
				return nil, fmt.Errorf("database: scan SQLite foreign key for %s: %w", table, err)
			}
			key.Table = table
		} else if err := rows.Scan(&key.Table, &key.Column, &key.RefTable, &key.RefColumn); err != nil {
			return nil, fmt.Errorf("database: scan PostgreSQL foreign key for %s: %w", table, err)
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: read foreign keys for %s: %w", table, err)
	}
	return out, nil
}

// PrimaryKeys returns primary-key columns in constraint order. Restore uses
// these values to reconnect nullable self-references after every row exists.
func PrimaryKeys(ctx context.Context, q Queryer, table string) ([]string, error) {
	if !schemaIdentifier.MatchString(table) {
		return nil, fmt.Errorf("database: invalid table identifier %q", table)
	}
	var rows *sql.Rows
	var err error
	if q.Dialect() == SQLite {
		rows, err = q.Query(ctx, `PRAGMA table_info("`+table+`")`)
	} else {
		rows, err = q.Query(ctx, `SELECT kcu.column_name
			FROM information_schema.table_constraints AS tc
			JOIN information_schema.key_column_usage AS kcu
			  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
			WHERE tc.constraint_type = 'PRIMARY KEY' AND tc.table_schema = current_schema()
			  AND tc.table_name = ? ORDER BY kcu.ordinal_position`, table)
	}
	if err != nil {
		return nil, fmt.Errorf("database: inspect primary key for %s: %w", table, err)
	}
	defer rows.Close()
	var columns []string
	if q.Dialect() == SQLite {
		for rows.Next() {
			var cid, notNull, primary int
			var name, kind string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
				return nil, fmt.Errorf("database: scan SQLite columns for %s: %w", table, err)
			}
			if primary > 0 {
				columns = append(columns, name)
			}
		}
	} else {
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return nil, fmt.Errorf("database: scan PostgreSQL primary key for %s: %w", table, err)
			}
			columns = append(columns, name)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: read primary key for %s: %w", table, err)
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("database: table %s has no primary key", table)
	}
	return columns, nil
}

// AppliedVersions returns migration identifiers in stable lexical order.
func AppliedVersions(ctx context.Context, q Queryer) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("database: read migration versions: %w", err)
	}
	defer rows.Close()
	var versions []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("database: scan migration version: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: read migration versions: %w", err)
	}
	return versions, nil
}
