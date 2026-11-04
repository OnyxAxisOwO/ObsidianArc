package update

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		// Plain releases.
		{"patch behind", "v0.9.2", "v0.9.3", true},
		{"minor behind", "v0.9.2", "v0.10.0", true},
		{"major behind", "v0.9.2", "v1.0.0", true},
		{"same release", "v0.9.2", "v0.9.2", false},
		{"ahead of the latest", "v0.10.0", "v0.9.2", false},
		{"numbers compare as numbers, not text", "v0.9.9", "v0.10.0", true},

		// A build past a tag is that tag for this purpose: the commits
		// after it are not a release yet.
		{"commits past the latest tag", "v0.9.2-64-g356a68e", "v0.9.2", false},
		{"commits past an older tag", "v0.9.2-64-g356a68e", "v0.9.3", true},
		{"dirty tree at the latest tag", "v0.9.2-dirty", "v0.9.2", false},
		{"dirty tree past an older tag", "v0.9.2-64-g356a68e-dirty", "v0.9.3", true},
		{"dirty tree at the tag itself", "v0.9.2-0-g356a68e-dirty", "v0.9.2", false},

		// Pre-releases sit below the release they lead to.
		{"release candidate below its release", "v1.3.0-rc.1", "v1.3.0", true},
		{"release is not behind its candidate", "v1.3.0", "v1.3.0-rc.1", false},
		{"candidate behind the next candidate", "v1.3.0-rc.1", "v1.3.0-rc.2", true},
		{"candidate 2 after candidate 10 is not newer", "v1.3.0-rc.10", "v1.3.0-rc.2", false},
		{"candidate 10 is newer than candidate 2", "v1.3.0-rc.2", "v1.3.0-rc.10", true},
		{"candidate of a later core is newer than a release of an earlier one", "v1.2.9", "v1.3.0-rc.1", true},
		{"a longer identifier list is the later one", "v1.3.0-rc", "v1.3.0-rc.1", true},
		{"numeric identifiers sort below alphanumeric", "v1.3.0-1", "v1.3.0-alpha", true},
		{"alphanumeric identifiers compare by text", "v1.3.0-alpha", "v1.3.0-beta", true},
		{"pre-release of the running core is behind the release", "v1.3.0-rc.1-5-gabc1234", "v1.3.0", true},
		{"pre-release commits keep their candidate", "v1.3.0-rc.1-5-gabc1234", "v1.3.0-rc.1", false},

		// A version nobody can place is never offered an update, and a
		// latest the server cannot read is never offered either.
		{"development string", "dev", "v0.9.3", false},
		{"empty current", "", "v0.9.3", false},
		{"date-stamped build", "v2026.09.29.15.44.24", "v0.9.3", false},
		{"unreadable latest", "v0.9.2", "latest", false},
		{"empty latest", "v0.9.2", "", false},
		{"test build string", "test", "v0.9.2", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Newer(tc.current, tc.latest); got != tc.want {
				t.Errorf("Newer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}
