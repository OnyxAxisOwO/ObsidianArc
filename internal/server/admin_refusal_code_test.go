package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A backoffice refusal carries a code the screen words in the reader's
// language, and the figures its sentence quotes travel in details, because the
// message beside them is English only.
func TestABackofficeRefusalCarriesItsCodeAndFigures(t *testing.T) {
	in := newInstance(t)
	founder := in.register("founder", "a-good-password")

	res := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{"health.window_minutes": "0"}, founder)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("out-of-range setting: %d %s, want 400", res.Code, res.Body.String())
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Setting string `json:"setting"`
			Min     int    `json:"min"`
			Max     int    `json:"max"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if got := payload.Error; got.Code != "setting_out_of_range" || got.Setting != "health.window_minutes" ||
		got.Min != 1 || got.Max != 7*24*60 {
		t.Errorf("refusal = %+v", got)
	}

	res = in.do(http.MethodGet, "/api/admin/users?group_id=not-an-id", nil, founder)
	var malformed struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&malformed); err != nil {
		t.Fatal(err)
	}
	if res.Code != http.StatusBadRequest || malformed.Error.Code != "malformed_group_id" {
		t.Errorf("malformed group filter: %d %q, want 400 malformed_group_id", res.Code, malformed.Error.Code)
	}
}
