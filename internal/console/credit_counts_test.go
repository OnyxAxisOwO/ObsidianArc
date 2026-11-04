package console

import (
	"strings"
	"testing"
)

// The usage screens print counts a member reads against their limits. A count
// of a million or more used to print through %v as 1.5e+06, which is not a
// number anyone can compare with a limit.
func TestCreditCountsOfAMillionOrMorePrintInFull(t *testing.T) {
	rig := newFlagRig(flagRoute{"GET", "/api/usage/me", 200,
		`{"windows":[{"kind":"day","enforced":true,"used_requests":1500000,"limit_requests":2000000,` +
			`"used_tokens":12582912,"limit_tokens":null,"used_credits":1.5,"limit_credits":null,"resets_at":0}]}`})
	result, out := rig.run("credit show")
	if !result.OK {
		t.Fatalf("credit show failed: %s", out)
	}
	for _, want := range []string{"1500000", "2000000", "12582912"} {
		if !strings.Contains(out, want) {
			t.Errorf("credit show did not print %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "e+") {
		t.Errorf("credit show printed a count in exponent form:\n%s", out)
	}
}

func TestCreditHistoryTotalsOfAMillionOrMorePrintInFull(t *testing.T) {
	rig := newFlagRig(flagRoute{"GET", "/api/usage/me/history", 200,
		`{"totals":{"requests":1500000,"total_tokens":12582912,"credits":2.5,"errors":0},` +
			`"turns":[{"id":"t1","model_name":"m","status":"done","input_tokens":1234567,"output_tokens":1000000,"credits":0.5,"started_at":0}]}`})
	result, out := rig.run("credit history")
	if !result.OK {
		t.Fatalf("credit history failed: %s", out)
	}
	if !strings.Contains(out, "total: 1500000 requests, 12582912 tokens, 2.50 credits, 0 errors") {
		t.Errorf("the totals line did not print in full:\n%s", out)
	}
	if !strings.Contains(out, "1234567") || !strings.Contains(out, "1000000") {
		t.Errorf("a turn's token counts did not print in full:\n%s", out)
	}
	if strings.Contains(out, "e+") {
		t.Errorf("credit history printed a count in exponent form:\n%s", out)
	}
}
