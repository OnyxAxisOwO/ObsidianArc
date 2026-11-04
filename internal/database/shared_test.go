package database

import (
	"errors"
	"testing"
)

// The words "unique" and "duplicate" alone are not a unique violation: a CHECK
// that mentions a constraint and a duplicate column are both refused by the
// engines, and only the unique-index wording means a name or code is taken.
func TestIsUniqueRecognisesOnlyUniqueIndexViolations(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"sqlite unique index", errors.New("constraint failed: UNIQUE constraint failed: providers.name (2067)"), true},
		{"postgres unique index", errors.New(`ERROR: duplicate key value violates unique constraint "providers_name_key" (SQLSTATE 23505)`), true},
		{"sqlite check", errors.New("CHECK constraint failed: sort_order >= 0"), false},
		{"sqlite not null", errors.New("NOT NULL constraint failed: models.model_id"), false},
		{"foreign key", errors.New("FOREIGN KEY constraint failed"), false},
		{"duplicate column is not a clash", errors.New("duplicate column name: tiers"), false},
		{"non-unique wording", errors.New("index is not unique over this column"), false},
		{"no error", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUnique(tc.err); got != tc.want {
				t.Errorf("IsUnique(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
