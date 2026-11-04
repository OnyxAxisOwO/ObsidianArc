package update

import (
	"regexp"
	"strconv"
	"strings"
)

// Newer reports whether latest names a release later than current.
//
// A build cut from a commit past a tag reports git describe's suffix:
// v0.9.2-64-g356a68e is sixty-four commits after v0.9.2, and it is 0.9.2 for
// this purpose. Those commits are not a release, so their own build is not
// behind the tag it grew from. A build that is not a release at all, such as
// a development string, cannot be placed, so nothing is offered for it.
func Newer(current, latest string) bool {
	have, okHave := parse(current)
	want, okWant := parse(latest)
	if !okHave || !okWant {
		return false
	}
	return compare(want, have) > 0
}

type version struct {
	core [3]int
	// The dot-separated identifiers after a hyphen, nil for a release.
	pre []string
}

var (
	// `git describe --long --dirty` appends "-N-gHASH" and "-dirty". The
	// dirty marker is stripped first, then the commit count and hash.
	describeSuffix = regexp.MustCompile(`^(.+)-\d+-g[0-9a-f]+$`)
	semver         = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$`)
)

func parse(raw string) (version, bool) {
	s := strings.TrimPrefix(strings.TrimSpace(raw), "v")
	s = strings.TrimSuffix(s, "-dirty")
	if match := describeSuffix.FindStringSubmatch(s); match != nil {
		s = match[1]
	}

	match := semver.FindStringSubmatch(s)
	if match == nil {
		return version{}, false
	}
	var v version
	for i := range v.core {
		n, err := strconv.Atoi(match[i+1])
		if err != nil {
			return version{}, false
		}
		v.core[i] = n
	}
	if match[4] != "" {
		v.pre = strings.Split(match[4], ".")
	}
	return v, true
}

// compare follows semantic versioning precedence: numbers first, then a
// release above every pre-release of the same numbers, then the identifiers
// of the pre-release one by one.
func compare(a, b version) int {
	for i := range a.core {
		if c := compareInt(a.core[i], b.core[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		if c := comparePreIdentifier(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	return compareInt(len(a.pre), len(b.pre))
}

func comparePreIdentifier(a, b string) int {
	numericA, numericB := isNumeric(a), isNumeric(b)
	switch {
	case numericA && numericB:
		// Leading zeros are not allowed in semver, so length settles it
		// before the digits do, and no overflow is possible.
		if c := compareInt(len(a), len(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	case numericA:
		return -1
	case numericB:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func isNumeric(s string) bool {
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

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
