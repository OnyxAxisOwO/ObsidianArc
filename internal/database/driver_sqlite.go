//go:build !nosqlite

package database

// modernc.org/sqlite is a pure-Go translation of SQLite: no cgo, so the
// server still cross-compiles to a static single binary. That is the only
// reason it is preferred over the cgo bindings, which are faster but would
// cost the deployment story this project is built around.
//
// Building with `-tags nosqlite` drops it (and several megabytes of binary)
// for a Postgres-only deployment.
import _ "modernc.org/sqlite"

const (
	sqliteEnabled    = true
	sqliteDriverName = "sqlite"
)
