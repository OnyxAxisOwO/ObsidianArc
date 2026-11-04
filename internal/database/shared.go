package database

import "strings"

// RowScanner is what a QueryRow or a Rows can both be scanned through, so one
// decoder serves a single row and a row of a listing.
type RowScanner interface{ Scan(dest ...any) error }

// IsUnique reports whether err is a unique-index violation on either engine.
//
// It matches each engine's own wording rather than the bare words "unique" and
// "duplicate". A CHECK, NOT NULL or foreign-key failure can say "constraint" and
// a duplicate column name can say "duplicate", and reading either as a clash
// sends the caller to the wrong explanation.
func IsUnique(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint failed") || // SQLite
		strings.Contains(message, "violates unique constraint") || // PostgreSQL
		strings.Contains(message, "duplicate key value") // PostgreSQL
}
