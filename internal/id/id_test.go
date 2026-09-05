package id

import (
	"sort"
	"testing"
	"time"
)

func TestNewShape(t *testing.T) {
	value := New()
	if len(value) != Length {
		t.Fatalf("length %d, want %d", len(value), Length)
	}
	if !Valid(value) {
		t.Fatalf("%q failed its own validator", value)
	}
}

func TestUnique(t *testing.T) {
	seen := make(map[string]bool, 10000)
	for i := 0; i < 10000; i++ {
		value := New()
		if seen[value] {
			t.Fatalf("duplicate identifier after %d draws: %s", i, value)
		}
		seen[value] = true
	}
}

// The whole reason for ULIDs over random UUIDs: `ORDER BY id` has to be
// chronological, because pagination and "newest first" listings rely on it.
func TestSortsByTime(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ordered := []string{
		NewAt(base),
		NewAt(base.Add(time.Millisecond)),
		NewAt(base.Add(time.Second)),
		NewAt(base.Add(time.Hour)),
		NewAt(base.AddDate(1, 0, 0)),
	}

	shuffled := append([]string(nil), ordered...)
	sort.Sort(sort.Reverse(sort.StringSlice(shuffled)))
	sort.Strings(shuffled)

	for i := range ordered {
		if shuffled[i] != ordered[i] {
			t.Fatalf("position %d: sorted to %s, want %s", i, shuffled[i], ordered[i])
		}
	}
}

func TestValidRejectsJunk(t *testing.T) {
	cases := []string{
		"",
		"too-short",
		"01M1RK2AVK2QMVQE2H66GGAD5",   // one character short
		"01M1RK2AVK2QMVQE2H66GGAD5BB", // one too long
		"01M1RK2AVK2QMVQE2H66GGADIL",  // I and L are not in Crockford base32
		"../../etc/passwd0000000000",
	}
	for _, value := range cases {
		if Valid(value) {
			t.Errorf("Valid(%q) = true, want false", value)
		}
	}
}

func TestSecretIsNotSortable(t *testing.T) {
	first := Secret(32)
	second := Secret(32)
	if first == second {
		t.Fatal("two secrets came back identical")
	}
	if len(first) < 40 {
		t.Errorf("32 random bytes encoded to only %d characters", len(first))
	}
}
