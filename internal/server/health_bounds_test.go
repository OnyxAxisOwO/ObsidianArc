package server

import (
	"net/http"
	"testing"
)

// The availability thresholds are read by the checker for every model, so an
// out-of-range value saved from the browser (the field's own clamp is not
// enough) must be refused by the server, the same as the import refuses it.
func TestAvailabilityThresholdsAreBoundedOnTheServer(t *testing.T) {
	in := newInstance(t)
	admin := in.register("availability-admin", "a-good-password")

	for key, value := range map[string]string{
		"health.disable_below":  "150",
		"health.warn_below":     "101",
		"health.retain_days":    "0",
		"health.window_minutes": "0",
	} {
		res := in.do(http.MethodPut, "/api/admin/settings", map[string]string{key: value}, admin)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s=%s: got %d %s, want 400", key, value, res.Code, res.Body.String())
		}
	}

	if res := in.do(http.MethodPut, "/api/admin/settings",
		map[string]string{"health.disable_below": "80", "health.retain_days": "14"}, admin); res.Code != http.StatusOK {
		t.Errorf("in-range thresholds: %d %s", res.Code, res.Body.String())
	}
}
