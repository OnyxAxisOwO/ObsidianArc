package systembackup

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

const (
	archiveFormat      = 1
	archiveKeyPurpose  = "obsidian-arc/system-backup-key-check"
	archiveKeySentinel = "Obsidian Arc instance backup key check v1"
	// Twice the largest attachment an operator may configure
	// (settings.MaxAttachmentCeilingMB): a blob travels base64-encoded, which
	// inflates it by a third, and the JSONL wrapper adds a little more. At
	// the old 64 MiB an attachment at the very ceiling failed every backup
	// for as long as the row existed. The restore side reads rows through a
	// scanner bounded by this same constant, so one number keeps both halves
	// of the round trip in step.
	maxArchiveRowBytes = 128 << 20
	maxArchiveExpanded = 8 << 30
	manifestEntryName  = "manifest.json"
	archiveEntryPrefix = "data/"
	archiveEntrySuffix = ".jsonl"
)

var ErrArchiveTooLarge = errors.New("instance backup exceeds the 4 GiB staging limit")

type archiveManifest struct {
	Format      int              `json:"format"`
	CreatedAt   int64            `json:"created_at"`
	Application string           `json:"application"`
	Dialect     database.Dialect `json:"dialect"`
	Migration   []string         `json:"migrations"`
	KeyCheck    string           `json:"key_check"`
	Tables      []archiveTable   `json:"tables"`
}

type archiveTable struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
}

type archiveCell struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

type cappedWriter struct {
	writer io.Writer
	max    int64
	used   int64
}

type archiveBudget struct{ expanded uint64 }

func (b *archiveBudget) add(size uint64) error {
	if size > maxArchiveExpanded-b.expanded {
		return errors.New("system backup exceeds the 8 GiB expanded-archive limit")
	}
	b.expanded += size
	return nil
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	remaining := w.max - w.used
	if remaining <= 0 {
		return 0, ErrArchiveTooLarge
	}
	if int64(len(p)) > remaining {
		written, err := w.writer.Write(p[:remaining])
		w.used += int64(written)
		if err != nil {
			return written, err
		}
		return written, ErrArchiveTooLarge
	}
	written, err := w.writer.Write(p)
	w.used += int64(written)
	return written, err
}

func writeArchive(ctx context.Context, db *database.DB, output *os.File, masterKey []byte, version string) error {
	limited := &cappedWriter{writer: output, max: MaxArchiveBytes}
	archive := zip.NewWriter(limited)
	var budget archiveBudget
	writeErr := db.ReadSnapshotBounded(ctx, MaxArchiveBytes, func(snapshot database.Queryer) error {
		tables, err := database.Tables(ctx, snapshot)
		if err != nil {
			return err
		}
		filtered := make([]string, 0, len(tables))
		manifest := archiveManifest{
			Format: archiveFormat, CreatedAt: time.Now().UnixMilli(),
			Application: version, Dialect: snapshot.Dialect(),
		}
		for _, table := range tables {
			// These rows describe the destination schema and scheduler itself.
			// Restoring a source scheduler would start writing to its old bucket.
			if table == "schema_migrations" || table == "system_backups" {
				continue
			}
			columns, err := database.Columns(ctx, snapshot, table)
			if err != nil {
				return err
			}
			filtered = append(filtered, table)
			manifest.Tables = append(manifest.Tables, archiveTable{Name: table, Columns: columns})
		}
		manifest.Migration, err = database.AppliedVersions(ctx, snapshot)
		if err != nil {
			return err
		}
		keyBox, err := secret.New(masterKey, archiveKeyPurpose)
		if err != nil {
			return fmt.Errorf("system backup: prepare key check: %w", err)
		}
		check, err := keyBox.Seal(archiveKeySentinel)
		if err != nil {
			return fmt.Errorf("system backup: seal key check: %w", err)
		}
		manifest.KeyCheck = base64.StdEncoding.EncodeToString(check)

		entry, err := newZipEntry(archive, manifestEntryName)
		if err != nil {
			return err
		}
		manifestBytes, err := json.Marshal(manifest)
		if err != nil {
			return fmt.Errorf("system backup: encode archive manifest: %w", err)
		}
		manifestBytes = append(manifestBytes, '\n')
		if len(manifestBytes) > 8<<20 {
			return errors.New("system backup: manifest exceeds the 8 MiB limit")
		}
		if err := budget.add(uint64(len(manifestBytes))); err != nil {
			return err
		}
		if _, err := entry.Write(manifestBytes); err != nil {
			return fmt.Errorf("system backup: write archive manifest: %w", err)
		}
		for i, table := range filtered {
			if err := writeTable(ctx, snapshot, archive, table, manifest.Tables[i].Columns, &budget); err != nil {
				return err
			}
		}
		return nil
	})
	closeErr := archive.Close()
	if writeErr != nil {
		if errors.Is(writeErr, ErrArchiveTooLarge) || errors.Is(closeErr, ErrArchiveTooLarge) {
			return ErrArchiveTooLarge
		}
		return writeErr
	}
	if closeErr != nil {
		if errors.Is(closeErr, ErrArchiveTooLarge) {
			return ErrArchiveTooLarge
		}
		return fmt.Errorf("system backup: close archive: %w", closeErr)
	}
	return nil
}

func newZipEntry(archive *zip.Writer, name string) (io.Writer, error) {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0600)
	header.SetModTime(time.Now())
	entry, err := archive.CreateHeader(header)
	if err != nil {
		return nil, fmt.Errorf("system backup: create archive entry %s: %w", name, err)
	}
	return entry, nil
}

func writeTable(ctx context.Context, q database.Queryer, archive *zip.Writer, table string, columns []string, budget *archiveBudget) error {
	entryName := archiveEntryPrefix + table + archiveEntrySuffix
	entry, err := newZipEntry(archive, entryName)
	if err != nil {
		return err
	}
	quotedColumns := make([]string, len(columns))
	for i, column := range columns {
		quotedColumns[i] = `"` + column + `"`
	}
	query := `SELECT ` + strings.Join(quotedColumns, ",") + ` FROM "` + table + `"`
	var args []any
	if table == "settings" {
		// This row only coordinates scheduler/restore writers across replicas.
		// It is recreated by the destination and must not be replayed as data.
		query += ` WHERE key <> ?`
		args = append(args, schedulerLockKey)
	}
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("system backup: read table %s: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return fmt.Errorf("system backup: scan table %s: %w", table, err)
		}
		encoded := make([]archiveCell, len(values))
		for i, value := range values {
			encoded[i], err = encodeCell(value)
			if err != nil {
				return fmt.Errorf("system backup: encode %s.%s: %w", table, columns[i], err)
			}
		}
		row, err := json.Marshal(encoded)
		if err != nil {
			return fmt.Errorf("system backup: encode table %s: %w", table, err)
		}
		if len(row)+1 > maxArchiveRowBytes {
			return fmt.Errorf("system backup: a row in %s exceeds the %d MiB row limit", table, maxArchiveRowBytes>>20)
		}
		row = append(row, '\n')
		if err := budget.add(uint64(len(row))); err != nil {
			return err
		}
		if _, err := entry.Write(row); err != nil {
			if errors.Is(err, ErrArchiveTooLarge) {
				return ErrArchiveTooLarge
			}
			return fmt.Errorf("system backup: write table %s: %w", table, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("system backup: read table %s: %w", table, err)
	}
	return nil
}

func encodeCell(value any) (archiveCell, error) {
	switch value := value.(type) {
	case nil:
		return archiveCell{Kind: "null"}, nil
	case string:
		return archiveCell{Kind: "text", Value: value}, nil
	case []byte:
		return archiveCell{Kind: "blob", Value: base64.StdEncoding.EncodeToString(value)}, nil
	case int64:
		return archiveCell{Kind: "int", Value: strconv.FormatInt(value, 10)}, nil
	case int32:
		return archiveCell{Kind: "int", Value: strconv.FormatInt(int64(value), 10)}, nil
	case int:
		return archiveCell{Kind: "int", Value: strconv.Itoa(value)}, nil
	case float64:
		return archiveCell{Kind: "float", Value: strconv.FormatFloat(value, 'g', -1, 64)}, nil
	case bool:
		return archiveCell{Kind: "bool", Value: strconv.FormatBool(value)}, nil
	case time.Time:
		return archiveCell{Kind: "time", Value: value.UTC().Format(time.RFC3339Nano)}, nil
	default:
		return archiveCell{}, fmt.Errorf("unsupported database value %T", value)
	}
}

func decodeCell(cell archiveCell) (any, error) {
	switch cell.Kind {
	case "null":
		if cell.Value != "" {
			return nil, errors.New("null cell has a value")
		}
		return nil, nil
	case "text":
		return cell.Value, nil
	case "blob":
		value, err := base64.StdEncoding.DecodeString(cell.Value)
		if err != nil {
			return nil, fmt.Errorf("invalid blob encoding: %w", err)
		}
		return value, nil
	case "int":
		value, err := strconv.ParseInt(cell.Value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid integer: %w", err)
		}
		return value, nil
	case "float":
		value, err := strconv.ParseFloat(cell.Value, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid float: %w", err)
		}
		return value, nil
	case "bool":
		value, err := strconv.ParseBool(cell.Value)
		if err != nil {
			return nil, fmt.Errorf("invalid boolean: %w", err)
		}
		return value, nil
	case "time":
		value, err := time.Parse(time.RFC3339Nano, cell.Value)
		if err != nil {
			return nil, fmt.Errorf("invalid time: %w", err)
		}
		return value, nil
	default:
		return nil, fmt.Errorf("unknown cell type %q", cell.Kind)
	}
}

func hashFile(file *os.File) (int64, string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, "", fmt.Errorf("system backup: rewind staged archive: %w", err)
	}
	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return 0, "", fmt.Errorf("system backup: hash staged archive: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, "", fmt.Errorf("system backup: rewind staged archive: %w", err)
	}
	return size, hex.EncodeToString(hasher.Sum(nil)), nil
}

func readManifest(file *os.File, size int64) (*zip.Reader, archiveManifest, error) {
	archive, err := zip.NewReader(file, size)
	if err != nil {
		return nil, archiveManifest{}, fmt.Errorf("system backup: open archive: %w", err)
	}
	if len(archive.File) == 0 || archive.File[0].Name != manifestEntryName {
		return nil, archiveManifest{}, errors.New("system backup: archive manifest is missing or not first")
	}
	if archive.File[0].UncompressedSize64 > 8<<20 {
		return nil, archiveManifest{}, errors.New("system backup: manifest is too large")
	}
	manifestFile, err := archive.File[0].Open()
	if err != nil {
		return nil, archiveManifest{}, fmt.Errorf("system backup: open manifest: %w", err)
	}
	defer manifestFile.Close()
	manifestData, err := io.ReadAll(io.LimitReader(manifestFile, (8<<20)+1))
	if err != nil {
		return nil, archiveManifest{}, fmt.Errorf("system backup: read manifest: %w", err)
	}
	if len(manifestData) > 8<<20 {
		return nil, archiveManifest{}, errors.New("system backup: manifest is too large")
	}
	var manifest archiveManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, archiveManifest{}, fmt.Errorf("system backup: decode manifest: %w", err)
	}
	if manifest.Format != archiveFormat {
		return nil, archiveManifest{}, fmt.Errorf("system backup: unsupported archive format %d", manifest.Format)
	}
	return archive, manifest, nil
}

func tableNames(manifest archiveManifest) ([]string, error) {
	names := make([]string, 0, len(manifest.Tables))
	seenTables := make(map[string]bool, len(manifest.Tables))
	for _, table := range manifest.Tables {
		if !validIdentifier(table.Name) || table.Name == "schema_migrations" || table.Name == "system_backups" || seenTables[table.Name] {
			return nil, fmt.Errorf("system backup: invalid or duplicate table %q", table.Name)
		}
		seenTables[table.Name] = true
		seenColumns := make(map[string]bool, len(table.Columns))
		for _, column := range table.Columns {
			if !validIdentifier(column) || seenColumns[column] {
				return nil, fmt.Errorf("system backup: invalid or duplicate column %q in %s", column, table.Name)
			}
			seenColumns[column] = true
		}
		if len(table.Columns) == 0 {
			return nil, fmt.Errorf("system backup: table %s has no columns", table.Name)
		}
		names = append(names, table.Name)
	}
	sort.Strings(names)
	return names, nil
}

func validIdentifier(value string) bool {
	if value == "" || !(value[0] == '_' || value[0] >= 'a' && value[0] <= 'z' || value[0] >= 'A' && value[0] <= 'Z') {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func entryTableName(name string) (string, bool) {
	if !strings.HasPrefix(name, archiveEntryPrefix) || !strings.HasSuffix(name, archiveEntrySuffix) {
		return "", false
	}
	table := strings.TrimSuffix(strings.TrimPrefix(name, archiveEntryPrefix), archiveEntrySuffix)
	return table, validIdentifier(table)
}

func scanArchiveRows(ctx context.Context, file *zip.File, columns int, visit func([]archiveCell) error) error {
	reader, err := file.Open()
	if err != nil {
		return fmt.Errorf("system backup: open %s: %w", file.Name, err)
	}
	defer reader.Close()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxArchiveRowBytes)
	line := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line++
		var cells []archiveCell
		if err := json.Unmarshal(scanner.Bytes(), &cells); err != nil {
			return fmt.Errorf("system backup: %s row %d: %w", file.Name, line, err)
		}
		if len(cells) != columns {
			return fmt.Errorf("system backup: %s row %d has %d values, want %d", file.Name, line, len(cells), columns)
		}
		if err := visit(cells); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("system backup: read %s: %w", file.Name, err)
	}
	return nil
}
