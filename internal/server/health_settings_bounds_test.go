package server

import (
	"net/http"
	"slices"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// The availability checker reads these on every probe. Without a range, a
// disable threshold of 5000 never fires and a retention of zero stops the
// history from ever being pruned, and the save and the import stored both as
// typed. The ends of each range are legal values, so an operator can reach
// them.
func TestHealthSettingsRefuseOutOfRangeValues(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	refused := []struct{ key, value string }{
		{settings.HealthWindowMins, "0"},
		{settings.HealthWindowMins, "10081"},
		{settings.HealthDisableAfter, "1001"},
		{settings.HealthRetainDays, "0"},
		{settings.HealthRetainDays, "3651"},
		{settings.HealthDisableBelow, "101"},
		{settings.HealthWarnBelow, "-1"},
	}
	for _, tc := range refused {
		save := in.do(http.MethodPut, "/api/admin/settings", map[string]string{tc.key: tc.value}, admin)
		if save.Code != http.StatusBadRequest {
			t.Fatalf("save %s=%s: %d %s, want 400", tc.key, tc.value, save.Code, save.Body.String())
		}
		imported := in.do(http.MethodPost, "/api/admin/settings/import", map[string]string{tc.key: tc.value}, admin)
		if imported.Code != http.StatusOK {
			t.Fatalf("import %s=%s: %d %s", tc.key, tc.value, imported.Code, imported.Body.String())
		}
		skipped := decode[struct {
			Skipped []string `json:"skipped"`
		}](t, imported).Skipped
		if !slices.Contains(skipped, tc.key) {
			t.Fatalf("import %s=%s: not named as skipped (skipped=%v)", tc.key, tc.value, skipped)
		}
	}

	accepted := []struct{ key, value string }{
		{settings.HealthWindowMins, "1"},
		{settings.HealthWindowMins, "10080"},
		{settings.HealthDisableAfter, "1000"},
		{settings.HealthRetainDays, "1"},
		{settings.HealthRetainDays, "3650"},
		{settings.HealthDisableBelow, "100"},
		{settings.HealthWarnBelow, "0"},
	}
	for _, tc := range accepted {
		save := in.do(http.MethodPut, "/api/admin/settings", map[string]string{tc.key: tc.value}, admin)
		if save.Code != http.StatusOK {
			t.Fatalf("save %s=%s: %d %s, want 200", tc.key, tc.value, save.Code, save.Body.String())
		}
	}
}
