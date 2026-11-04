package admin

import (
	"net/http/httptest"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/reqlog"
)

// A misspelt outcome must be refused, not read as no outcome: the log has no
// condition for it, so the operator would be shown every entry under a filter
// they believe is applied.
func TestLogOutcomeFilterAcceptsOnlyTheOutcomesTheStoreKnows(t *testing.T) {
	cases := []struct {
		query   string
		want    string
		refused bool
	}{
		{query: "", want: ""},
		{query: "outcome=ok", want: reqlog.OutcomeOK},
		{query: "outcome=FAILED", want: reqlog.OutcomeFailed},
		{query: "outcome=error", refused: true},
		{query: "outcome=all", refused: true},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/api/admin/logs?"+tc.query, nil)
		filter, err := logFilterFrom(r)
		if tc.refused {
			if err == nil {
				t.Errorf("%q: accepted as outcome %q", tc.query, filter.Outcome)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: refused: %v", tc.query, err)
			continue
		}
		if filter.Outcome != tc.want {
			t.Errorf("%q: outcome = %q, want %q", tc.query, filter.Outcome, tc.want)
		}
	}
}
