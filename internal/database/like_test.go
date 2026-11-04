package database

import (
	"context"
	"slices"
	"testing"
)

func TestEscapeLike(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"a_b", `a\_b`},
		{"100%", `100\%`},
		{`a\b`, `a\\b`},
		// The backslash is escaped first. Done last, it would double the escapes
		// added in front of _ and %, and they would no longer escape anything.
		{`a_b%c\d`, `a\_b\%c\\d`},
	} {
		if got := EscapeLike(tc.in); got != tc.want {
			t.Errorf("EscapeLike(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// SQLite has no default escape character, so the clause that declares one is
// what makes EscapeLike mean anything. A typed underscore has to find that
// underscore and nothing it would have matched as a wildcard.
func TestEscapeLikeMatchesTheTypedCharacterOnly(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if _, err := db.Exec(ctx, `CREATE TABLE like_probe (name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a_b", "axb", "xa_by", "100%", "1000", `a\b`, "aab"} {
		if _, err := db.Exec(ctx, `INSERT INTO like_probe (name) VALUES (?)`, name); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		typed string
		want  []string
	}{
		{"a_b", []string{"a_b", "xa_by"}},
		{"100%", []string{"100%"}},
		{`a\b`, []string{`a\b`}},
	} {
		got := likeProbe(t, db, tc.typed)
		if !slices.Equal(got, tc.want) {
			t.Errorf("LIKE for %q matched %q, want %q", tc.typed, got, tc.want)
		}
	}
}

func likeProbe(t *testing.T, db *DB, typed string) []string {
	t.Helper()
	rows, err := db.Query(context.Background(),
		`SELECT name FROM like_probe WHERE name LIKE ? ESCAPE '\' ORDER BY name`,
		"%"+EscapeLike(typed)+"%")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}
