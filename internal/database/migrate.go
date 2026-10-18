package database

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
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
	if err := db.ensureMigrationsTable(ctx); err != nil {
		return nil, err
	}

	done, err := appliedSet(ctx, db)
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
		err := db.applyMigration(ctx, m)
		if errors.Is(err, errRecordedElsewhere) {
			continue
		}
		if err != nil {
			return applied, err
		}
		applied = append(applied, m.version)
	}
	return applied, nil
}

// MigrateFor brings a database to the schema a backup was taken from: of the
// core's files and the plugins' alike, only the versions the backup's instance
// had applied. Migrate would apply every plugin file it is handed, which is
// wrong here — the schema check that follows compares tables and columns, so
// a plugin the instance never installed, or removed with its data, would
// leave tables behind that the archive does not have.
//
// The first source to hold a version supplies it, so the copy a backup
// carried outranks one the deployment bundles. A version nobody supplies is
// an error that names it: the archive holds tables nothing here can create.
func (db *DB) MigrateFor(ctx context.Context, versions []string, plugins ...fs.FS) ([]string, error) {
	if err := db.ensureMigrationsTable(ctx); err != nil {
		return nil, err
	}
	done, err := appliedSet(ctx, db)
	if err != nil {
		return nil, err
	}
	pending, err := loadMigrations(db.Dialect())
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(versions))
	for _, version := range versions {
		wanted[version] = true
	}
	// The core's files stop at the backup's versions as well. Taking all of them
	// would leave an archive from an older build restored into a newer one at the
	// newer schema, and the schema check that follows would then refuse a
	// destination that can no longer take that archive. The restored server
	// brings its own newer migrations up when it starts.
	core := pending[:0]
	for _, m := range pending {
		if wanted[m.version] {
			core = append(core, m)
		}
	}
	pending = core
	have := make(map[string]bool, len(pending))
	for _, m := range pending {
		have[m.version] = true
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

// ensureMigrationsTable creates the bookkeeping table. PostgreSQL's IF NOT
// EXISTS is an unlocked catalog check, so two processes booting one fresh
// database can both pass it, and the loser fails on pg_type_typname_nsp_index
// (23505). That error is raised only after the winner commits, so the table
// exists when we look again.
func (db *DB) ensureMigrationsTable(ctx context.Context) error {
	_, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at BIGINT NOT NULL
	)`)
	if err == nil {
		return nil
	}
	return recoverMigrationsTable(err, func() error {
		var n int
		return db.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n)
	})
}

// recoverMigrationsTable decides what a failed CREATE TABLE means. The failure
// is a lost race only if the table can be read back; when it cannot, the boot
// error names both failures, because the collision alone would point the
// operator at the wrong cause.
func recoverMigrationsTable(createErr error, probe func() error) error {
	if err := probe(); err != nil {
		return fmt.Errorf("database: create schema_migrations: %w (reading it back also failed: %v)", createErr, err)
	}
	// There is no classifier here for "lost the race" as opposed to any other
	// CREATE failure, and matching driver error text would be brittle, so the
	// readable table is the gate. The error is logged rather than dropped: a
	// failure that was not a race would otherwise show up only at the first
	// claim, with nothing pointing back at this line.
	slog.Warn("database: create schema_migrations failed but the table reads back; continuing",
		"error", createErr)
	return nil
}

// appliedSet returns the versions schema_migrations records. One read of the
// table serves every caller: the table is small, and asking per version costs a
// round trip each.
func appliedSet(ctx context.Context, q Queryer) (map[string]bool, error) {
	rows, err := q.Query(ctx, `SELECT version FROM schema_migrations`)
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

// errRecordedElsewhere means another process applied this migration after
// applyPending read the applied set. Skipping it is the outcome the race should
// have; it is not a failure.
var errRecordedElsewhere = errors.New("database: migration already recorded")

// applyMigration claims the version as the first statement of the transaction
// that runs its DDL. The applied set read by Migrate is a snapshot, and a second
// process booting against the same database can record the version between that
// read and this transaction. Without the claim both would run the DDL, and the
// loser would fail on a table that already exists and refuse to start. On
// SQLite the write lock makes the loser wait for the winner; on PostgreSQL the
// conflicting insert waits for the winner's commit. Either way only one side
// runs the DDL.
func (db *DB) applyMigration(ctx context.Context, m migration) error {
	return db.Tx(ctx, func(tx *Tx) error {
		claimed, err := claimMigration(ctx, tx, m.version)
		if err != nil {
			return err
		}
		if !claimed {
			return errRecordedElsewhere
		}
		return runStatements(ctx, tx, m)
	})
}

// claimMigration records version as applied through q and reports whether this
// caller was the one to record it. The unique key decides: a version another
// process recorded first makes the insert a no-op, so the caller skips it.
func claimMigration(ctx context.Context, q Queryer, version string) (bool, error) {
	res, err := q.Exec(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)
		 ON CONFLICT (version) DO NOTHING`,
		version, time.Now().UnixMilli())
	if err != nil {
		return false, fmt.Errorf("database: claim migration %s: %w", version, err)
	}
	claimed, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("database: claim migration %s: %w", version, err)
	}
	return claimed > 0, nil
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
		// Claimed before the DDL, as applyMigration does. A COUNT read here does not
		// stop a second instance installing the same plugin from running the DDL
		// too: on PostgreSQL the conflicting insert is what waits for the other
		// side's commit, and the loser then skips instead of failing on a table
		// that already exists.
		claimed, err := claimMigration(ctx, tx, m.version)
		if err != nil {
			return applied, err
		}
		if !claimed {
			continue
		}
		if err := runStatements(ctx, tx, m); err != nil {
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

// CoreVersions lists the migrations the core itself ships, sorted: the
// versions no plugin's migration may share a name with, since all of them are
// recorded in one table.
func CoreVersions() ([]string, error) {
	core, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("database: read migrations: %w", err)
	}
	return Versions(core)
}

// Applied reports which of versions schema_migrations records.
func Applied(ctx context.Context, q Queryer, versions []string) (map[string]bool, error) {
	recorded, err := appliedSet(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(versions))
	for _, version := range versions {
		out[version] = recorded[version]
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

// runStatements runs a migration's DDL and records nothing; the caller has
// already claimed the version or records it itself.
func runStatements(ctx context.Context, q Queryer, m migration) error {
	for i, stmt := range splitStatements(m.body) {
		if _, err := q.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("database: migration %s statement %d: %w", m.version, i+1, err)
		}
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
