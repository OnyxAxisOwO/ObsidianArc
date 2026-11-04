package database

import "strings"

// Placeholders is the "?, ?, ?" list an IN clause of n values takes. Every
// query that builds an IN list uses it, so the spelling is decided in one place.
func Placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
