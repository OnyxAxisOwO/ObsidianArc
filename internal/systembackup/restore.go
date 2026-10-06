package systembackup

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

// VerifyArchiveKey lets the offline command reject a missing or wrong master
// key before it creates or migrates a destination database.
func VerifyArchiveKey(path string, masterKey []byte) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("system backup: open restore file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("system backup: inspect restore file: %w", err)
	}
	if info.Size() < 1 || info.Size() > MaxArchiveBytes {
		return fmt.Errorf("system backup: archive must be between 1 byte and %d bytes", MaxArchiveBytes)
	}
	_, manifest, err := readManifest(file, info.Size())
	if err != nil {
		return err
	}
	return verifyKeyCheck(manifest, masterKey)
}

// PrepareRestore migrates an empty destination to the schema the archive was
// taken from, which is not always this binary's own: the instance may have had
// plugin packages installed, and their tables and columns are in the archive
// with nothing in a fresh database to create them.
//
// The packages the archive carries (plugin_packages) supply their own
// migrations. extra is what this binary and its deployment can supply —
// compiled-in plugins, bundled packages — and is consulted only for the
// versions the archive names, after the archive's own packages, so an
// instance that removed a plugin's data or never installed it does not get its
// tables back and fail the schema check on them.
func PrepareRestore(ctx context.Context, db *database.DB, path string, masterKey []byte, extra ...fs.FS) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("system backup: open restore file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("system backup: inspect restore file: %w", err)
	}
	if info.Size() < 1 || info.Size() > MaxArchiveBytes {
		return fmt.Errorf("system backup: archive must be between 1 byte and %d bytes", MaxArchiveBytes)
	}
	archive, manifest, err := readManifest(file, info.Size())
	if err != nil {
		return err
	}
	if manifest.Dialect != db.Dialect() {
		return fmt.Errorf("system backup: archive uses %s but destination uses %s; restore with the same database engine", manifest.Dialect, db.Dialect())
	}
	// Before running any SQL the archive brought along, not after.
	if err := verifyKeyCheck(manifest, masterKey); err != nil {
		return err
	}
	carried, err := archivedPackages(ctx, archive, manifest)
	if err != nil {
		return err
	}
	_, err = db.MigrateFor(ctx, manifest.Migration, append(carried, extra...)...)
	return err
}

// archivedPackages reads the plugin packages out of the archive's
// plugin_packages table and returns each one's migrations. An archive from
// before packages existed has no such table, which is not an error.
func archivedPackages(ctx context.Context, archive *zip.Reader, manifest archiveManifest) ([]fs.FS, error) {
	const table, column = "plugin_packages", "archive"
	meta := manifestTable(manifest, table)
	entry := archiveEntries(archive)[table]
	if meta.Name == "" || entry == nil {
		return nil, nil
	}
	position := -1
	for i, name := range meta.Columns {
		if name == column {
			position = i
		}
	}
	if position < 0 {
		return nil, fmt.Errorf("system backup: %s has no %s column", table, column)
	}
	var sources []fs.FS
	err := scanArchiveRows(ctx, entry, len(meta.Columns), func(cells []archiveCell) error {
		value, err := decodeCell(cells[position])
		if err != nil {
			return fmt.Errorf("system backup: decode %s row: %w", table, err)
		}
		raw, ok := value.([]byte)
		if !ok {
			return fmt.Errorf("system backup: %s.%s is not binary", table, column)
		}
		pkg, err := arcx.Parse(raw)
		if err != nil {
			return fmt.Errorf("system backup: a plugin package in the archive is not valid: %w", err)
		}
		if pkg.HasMigrations() {
			sources = append(sources, pkg.Migrations())
		}
		return nil
	})
	return sources, err
}

// RestoreArchive replaces an empty, migrated database from one instance
// archive. The caller owns the offline requirement; this function still
// rechecks emptiness inside the same transaction as the imports.
func RestoreArchive(ctx context.Context, db *database.DB, path string, masterKey []byte) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("system backup: open restore file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("system backup: inspect restore file: %w", err)
	}
	if info.Size() < 1 || info.Size() > MaxArchiveBytes {
		return fmt.Errorf("system backup: archive must be between 1 byte and %d bytes", MaxArchiveBytes)
	}
	archive, manifest, err := readManifest(file, info.Size())
	if err != nil {
		return err
	}
	if manifest.Dialect != db.Dialect() {
		return fmt.Errorf("system backup: archive uses %s but destination uses %s; restore with the same database engine", manifest.Dialect, db.Dialect())
	}
	if err := verifyKeyCheck(manifest, masterKey); err != nil {
		return err
	}
	tables, err := tableNames(manifest)
	if err != nil {
		return err
	}
	if err := validateArchiveEntries(archive, tables); err != nil {
		return err
	}

	return db.Tx(ctx, func(tx *database.Tx) error {
		// Serialize empty-target checks with restores and backup scheduler claims
		// across processes, not only among callers sharing this binary.
		if err := lockInstance(ctx, tx); err != nil {
			return err
		}
		if err := validateDestination(ctx, tx, manifest, tables); err != nil {
			return err
		}
		if err := requireEmptyDatabase(ctx, tx); err != nil {
			return err
		}
		if err := resetDestinationScheduler(ctx, tx); err != nil {
			return err
		}
		order, selfRefs, err := restoreOrder(ctx, tx, tables)
		if err != nil {
			return err
		}
		entryByTable := archiveEntries(archive)
		for _, tableName := range order {
			table := manifestTable(manifest, tableName)
			if err := insertTable(ctx, tx, table, entryByTable[tableName], selfRefs[tableName]); err != nil {
				return err
			}
		}
		for tableName, refs := range selfRefs {
			if len(refs) == 0 {
				continue
			}
			table := manifestTable(manifest, tableName)
			pks, err := database.PrimaryKeys(ctx, tx, tableName)
			if err != nil {
				return err
			}
			if err := restoreSelfReferences(ctx, tx, table, entryByTable[tableName], refs, pks); err != nil {
				return err
			}
		}
		// The instance lock row is coordination state, not an operator setting.
		// Releasing the database row lock still waits until commit, but removing
		// its marker keeps it out of the restored settings table.
		if _, err := tx.Exec(ctx, `DELETE FROM settings WHERE key = ?`, schedulerLockKey); err != nil {
			return fmt.Errorf("system backup: release restore marker: %w", err)
		}
		return nil
	})
}

// A nil key means the operator has declared the original lost and chosen to
// restore without it: everything the key sealed (stored provider credentials,
// second-factor secrets) stays unreadable and has to be entered again, but the
// rest of the archive is plain data and is worth more than refusing.
func verifyKeyCheck(manifest archiveManifest, masterKey []byte) error {
	if masterKey == nil {
		return nil
	}
	if manifest.KeyCheck == "" {
		return errors.New("system backup: archive has no instance-key check; it may be from an unsupported format")
	}
	sealed, err := base64Decode(manifest.KeyCheck)
	if err != nil {
		return errors.New("system backup: archive has an invalid instance-key check")
	}
	box, err := secret.New(masterKey, archiveKeyPurpose)
	if err != nil {
		return fmt.Errorf("system backup: prepare key check: %w", err)
	}
	plain, err := box.Open(sealed)
	if err != nil || plain != archiveKeySentinel {
		return errors.New("system backup: the configured OBSIDIAN_SECRET_KEY does not match this archive")
	}
	return nil
}

func base64Decode(value string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(value)
}

func validateArchiveEntries(archive *zip.Reader, tables []string) error {
	if len(archive.File) != len(tables)+1 {
		return errors.New("system backup: archive has missing or unexpected entries")
	}
	if uint64(len(archive.File)) > 10001 {
		return errors.New("system backup: archive contains too many entries")
	}
	expected := map[string]bool{manifestEntryName: true}
	for _, table := range tables {
		expected[archiveEntryPrefix+table+archiveEntrySuffix] = true
	}
	var expanded uint64
	for _, entry := range archive.File {
		if !expected[entry.Name] || entry.FileInfo().IsDir() || entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("system backup: unexpected archive entry %q", entry.Name)
		}
		delete(expected, entry.Name)
		if entry.UncompressedSize64 > maxArchiveExpanded-expanded {
			return errors.New("system backup: expanded archive exceeds the 8 GiB restore limit")
		}
		expanded += entry.UncompressedSize64
	}
	if len(expected) > 0 {
		return errors.New("system backup: archive is missing one or more table entries")
	}
	return nil
}

func resetDestinationScheduler(ctx context.Context, tx database.Queryer) error {
	var leaseUntil int64
	err := tx.QueryRow(ctx, `SELECT lease_until FROM system_backups WHERE id = ?`, singletonID).Scan(&leaseUntil)
	if err != nil && !database.IsNotFound(err) {
		return fmt.Errorf("system backup: inspect destination scheduler: %w", err)
	}
	if leaseUntil > time.Now().UnixMilli() {
		return errors.New("system backup: stop all instances using this database before restore")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM system_backups WHERE id = ?`, singletonID); err != nil {
		return fmt.Errorf("system backup: clear destination scheduler: %w", err)
	}
	return nil
}

func validateDestination(ctx context.Context, tx database.Queryer, manifest archiveManifest, tables []string) error {
	if len(manifest.Migration) == 0 {
		return errors.New("system backup: archive has no migration history")
	}
	seenMigrations := make(map[string]bool, len(manifest.Migration))
	for _, version := range manifest.Migration {
		if version == "" || seenMigrations[version] {
			return errors.New("system backup: archive has invalid or duplicate migration history")
		}
		seenMigrations[version] = true
	}
	currentMigrations, err := database.AppliedVersions(ctx, tx)
	if err != nil {
		return err
	}
	if !equalStrings(currentMigrations, manifest.Migration) {
		return errors.New("system backup: migrate the destination with the same Obsidian Arc version used to create this archive")
	}
	currentTables, err := database.Tables(ctx, tx)
	if err != nil {
		return err
	}
	var currentDataTables []string
	for _, table := range currentTables {
		if table != "schema_migrations" && table != "system_backups" {
			currentDataTables = append(currentDataTables, table)
		}
	}
	sort.Strings(currentDataTables)
	if !equalStrings(currentDataTables, tables) {
		return fmt.Errorf("system backup: destination schema has a different table set (destination has %v, archive has %v)", currentDataTables, tables)
	}
	for _, table := range manifest.Tables {
		columns, err := database.Columns(ctx, tx, table.Name)
		if err != nil {
			return err
		}
		if !equalStrings(columns, table.Columns) {
			return fmt.Errorf("system backup: destination columns for %s do not match the archive", table.Name)
		}
	}
	return nil
}

func requireEmptyDatabase(ctx context.Context, tx database.Queryer) error {
	tables, err := database.Tables(ctx, tx)
	if err != nil {
		return err
	}
	for _, table := range tables {
		if table == "schema_migrations" || table == "system_backups" {
			continue
		}
		var count int64
		query := `SELECT COUNT(*) FROM "` + table + `"`
		var args []any
		if table == "settings" {
			query += ` WHERE key <> ?`
			args = append(args, schedulerLockKey)
		}
		if err := tx.QueryRow(ctx, query, args...).Scan(&count); err != nil {
			return fmt.Errorf("system backup: inspect destination table %s: %w", table, err)
		}
		if count != 0 {
			return fmt.Errorf("system backup: destination table %s is not empty; restore into a fresh database", table)
		}
	}
	return nil
}

func restoreOrder(ctx context.Context, tx database.Queryer, tableNames []string) ([]string, map[string][]database.ForeignKey, error) {
	known := make(map[string]bool, len(tableNames))
	for _, table := range tableNames {
		known[table] = true
	}
	dependencies := make(map[string]map[string]bool, len(tableNames))
	selfRefs := make(map[string][]database.ForeignKey)
	for _, table := range tableNames {
		dependencies[table] = make(map[string]bool)
		keys, err := database.ForeignKeys(ctx, tx, table)
		if err != nil {
			return nil, nil, err
		}
		for _, key := range keys {
			if !known[key.RefTable] {
				return nil, nil, fmt.Errorf("system backup: %s references missing table %s", table, key.RefTable)
			}
			if key.RefTable == table {
				selfRefs[table] = append(selfRefs[table], key)
			} else {
				dependencies[table][key.RefTable] = true
			}
		}
	}

	var order []string
	remaining := len(dependencies)
	for remaining > 0 {
		ready := make([]string, 0)
		for table, deps := range dependencies {
			if deps == nil {
				continue
			}
			resolved := true
			for dependency := range deps {
				if dependencies[dependency] != nil {
					resolved = false
					break
				}
			}
			if resolved {
				ready = append(ready, table)
			}
		}
		if len(ready) == 0 {
			return nil, nil, errors.New("system backup: schema contains a cross-table foreign-key cycle")
		}
		sort.Strings(ready)
		for _, table := range ready {
			order = append(order, table)
			dependencies[table] = nil
			remaining--
		}
	}
	return order, selfRefs, nil
}

func insertTable(ctx context.Context, tx database.Queryer, table archiveTable, entry *zip.File, selfRefs []database.ForeignKey) error {
	positions := make(map[string]int, len(table.Columns))
	for index, column := range table.Columns {
		positions[column] = index
	}
	quoted := make([]string, len(table.Columns))
	placeholders := make([]string, len(table.Columns))
	for i, column := range table.Columns {
		quoted[i] = `"` + column + `"`
		placeholders[i] = "?"
	}
	query := `INSERT INTO "` + table.Name + `" (` + strings.Join(quoted, ",") + `) VALUES (` + strings.Join(placeholders, ",") + `)`
	return scanArchiveRows(ctx, entry, len(table.Columns), func(cells []archiveCell) error {
		values := make([]any, len(cells))
		for i, cell := range cells {
			value, err := decodeCell(cell)
			if err != nil {
				return fmt.Errorf("system backup: decode %s row: %w", table.Name, err)
			}
			values[i] = value
		}
		for _, key := range selfRefs {
			index, ok := positions[key.Column]
			if !ok {
				return fmt.Errorf("system backup: self-reference column %s.%s is missing", table.Name, key.Column)
			}
			values[index] = nil
		}
		if _, err := tx.Exec(ctx, query, values...); err != nil {
			return fmt.Errorf("system backup: insert %s: %w", table.Name, err)
		}
		return nil
	})
}

func restoreSelfReferences(ctx context.Context, tx database.Queryer, table archiveTable, entry *zip.File, refs []database.ForeignKey, pks []string) error {
	positions := make(map[string]int, len(table.Columns))
	for index, column := range table.Columns {
		positions[column] = index
	}
	pkPositions := make([]int, len(pks))
	for i, pk := range pks {
		position, ok := positions[pk]
		if !ok {
			return fmt.Errorf("system backup: primary key column %s.%s is missing", table.Name, pk)
		}
		pkPositions[i] = position
	}
	for _, key := range refs {
		columnPosition, ok := positions[key.Column]
		if !ok {
			return fmt.Errorf("system backup: self-reference column %s.%s is missing", table.Name, key.Column)
		}
		pkWhere := make([]string, len(pks))
		for i, pk := range pks {
			pkWhere[i] = `"` + pk + `" = ?`
		}
		query := `UPDATE "` + table.Name + `" SET "` + key.Column + `" = ? WHERE ` + strings.Join(pkWhere, " AND ")
		if err := scanArchiveRows(ctx, entry, len(table.Columns), func(cells []archiveCell) error {
			value, err := decodeCell(cells[columnPosition])
			if err != nil {
				return fmt.Errorf("system backup: decode %s.%s: %w", table.Name, key.Column, err)
			}
			if value == nil {
				return nil
			}
			args := make([]any, 1+len(pkPositions))
			args[0] = value
			for i, position := range pkPositions {
				args[i+1], err = decodeCell(cells[position])
				if err != nil {
					return fmt.Errorf("system backup: decode %s primary key: %w", table.Name, err)
				}
			}
			result, err := tx.Exec(ctx, query, args...)
			if err != nil {
				return fmt.Errorf("system backup: restore self-reference %s.%s: %w", table.Name, key.Column, err)
			}
			changed, err := result.RowsAffected()
			if err != nil || changed != 1 {
				return fmt.Errorf("system backup: could not identify row for %s.%s", table.Name, key.Column)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func archiveEntries(archive *zip.Reader) map[string]*zip.File {
	entries := make(map[string]*zip.File, len(archive.File))
	for _, entry := range archive.File {
		if table, ok := entryTableName(entry.Name); ok {
			entries[table] = entry
		}
	}
	return entries
}

func manifestTable(manifest archiveManifest, name string) archiveTable {
	for _, table := range manifest.Tables {
		if table.Name == name {
			return table
		}
	}
	return archiveTable{}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
