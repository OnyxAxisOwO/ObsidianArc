package database

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations are written once, for both engines, with a placeholder for the
// only column type whose spelling differs. Adding a token here is preferable
// to keeping two copies of every migration in step by hand.
var typeTokens = map[Dialect]map[string]string{
	SQLite:   {"%BLOB%": "BLOB"},
	Postgres: {"%BLOB%": "BYTEA"},
}

type migration struct {
	version string
	body    string
}

// Migrate applies every migration not yet recorded, in filename order, each
// inside its own transaction. It returns the versions it applied.
//
// Nothing else in the server alters the schema. A column that is not in a
// migration file does not exist.
func (db *DB) Migrate(ctx context.Context) ([]string, error) {
	if _, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at BIGINT NOT NULL
	)`); err != nil {
		return nil, fmt.Errorf("database: create schema_migrations: %w", err)
	}

	done, err := db.appliedVersions(ctx)
	if err != nil {
		return nil, err
	}

	pending, err := loadMigrations(db.Dialect())
	if err != nil {
		return nil, err
	}

	applied := make([]string, 0, len(pending))
	for _, m := range pending {
		if done[m.version] {
			continue
		}
		if err := db.applyMigration(ctx, m); err != nil {
			return applied, err
		}
		applied = append(applied, m.version)
	}
	return applied, nil
}

func (db *DB) appliedVersions(ctx context.Context) (map[string]bool, error) {
	rows, err := db.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("database: read schema_migrations: %w", err)
	}
	defer rows.Close()

	done := map[string]bool{}
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("database: scan schema_migrations: %w", err)
		}
		done[version] = true
	}
	return done, rows.Err()
}

func (db *DB) applyMigration(ctx context.Context, m migration) error {
	return db.Tx(ctx, func(tx *Tx) error {
		for i, stmt := range splitStatements(m.body) {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("database: migration %s statement %d: %w", m.version, i+1, err)
			}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			m.version, time.Now().UnixMilli()); err != nil {
			return fmt.Errorf("database: record migration %s: %w", m.version, err)
		}
		return nil
	})
}

func loadMigrations(dialect Dialect) ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("database: read migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	out := make([]migration, 0, len(names))
	for _, name := range names {
		raw, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("database: read migration %s: %w", name, err)
		}
		body := string(raw)
		for token, replacement := range typeTokens[dialect] {
			body = strings.ReplaceAll(body, token, replacement)
		}
		out = append(out, migration{
			version: strings.TrimSuffix(name, ".sql"),
			body:    body,
		})
	}
	return out, nil
}

// splitStatements breaks a migration file on semicolons. pgx's extended
// protocol refuses a multi-statement Exec, so the file cannot simply be
// handed over whole.
//
// It understands single-quoted strings (including the doubled-quote escape)
// and `--` comments, which is everything the DDL in this project uses. It is
// not a SQL parser and does not need to be: these files are written here, not
// received from anywhere.
func splitStatements(src string) []string {
	var (
		out         []string
		current     strings.Builder
		inString    bool
		inComment   bool
		emitCurrent = func() {
			if stmt := strings.TrimSpace(current.String()); stmt != "" {
				out = append(out, stmt)
			}
			current.Reset()
		}
	)

	for i := 0; i < len(src); i++ {
		c := src[i]

		switch {
		case inComment:
			current.WriteByte(c)
			if c == '\n' {
				inComment = false
			}
		case inString:
			current.WriteByte(c)
			if c == '\'' {
				if i+1 < len(src) && src[i+1] == '\'' {
					current.WriteByte('\'')
					i++
				} else {
					inString = false
				}
			}
		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			inComment = true
			current.WriteByte(c)
		case c == '\'':
			inString = true
			current.WriteByte(c)
		case c == ';':
			emitCurrent()
		default:
			current.WriteByte(c)
		}
	}
	emitCurrent()
	return out
}
