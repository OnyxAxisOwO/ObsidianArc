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
//
// plugins are the migration directories compiled-in plugins bring, each a
// filesystem of *.sql files at its root. They join the core's files in one
// sorted sequence under one schema_migrations table, which is what lets a
// migration move from the core into a plugin without being applied twice: it
// keeps its version, and a database that ran it as core already has the row.
// A plugin's own new migrations are named <plugin>_NNNN_*.sql, which sorts
// after every numbered core file and cannot collide with a later one.
func (db *DB) Migrate(ctx context.Context, plugins ...fs.FS) ([]string, error) {
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

	pending, err := loadMigrations(db.Dialect(), plugins...)
	if err != nil {
		return nil, err
	}
	return db.applyPending(ctx, pending, done)
}

func (db *DB) applyPending(ctx context.Context, pending []migration, done map[string]bool) ([]string, error) {
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

// MigrateFor brings a database to the schema a backup was taken from: the
// core's files, and of the plugins' only the versions the backup's instance
// had applied. Migrate would apply every plugin file it is handed, which is
// wrong here — the schema check that follows compares tables and columns, so
// a plugin the instance never installed, or removed with its data, would
// leave tables behind that the archive does not have.
//
// The first source to hold a version supplies it, so the copy a backup
// carried outranks one the deployment bundles. A version nobody supplies is
// an error that names it: the archive holds tables nothing here can create.
func (db *DB) MigrateFor(ctx context.Context, versions []string, plugins ...fs.FS) ([]string, error) {
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
	have := make(map[string]bool, len(pending))
	for _, m := range pending {
		have[m.version] = true
	}
	wanted := make(map[string]bool, len(versions))
	for _, version := range versions {
		wanted[version] = true
	}
	for _, source := range plugins {
		found, err := readMigrations(db.Dialect(), []fs.FS{source})
		if err != nil {
			return nil, err
		}
		for _, m := range found {
			if wanted[m.version] && !have[m.version] {
				have[m.version] = true
				pending = append(pending, m)
			}
		}
	}
	var missing []string
	for _, version := range versions {
		if !have[version] {
			missing = append(missing, version)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("database: the backup's instance had applied %s, which nothing here can supply — a plugin package it had installed and later removed keeping its data must be provided again, or restore with the Obsidian Arc build that made the backup",
			strings.Join(missing, ", "))
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].version < pending[j].version })
	return db.applyPending(ctx, pending, done)
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
	return db.Tx(ctx, func(tx *Tx) error { return runMigration(ctx, tx, m) })
}

// MigrateIn applies the migrations in sources that are not yet recorded,
// inside the caller's transaction, and returns the versions it applied.
// The core's own files are not among them: this is for installing a plugin
// on a running server, where the plugin's tables, its first settings and
// its state have to arrive together or not at all. Both engines this server
// runs on have transactional DDL, so a failure halfway leaves nothing.
func (db *DB) MigrateIn(ctx context.Context, tx *Tx, sources ...fs.FS) ([]string, error) {
	pending, err := readMigrations(db.Dialect(), sources)
	if err != nil {
		return nil, err
	}
	var applied []string
	for _, m := range pending {
		var recorded int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&recorded); err != nil {
			return applied, fmt.Errorf("database: read schema_migrations: %w", err)
		}
		if recorded > 0 {
			continue
		}
		if err := runMigration(ctx, tx, m); err != nil {
			return applied, err
		}
		applied = append(applied, m.version)
	}
	return applied, nil
}

// Versions lists the migration versions sources hold, sorted.
func Versions(sources ...fs.FS) ([]string, error) {
	found, err := readMigrations(SQLite, sources)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(found))
	for _, m := range found {
		out = append(out, m.version)
	}
	return out, nil
}

// Applied reports which of versions schema_migrations records.
func Applied(ctx context.Context, q Queryer, versions []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, version := range versions {
		var n int
		if err := q.QueryRow(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&n); err != nil {
			return nil, fmt.Errorf("database: read schema_migrations: %w", err)
		}
		out[version] = n > 0
	}
	return out, nil
}

// Unrecord removes versions from schema_migrations, for a plugin whose
// uninstall dropped what they created: reinstalling it has to run them
// again rather than trust a record of tables that are gone.
func Unrecord(ctx context.Context, q Queryer, versions []string) error {
	for _, version := range versions {
		if _, err := q.Exec(ctx, `DELETE FROM schema_migrations WHERE version = ?`, version); err != nil {
			return fmt.Errorf("database: unrecord migration %s: %w", version, err)
		}
	}
	return nil
}

// RunScripts runs every *.sql file in fsys, in name order, through q, with
// the same type tokens and statement splitting as a migration and nothing
// recorded — the undo a plugin ships for its own migrations.
func RunScripts(ctx context.Context, q Queryer, fsys fs.FS) error {
	scripts, err := readMigrations(q.Dialect(), []fs.FS{fsys})
	if err != nil {
		return err
	}
	for _, script := range scripts {
		for i, stmt := range splitStatements(script.body) {
			if _, err := q.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("database: script %s statement %d: %w", script.version, i+1, err)
			}
		}
	}
	return nil
}

func runMigration(ctx context.Context, q Queryer, m migration) error {
	for i, stmt := range splitStatements(m.body) {
		if _, err := q.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("database: migration %s statement %d: %w", m.version, i+1, err)
		}
	}
	if _, err := q.Exec(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version, time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("database: record migration %s: %w", m.version, err)
	}
	return nil
}

func loadMigrations(dialect Dialect, plugins ...fs.FS) ([]migration, error) {
	core, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("database: read migrations: %w", err)
	}
	return readMigrations(dialect, append([]fs.FS{core}, plugins...))
}

func readMigrations(dialect Dialect, sources []fs.FS) ([]migration, error) {
	var out []migration
	seen := map[string]bool{}
	for _, source := range sources {
		entries, err := fs.ReadDir(source, ".")
		if err != nil {
			return nil, fmt.Errorf("database: read migrations: %w", err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
				continue
			}
			version := strings.TrimSuffix(name, ".sql")
			// Two owners of one version would mean one of them is silently
			// skipped on every database that ran the other.
			if seen[version] {
				return nil, fmt.Errorf("database: migration %s is defined twice", version)
			}
			seen[version] = true
			raw, err := fs.ReadFile(source, name)
			if err != nil {
				return nil, fmt.Errorf("database: read migration %s: %w", name, err)
			}
			body := string(raw)
			for token, replacement := range typeTokens[dialect] {
				body = strings.ReplaceAll(body, token, replacement)
			}
			out = append(out, migration{version: version, body: body})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
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
