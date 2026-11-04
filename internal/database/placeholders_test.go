package database

import "testing"

func TestPlaceholdersSpellsOneMarkerPerValue(t *testing.T) {
	cases := map[int]string{
		0: "",
		1: "?",
		3: "?, ?, ?",
	}
	for n, want := range cases {
		if got := Placeholders(n); got != want {
			t.Errorf("Placeholders(%d) = %q, want %q", n, got, want)
		}
	}
}
