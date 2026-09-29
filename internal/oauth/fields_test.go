package oauth

import (
	"errors"
	"regexp"
	"testing/fstest"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// An account field of this test binary's own, bound to the OpenID Connect
// subject the way a plugin would bind one, so the subject binding is
// exercised here without any plugin: five to fifteen digits, unique — the
// shape the first real binding had.
const (
	badge     = "badge"
	badgeRule = "test.badge_rule"
)

var badgeRE = regexp.MustCompile(`^[1-9][0-9]{4,14}$`)

func init() {
	user.DefineField(user.Field{
		Key: badge, Unique: true,
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

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
