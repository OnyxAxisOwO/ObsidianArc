package server

import (
	"net/http"
	"slices"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// A history of zero or less sent every turn's whole conversation to the
// provider, and a negative generation cap was read as unlimited. Both were
// stored by the save and by the import, because neither had a range. Zero stays
// legal for the cap, where the interface already calls it unlimited.
func TestSettingsRefuseOutOfRangeTurnHistoryAndConcurrency(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	refused := []struct{ key, value string }{
		{settings.ConversationMaxTurns, "0"},
		{settings.ConversationMaxTurns, "-1"},
		{settings.QuotaMaxConcurrent, "-1"},
	}
	for _, tc := range refused {
		save := in.do(http.MethodPut, "/api/admin/settings", map[string]string{tc.key: tc.value}, admin)
		if save.Code != http.StatusBadRequest {
			t.Fatalf("save %s=%s: %d %s, want 400", tc.key, tc.value, save.Code, save.Body.String())
		}
		// An import drops what it cannot take and names it, rather than failing
		// the file, so the value is refused by being skipped.
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

	unlimited := in.do(http.MethodPut, "/api/admin/settings", map[string]string{settings.QuotaMaxConcurrent: "0"}, admin)
	if unlimited.Code != http.StatusOK {
		t.Fatalf("save quota.max_concurrent=0: %d %s, want 200 (0 means unlimited)", unlimited.Code, unlimited.Body.String())
	}
}
