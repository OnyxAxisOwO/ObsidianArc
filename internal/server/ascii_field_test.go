package server

import (
	"net/http"
	"testing"
)

// The background behind an empty conversation is on until an operator turns
// it off, and the browser learns which from /api/site, which is read before
// anyone has signed in.
func TestAsciiFieldIsSwitchedByTheOperator(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")

	site := decode[map[string]any](t, in.do(http.MethodGet, "/api/site", nil, nil))
	if site["ascii_field"] != true {
		t.Fatalf("site ascii_field = %v, want true by default", site["ascii_field"])
	}

	if res := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"theme.ascii_field": "false"}, admin); res.Code != http.StatusOK {
		t.Fatalf("save: %d %s", res.Code, res.Body.String())
	}
	site = decode[map[string]any](t, in.do(http.MethodGet, "/api/site", nil, nil))
	if site["ascii_field"] != false {
		t.Fatalf("site ascii_field = %v after the operator turned it off", site["ascii_field"])
	}
}
