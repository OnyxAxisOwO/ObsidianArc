package plugin_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/pkgtest"
)

// A body over the route's limit is a 413, the status a client needs to know it
// must send less. It used to come back as a 400, which reads as a malformed
// request.
func TestAPackageRouteAnswersAnOversizeBodyWith413(t *testing.T) {
	a := newAdmin(t)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	big := map[string]string{"name": strings.Repeat("a", 5<<20)}
	res := a.in.Do(http.MethodPost, "/api/x/demo/hook", big, nil)
	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversize body to a package route answered %d: %s", res.Code, res.Body.String())
	}
}
