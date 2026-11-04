package console

import (
	"strings"
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

// A share of a bar is often one third, and the raw float prints as
// 33.333333333333336%. The column shows a tenth of a percent, and an absent
// value shows a dash rather than Go's "<nil>".
func TestCreditBonusPercentIsReadable(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"GET", "/api/bonus", 200,
			`{"bars":[{"bar_id":"b1","name":"Welcome","kind":"bonus","enabled":true,"percent":33.333333333333336},` +
				`{"bar_id":"b2","name":"Spare","kind":"reserve","enabled":false}]}`},
	)
	result, out := rig.run("credit bonus")
	if !result.OK {
		t.Fatalf("credit bonus failed: %s", out)
	}
	for _, bad := range []string{"33.333333333333336", "<nil>", "e+"} {
		if strings.Contains(out, bad) {
			t.Errorf("credit bonus printed %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "33.3%") {
		t.Errorf("credit bonus did not print the share as 33.3%%:\n%s", out)
	}
}
