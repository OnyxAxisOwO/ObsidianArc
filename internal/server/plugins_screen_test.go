package server

import (
	"net/http"
	"testing"
)

// The plugins screen's routes are core ones — they exist on a build with no
// plugin at all, to say so — and sit behind the backoffice's wrapper like
// every other administrative route.
func TestThePluginsScreenIsBehindTheBackoffice(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	regular := in.register("visitor", "another-password")

	routes := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/admin/plugins", nil},
		{http.MethodGet, "/api/admin/plugins/anything", nil},
		{http.MethodPost, "/api/admin/plugins/anything/install", map[string]any{}},
		{http.MethodPost, "/api/admin/plugins/anything/enable", nil},
		{http.MethodPost, "/api/admin/plugins/anything/disable", map[string]any{}},
		{http.MethodPost, "/api/admin/plugins/anything/uninstall", map[string]any{}},
	}
	for _, route := range routes {
		if code := in.do(route.method, route.path, route.body, nil).Code; code != http.StatusUnauthorized {
			t.Errorf("%s %s anonymously: %d", route.method, route.path, code)
		}
		if code := in.do(route.method, route.path, route.body, regular).Code; code != http.StatusForbidden {
			t.Errorf("%s %s as a member: %d", route.method, route.path, code)
		}
	}

	list := decode[struct {
		Plugins []any `json:"plugins"`
	}](t, in.do(http.MethodGet, "/api/admin/plugins", nil, admin))
	if len(list.Plugins) != 0 {
		t.Fatalf("a core build lists plugins: %v", list.Plugins)
	}
	for _, route := range routes[1:] {
		if code := in.do(route.method, route.path, route.body, admin).Code; code != http.StatusNotFound {
			t.Errorf("%s %s for a plugin this build lacks: %d", route.method, route.path, code)
		}
	}
}
