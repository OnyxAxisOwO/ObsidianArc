package plugin_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/apikey"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/pkgtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/server/servertest"
)

// The demo's hook accepts the token it was configured with, sent as its
// Authorization header. Configured with an Arc key as that token, it must still
// refuse the same key from a caller: the key is withheld from the backend, so
// the hook sees no token at all.
func TestAnArcKeySentToAPackageRouteIsNotForwardedToIt(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	key := apikey.TokenPrefix + "3f9c1a7e0b2d4f6a8c1e3b5d7f9a0c2e"
	a.setSettings(map[string]string{"demo.secret": key})

	hook := func(token string) *httptest.ResponseRecorder {
		return a.in.DoWith(http.MethodPost, "/api/x/demo/hook", map[string]string{"name": "from-caller"}, nil,
			http.Header{"Authorization": {"Bearer " + token}})
	}
	res := hook(key)
	if res.Code != http.StatusUnauthorized || servertest.ErrorCode(t, res) != "unauthorized" {
		t.Fatalf("an Arc key reached the backend: %d %s", res.Code, res.Body.String())
	}
	list := a.mustDo(http.MethodGet, "/api/admin/x/demo/things", nil, http.StatusOK)
	if strings.Contains(list.Body.String(), "from-caller") {
		t.Fatal("a refused call wrote a row anyway")
	}

	// A token the package itself was given is still its own to receive.
	a.setSettings(map[string]string{"demo.secret": "s3cret-token"})
	if res := hook("s3cret-token"); res.Code != http.StatusOK {
		t.Fatalf("the package's own token: %d %s", res.Code, res.Body.String())
	}
}
