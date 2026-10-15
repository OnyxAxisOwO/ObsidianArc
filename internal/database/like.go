package database

import "strings"

// EscapeLike neutralises the LIKE wildcards in a value a person typed, so a
// search for "_" finds an underscore instead of every row. The query has to
// declare the escape character, as `LIKE ? ESCAPE '\'`. An unescaped _ or %
// is a wildcard on both engines, and SQLite has no default escape character,
// so the clause is declared everywhere rather than relying on a default.
func EscapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "%", `\%`)
	return strings.ReplaceAll(value, "_", `\_`)
}
