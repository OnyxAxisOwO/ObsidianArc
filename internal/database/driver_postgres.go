package database

// pgx's database/sql shim rather than its native interface: every query in
// this project is plain SQL over database/sql, and going through the shim is
// what lets the same store code serve both engines.
import _ "github.com/jackc/pgx/v5/stdlib"
