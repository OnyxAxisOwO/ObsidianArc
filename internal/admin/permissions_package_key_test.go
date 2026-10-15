package admin

import (
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A package installed while the server runs picks its own key names. One that
// starts with a core prefix must not inherit the core grant that prefix maps to,
// or the availability grant would read and write the package's settings, its
// non-secret ones included.
func TestAPackageKeyDoesNotInheritACoreGrantByItsPrefix(t *testing.T) {
	svc := settings.New(nil)
	if err := svc.AddPluginDefinitions("acme", []settings.Definition{
		{Key: "health.endpoint"},
		{Key: "acme.token"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	availability := user.User{Role: user.RoleAdmin, AdminPermissions: []string{"availability"}}

	if got := settingPermission(svc, "health.endpoint"); got != "super_admin" {
		t.Fatalf("a package's health. key is judged by %q, want super_admin", got)
	}
	if hasPermission(availability, settingPermission(svc, "health.endpoint")) {
		t.Fatal("the availability grant reaches a package's health. key")
	}
	// A package key outside every core prefix keeps the default it always had.
	if got := settingPermission(svc, "acme.token"); got != "settings" {
		t.Fatalf("a package's own key is judged by %q, want settings", got)
	}
}

var definePermTestCompiled sync.Once

// A plugin compiled into the binary defines its keys once, at init, and its
// definitions are documented to follow the core prefix rules. So a compiled-in
// key with a core prefix keeps that prefix's grant. Only a key installed at run
// time loses it, and the test above covers that branch. Without this exemption
// the compiled-in key would fall through to super_admin and the availability
// grant would stop reaching it.
func TestACompiledInPluginKeyKeepsTheCoreGrantItsPrefixNames(t *testing.T) {
	definePermTestCompiled.Do(func() {
		settings.Define(settings.Definition{Key: "health.permtest_compiled", Default: "x", Plugin: "permtest"})
	})
	svc := settings.New(nil)
	if got := settingPermission(svc, "health.permtest_compiled"); got != "availability" {
		t.Fatalf("a compiled-in plugin's health. key is judged by %q, want availability", got)
	}
	availability := user.User{Role: user.RoleAdmin, AdminPermissions: []string{"availability"}}
	if !hasPermission(availability, settingPermission(svc, "health.permtest_compiled")) {
		t.Fatal("the availability grant does not reach a compiled-in plugin's health. key")
	}
}
