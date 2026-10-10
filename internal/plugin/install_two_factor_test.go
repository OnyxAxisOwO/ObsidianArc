package plugin_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/server/servertest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// Installing a plugin takes the operator's second-step code when the policy
// asks for one, as switching it off does. This build compiles no plugin in, so
// a request that gets past the code ends in a 404 from the install itself. A
// request the code stops never reaches the install, which is what is checked.
func TestInstallingAPluginNeedsTheSecondStepWhenThePolicyAsksForIt(t *testing.T) {
	a := newAdmin(t)
	tf := a.in.EnrolTwoFactor(a.s)
	install := func(body map[string]any) *httptest.ResponseRecorder {
		return a.do(http.MethodPost, "/api/admin/plugins/demo/install", body)
	}

	a.setSettings(map[string]string{settings.TwoFactorPluginManage: "true"})
	if res := install(map[string]any{"enable": true}); res.Code != http.StatusBadRequest || servertest.ErrorCode(t, res) != "two_factor_code_required" {
		t.Fatalf("an install without a code: %d %s", res.Code, res.Body.String())
	}
	if res := install(map[string]any{"enable": true, "two_factor_code": "000000"}); res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "two_factor_code") {
		t.Fatalf("an install with a wrong code: %d %s", res.Code, res.Body.String())
	}
	if res := install(map[string]any{"enable": true, "two_factor_code": tf.Next(t)}); res.Code != http.StatusNotFound {
		t.Fatalf("an install with a right code: %d %s", res.Code, res.Body.String())
	}

	// With the policy off, the code is not asked for.
	a.setSettings(map[string]string{settings.TwoFactorPluginManage: "false"})
	if res := install(map[string]any{"enable": true}); res.Code != http.StatusNotFound {
		t.Fatalf("an install with the policy off: %d %s", res.Code, res.Body.String())
	}
}
