package plugin

import (
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
)

// The plugin list shows a setting's default, except a secret's: that default is
// a credential the package shipped. An installed package's list is read off its
// manifest, not the settings registry, so it is held to the rule on its own.
func TestAPackagesListedSettingsLeaveOutASecretsDefault(t *testing.T) {
	r := newRig(t)
	l := &loaded{name: "gadget", pkg: &arcx.Package{Manifest: &arcx.Manifest{
		Name: "gadget", Version: "1.0.0", Settings: []arcx.Setting{
			{Key: "gadget.endpoint", Default: "https://example.test"},
			{Key: "gadget.token", Default: "shipped-token", Secret: true},
		},
	}}}
	got := map[string]SettingInfo{}
	for _, s := range r.manager.packageInfo(l, record{}).Contributions.Settings {
		got[s.Key] = s
	}
	if got["gadget.endpoint"].Default != "https://example.test" {
		t.Errorf("a plain setting's default is %q, want it shown", got["gadget.endpoint"].Default)
	}
	if s := got["gadget.token"]; !s.Secret || s.Default != "" {
		t.Errorf("a secret is listed as %+v; its default must be left out", s)
	}
}
