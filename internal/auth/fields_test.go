package auth

import (
	"errors"
	"regexp"
	"testing/fstest"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A plugin account field of this test binary's own, so the sign-up rules for
// fields are exercised here without any plugin: a unique, searchable column
// that takes five to fifteen digits — the shape the first real one had.
const (
	badge     = "badge"
	badgeRule = "test.badge_rule"
)

var badgeRE = regexp.MustCompile(`^[1-9][0-9]{4,14}$`)

func init() {
	user.DefineField(user.Field{
		Key: badge, Unique: true, Searchable: true,
		Validate: func(v string) error {
			if !badgeRE.MatchString(v) {
				return errors.New("five to fifteen digits")
			}
			return nil
		},
	})
}

var badgeMigration = fstest.MapFS{
	"zz_test_badge.sql": {Data: []byte(`ALTER TABLE users ADD COLUMN badge TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX ux_users_badge ON users (badge) WHERE badge <> '';`)},
}

func badgeOf(v string) map[string]string { return map[string]string{badge: v} }
