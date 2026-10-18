package plugin

import "testing"

// A package's version decides whether a bundled copy replaces an installed one,
// and whether an upload is a downgrade. Both follow SemVer precedence, so a
// release candidate is before the release it leads up to, and build metadata
// does not count.
func TestPackageVersionsOrderBySemVerPrecedence(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.4.0-rc.1", "1.4.0", -1},
		{"1.4.0-rc.2", "1.4.0-rc.10", -1},
		{"1.4.0-alpha", "1.4.0-alpha.1", -1},
		{"1.4.0-alpha.beta", "1.4.0-alpha.1", 1},
		{"1.4.0-beta", "1.4.0-alpha", 1},
		{"1.4.0-rc-1", "1.4.0-rc-2", -1},
		{"1.4.0-99999999999999999999999", "1.4.0-100000000000000000000000", -1},
		{"1.4.0+build.7", "1.4.0", 0},
		{"1.4.0-rc.1+build.7", "1.4.0-rc.1+build.9", 0},
		{"1.10.0", "1.9.9", 1},
		{"1.4", "1.4.0", 0},
	} {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := compareVersions(tc.b, tc.a); got != -tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.b, tc.a, got, -tc.want)
		}
	}
}
