package console

import (
	"testing"
)

// fmt prints a float64 of a million or more in exponent form. A token limit of
// a million then reads as 1e+06, which is not a number a person can compare.
func TestCountsOfAMillionOrMorePrintInFull(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"a million", numberString(float64(1000000)), "1000000"},
		{"a heap of twelve megabytes", numberString(float64(12582912)), "12582912"},
		{"a limit read through limitStr", limitStr(float64(1500000)), "1500000"},
		{"a fraction keeps its digits", formatNumber(0.5), "0.5"},
		{"an absent value stays a dash", limitStr(nil), "-"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}
