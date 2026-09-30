package server

import (
	"net/http"
	"strings"
	"testing"
)

// This package's tests build servers with no plugin compiled in, which is
// the claim plugins exist to make true: the core is a whole product without
// any of them. It advertises none, asks for no plugin field, mounts no
// plugin route and has no plugin column.
func TestACoreBuildKnowsNothingOfAnyPlugin(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	site := decode[struct {
		Plugins map[string]any    `json:"plugins"`
		Fields  map[string]string `json:"fields"`
	}](t, in.do(http.MethodGet, "/api/site", nil, nil))
	if len(site.Plugins) != 0 || len(site.Fields) != 0 {
		t.Fatalf("a core build advertises plugins %v and fields %v", site.Plugins, site.Fields)
	}

	// A plugin's settings are unknown keys here, refused like any typo.
	if res := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"some_plugin.key": "x"}, admin); res.Code != http.StatusBadRequest {
		t.Fatalf("a plugin setting was writable on a core build: %d", res.Code)
	}

	// And its column does not exist on a fresh database.
	rows, err := in.db.Query(t.Context(), `SELECT * FROM users LIMIT 0`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range columns {
		if column == "qq" {
			t.Fatal("a core build's accounts table has the QQ-group plugin's column")
		}
	}
}

// A setting row left behind by a plugin that is no longer compiled in may be
// that plugin's credential, and the redaction that would have masked it
// left with the plugin — so the backoffice does not show it at all.
func TestAnOrphanedPluginSettingIsNeverShown(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	if _, err := in.db.Exec(t.Context(),
		`INSERT INTO settings (key, value, updated_at) VALUES ('some_plugin.secret', 'left-behind-secret', 0)`); err != nil {
		t.Fatal(err)
	}
	if err := in.server.settings.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	listed := in.do(http.MethodGet, "/api/admin/settings", nil, admin)
	if listed.Code != http.StatusOK {
		t.Fatalf("list settings: %d", listed.Code)
	}
	if body := listed.Body.String(); strings.Contains(body, "left-behind-secret") || strings.Contains(body, "some_plugin.secret") {
		t.Fatalf("an orphaned plugin setting is in the backoffice: %s", body)
	}
}
