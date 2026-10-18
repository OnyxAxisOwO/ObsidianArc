package plugin_test

import (
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/pkgtest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/server/servertest"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// The operator's second-step code covers every change to plugins, not only the
// install: switching one off, removing it, and installing a previewed package
// each check it before doing anything. A request the code stops must leave the
// plugin as it was.
func TestEveryPluginChangeNeedsTheSecondStepWhenThePolicyAsksForIt(t *testing.T) {
	a := newAdmin(t)
	tf := a.in.EnrolTwoFactor(a.s)
	a.in.InstallPackage(a.s, pkgtest.Demo(t), true, nil)
	a.setSettings(map[string]string{settings.TwoFactorPluginManage: "true"})

	for _, c := range []struct {
		path string
		body map[string]any
	}{
		{"/api/admin/plugins/demo/disable", map[string]any{}},
		{"/api/admin/plugins/demo/uninstall", map[string]any{}},
		{"/api/admin/plugins/install-package", map[string]any{"token": "not-previewed"}},
	} {
		res := a.do(http.MethodPost, c.path, c.body)
		if res.Code != http.StatusBadRequest || servertest.ErrorCode(t, res) != "two_factor_code_required" {
			t.Fatalf("%s without a code: %d %s", c.path, res.Code, res.Body.String())
		}
	}
	if listed(t, a, "demo")["state"] != "enabled" {
		t.Fatal("a change the code stopped switched the plugin off")
	}

	if res := a.do(http.MethodPost, "/api/admin/plugins/demo/disable", map[string]any{"two_factor_code": tf.Next(t)}); res.Code != http.StatusOK {
		t.Fatalf("a disable with the right code: %d %s", res.Code, res.Body.String())
	}
	if listed(t, a, "demo")["state"] != "disabled" {
		t.Fatal("a disable with the right code did not switch the plugin off")
	}
}
