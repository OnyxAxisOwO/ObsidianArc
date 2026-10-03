package server

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
)

// The overview of everyone's allowance is sorted across the whole population
// and phrased by the same arithmetic as each account's own bars. Both are
// easy to get subtly wrong — the sort by taking a page first, the figures by
// finding a counter for the wrong bucket — so this holds the list to the
// per-account answer and to the order an operator needs.
func TestAdminAllowancesListsTheTightestFirst(t *testing.T) {
	in := newInstance(t)
	admin := in.register("allowance-admin", "a-good-password")
	spent := map[string]int64{"nearly": 900, "barely": 100, "gone": 1000}
	people := map[string]*session{}
	for name, tokens := range spent {
		who := in.register(name, "a-good-password")
		people[name] = who
		// An account's first window starts when it was created.
		var created int64
		if err := in.db.QueryRow(t.Context(), `SELECT created_at FROM users WHERE id = ?`, who.userID).Scan(&created); err != nil {
			t.Fatal(err)
		}
		if _, err := in.db.Exec(t.Context(),
			`INSERT INTO usage_counters (scope_key, window_kind, window_start, requests, tokens, credits)
			 VALUES (?, '5h', ?, 3, ?, 0)`, "u:"+who.userID, created, tokens); err != nil {
			t.Fatal(err)
		}
	}
	if res := in.do(http.MethodPut, "/api/admin/quota/policies", map[string]any{
		"scope":   "global",
		"windows": map[string]any{"5h": map[string]any{"enabled": true, "tokens": 1000}},
	}, admin); res.Code != http.StatusOK {
		t.Fatalf("save policy: %d %s", res.Code, res.Body.String())
	}

	type listing struct {
		Rows []struct {
			ID        string              `json:"id"`
			Username  string              `json:"username"`
			Pressure  *float64            `json:"pressure"`
			Unlimited bool                `json:"unlimited"`
			Windows   []quota.WindowUsage `json:"windows"`
		} `json:"rows"`
		Total     int `json:"total"`
		Low       int `json:"low"`
		Exhausted int `json:"exhausted"`
	}
	all := decode[listing](t, in.do(http.MethodGet, "/api/admin/usage/allowances", nil, admin))

	var order []string
	for _, row := range all.Rows {
		order = append(order, row.Username)
	}
	// The administrator is exempt by default, so nothing constrains them and
	// they come last whatever else is true.
	if want := []string{"gone", "nearly", "barely", "allowance-admin"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if all.Total != 4 || all.Low != 2 || all.Exhausted != 1 {
		t.Errorf("total/low/exhausted = %d/%d/%d, want 4/2/1", all.Total, all.Low, all.Exhausted)
	}
	if all.Rows[1].Pressure == nil || *all.Rows[1].Pressure != 0.9 {
		t.Errorf("nearly's pressure = %v, want 0.9", all.Rows[1].Pressure)
	}
	if !all.Rows[3].Unlimited || all.Rows[3].Pressure != nil {
		t.Errorf("the administrator = %+v, want unlimited with no pressure", all.Rows[3])
	}

	// The same windows an account's own panel shows, for the same account.
	panel := decode[struct {
		Usage struct {
			Windows []quota.WindowUsage `json:"windows"`
		} `json:"usage"`
	}](t, in.do(http.MethodGet, "/api/admin/users/"+people["nearly"].userID, nil, admin))
	if !reflect.DeepEqual(all.Rows[1].Windows, panel.Usage.Windows) {
		t.Errorf("overview windows = %+v\nuser panel windows = %+v", all.Rows[1].Windows, panel.Usage.Windows)
	}

	// Filtering and paging are applied after the sort, to the whole list.
	if got := decode[listing](t, in.do(http.MethodGet, "/api/admin/usage/allowances?state=exhausted", nil, admin)); got.Total != 1 || got.Rows[0].Username != "gone" {
		t.Errorf("exhausted = %+v, want only gone", got.Rows)
	}
	if got := decode[listing](t, in.do(http.MethodGet, "/api/admin/usage/allowances?limit=1&offset=1", nil, admin)); len(got.Rows) != 1 || got.Rows[0].Username != "nearly" || got.Total != 4 {
		t.Errorf("second page = %+v (total %d), want nearly of 4", got.Rows, got.Total)
	}
	if res := in.do(http.MethodGet, "/api/admin/usage/allowances?state=bogus", nil, admin); res.Code != http.StatusBadRequest {
		t.Errorf("unknown state: %d, want 400", res.Code)
	}
	if res := in.do(http.MethodGet, "/api/admin/usage/allowances", nil, people["gone"]); res.Code != http.StatusForbidden && res.Code != http.StatusUnauthorized {
		t.Errorf("a member reading everyone's allowance: %d", res.Code)
	}
}
