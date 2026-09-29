package qqgroup

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/server/servertest"
)

// The plugin's whole life through the backoffice, on an instance that has
// never had it: every hook it attaches has to stay out of the way until it
// is installed and switched on, come back when it is, and — with the data
// purged — leave a schema the core runs on as if the plugin had never been.

type pluginInfo struct {
	Name     string `json:"name"`
	State    string `json:"state"`
	Manifest struct {
		Version string `json:"version"`
	} `json:"manifest"`
	Contributions struct {
		Fields       []string `json:"fields"`
		PublicRoutes []string `json:"public_routes"`
		Commands     []string `json:"commands"`
		Purges       bool     `json:"purges"`
	} `json:"contributions"`
}

func pluginState(t *testing.T, in *servertest.Instance, as *servertest.Session) pluginInfo {
	t.Helper()
	response := in.Do(http.MethodGet, "/api/admin/plugins/"+Name, nil, as)
	if response.Code != http.StatusOK {
		t.Fatalf("read the plugin: %d %s", response.Code, response.Body.String())
	}
	return servertest.Decode[struct {
		Plugin pluginInfo `json:"plugin"`
	}](t, response).Plugin
}

func advertised(t *testing.T, in *servertest.Instance) bool {
	t.Helper()
	return strings.Contains(in.Do(http.MethodGet, "/api/site", nil, nil).Body.String(), `"`+Name+`":`)
}

func hasColumn(t *testing.T, in *servertest.Instance) bool {
	t.Helper()
	_, err := in.DB.Exec(context.Background(), `SELECT qq FROM users LIMIT 1`)
	return err == nil
}

func TestAFreshInstanceOffersThePluginWithoutRunningIt(t *testing.T) {
	in := servertest.NewFresh(t)
	founder := in.Register("founder", "a-good-password")

	info := pluginState(t, in, founder)
	if info.State != "available" || info.Manifest.Version != Version {
		t.Fatalf("plugin = %+v", info)
	}
	if !info.Contributions.Purges || len(info.Contributions.Fields) != 1 ||
		len(info.Contributions.PublicRoutes) != 1 || len(info.Contributions.Commands) != 2 {
		t.Fatalf("the manifest does not list what the plugin attaches: %+v", info.Contributions)
	}
	if hasColumn(t, in) {
		t.Fatal("an uninstalled plugin's column exists")
	}
	if advertised(t, in) {
		t.Fatal("the browser is told to load an uninstalled plugin")
	}
	if code := in.Do(http.MethodGet, "/api/admin/departures", nil, founder).Code; code != http.StatusNotFound {
		t.Fatalf("an uninstalled plugin's admin route answered %d", code)
	}
	if code := doBot(in, testBotToken, map[string]any{"qq": "12345"}).Code; code != http.StatusNotFound {
		t.Fatalf("an uninstalled plugin's webhook answered %d", code)
	}
	if strings.Contains(in.Do(http.MethodGet, "/api/admin/settings", nil, founder).Body.String(), Requirement) {
		t.Fatal("an uninstalled plugin's setting is listed")
	}
	// And the core's own account routes run without the column at all.
	in.RegisterWith(map[string]any{"username": "second", "password": "a-good-password"})
}

func TestInstallSwitchOffAndRemoveThroughTheBackoffice(t *testing.T) {
	in := servertest.NewFresh(t)
	founder := in.Register("founder", "a-good-password")

	install := in.Do(http.MethodPost, "/api/admin/plugins/"+Name+"/install", map[string]any{
		"enable":   false,
		"settings": map[string]string{BotWebhookToken: testBotToken, BotDepartureMode: ModeDelete},
	}, founder)
	if install.Code != http.StatusOK {
		t.Fatalf("install: %d %s", install.Code, install.Body.String())
	}
	if info := pluginState(t, in, founder); info.State != "disabled" {
		t.Fatalf("installed switched off, the state is %s", info.State)
	}
	if !hasColumn(t, in) {
		t.Fatal("install did not run the migration")
	}
	if advertised(t, in) {
		t.Fatal("a switched-off plugin is advertised")
	}
	if again := in.Do(http.MethodPost, "/api/admin/plugins/"+Name+"/install", map[string]any{}, founder); again.Code != http.StatusConflict {
		t.Fatalf("a second install: %d", again.Code)
	}

	if enable := in.Do(http.MethodPost, "/api/admin/plugins/"+Name+"/enable", nil, founder); enable.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", enable.Code, enable.Body.String())
	}
	if !advertised(t, in) {
		t.Fatal("an enabled plugin is not advertised")
	}
	// The first settings arrived with the install.
	if code := doBot(in, testBotToken, map[string]any{"qq": "123456"}).Code; code == http.StatusNotFound {
		t.Fatal("the webhook is still missing once enabled")
	}
	member := in.RegisterWith(map[string]any{
		"username": "member", "password": "a-good-password", "fields": map[string]string{Field: "10001"},
	})
	me := in.Do(http.MethodGet, "/api/auth/me", nil, member).Body.String()
	if !strings.Contains(me, `"qq":"10001"`) {
		t.Fatalf("the field is not on the account: %s", me)
	}

	// Switching it off needs the second step, which the founder has not set
	// up yet — and then a code from it.
	off := in.Do(http.MethodPost, "/api/admin/plugins/"+Name+"/disable", map[string]any{}, founder)
	if off.Code != http.StatusForbidden || servertest.ErrorCode(t, off) != "two_factor_required" {
		t.Fatalf("disable without two-step: %d %s", off.Code, off.Body.String())
	}
	authenticator := in.EnrolTwoFactor(founder)
	off = in.Do(http.MethodPost, "/api/admin/plugins/"+Name+"/disable", map[string]any{}, founder)
	if off.Code != http.StatusBadRequest || servertest.ErrorCode(t, off) != "two_factor_code_required" {
		t.Fatalf("disable without a code: %d %s", off.Code, off.Body.String())
	}
	off = in.Do(http.MethodPost, "/api/admin/plugins/"+Name+"/disable",
		map[string]any{"two_factor_code": "000000"}, founder)
	if off.Code == http.StatusOK {
		t.Fatal("a wrong code switched the plugin off")
	}
	off = in.Do(http.MethodPost, "/api/admin/plugins/"+Name+"/disable",
		map[string]any{"two_factor_code": authenticator.Next(t)}, founder)
	if off.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", off.Code, off.Body.String())
	}
	me = in.Do(http.MethodGet, "/api/auth/me", nil, member).Body.String()
	if strings.Contains(me, `"qq"`) {
		t.Fatalf("a switched-off plugin's field is still on the account: %s", me)
	}
	if code := in.Do(http.MethodGet, "/api/admin/departures", nil, founder).Code; code != http.StatusNotFound {
		t.Fatalf("a switched-off plugin's route answered %d", code)
	}

	// Removing it with its data takes the column away; the core carries on.
	remove := in.Do(http.MethodPost, "/api/admin/plugins/"+Name+"/uninstall",
		map[string]any{"purge": true, "two_factor_code": authenticator.Next(t)}, founder)
	if remove.Code != http.StatusOK {
		t.Fatalf("uninstall: %d %s", remove.Code, remove.Body.String())
	}
	if hasColumn(t, in) {
		t.Fatal("purging kept the column")
	}
	if info := pluginState(t, in, founder); info.State != "available" {
		t.Fatalf("after uninstalling, the state is %s", info.State)
	}
	if code := in.Do(http.MethodGet, "/api/auth/me", nil, member).Code; code != http.StatusOK {
		t.Fatalf("an account read after the purge: %d", code)
	}
	in.RegisterWith(map[string]any{"username": "after", "password": "a-good-password"})

	// And it can come back, from nothing.
	if again := in.Do(http.MethodPost, "/api/admin/plugins/"+Name+"/install",
		map[string]any{"enable": true}, founder); again.Code != http.StatusOK {
		t.Fatalf("reinstall: %d %s", again.Code, again.Body.String())
	}
	if !hasColumn(t, in) {
		t.Fatal("reinstalling after a purge did not bring the column back")
	}
}

// An upgrade from a build where the plugin could not be switched: the
// migrations are recorded, so the boot adopts it and nothing changes for the
// instance's members.
func TestAnUpgradedInstanceKeepsThePluginOn(t *testing.T) {
	in := servertest.NewLegacy(t, nil)
	founder := in.Register("founder", "a-good-password")
	if info := pluginState(t, in, founder); info.State != "enabled" {
		t.Fatalf("the upgraded instance's plugin is %s", info.State)
	}
	if !advertised(t, in) {
		t.Fatal("the adopted plugin is not advertised")
	}
}

// Three grants: looking, switching, removing. Each is its own, and a grant
// to change includes the one to look.
func TestThePluginGrantsAreSeparate(t *testing.T) {
	in := servertest.NewFresh(t)
	founder := in.Register("founder", "a-good-password")
	grant := func(username string, permissions ...string) *servertest.Session {
		t.Helper()
		session := in.RegisterWith(map[string]any{"username": username, "password": "a-good-password"})
		response := in.Do(http.MethodPatch, "/api/admin/users/"+session.UserID,
			map[string]any{"role": "admin", "admin_permissions": permissions}, founder)
		if response.Code != http.StatusOK {
			t.Fatalf("grant %s: %d %s", username, response.Code, response.Body.String())
		}
		return session
	}
	viewer := grant("viewer", "plugins")
	manager := grant("manager", "plugins_manage")
	remover := grant("remover", "plugins_remove")
	outsider := grant("outsider", "users")

	for _, who := range []*servertest.Session{viewer, manager, remover} {
		if code := in.Do(http.MethodGet, "/api/admin/plugins", nil, who).Code; code != http.StatusOK {
			t.Fatalf("a plugins grant could not list: %d", code)
		}
	}
	if code := in.Do(http.MethodGet, "/api/admin/plugins", nil, outsider).Code; code != http.StatusForbidden {
		t.Fatalf("an unrelated grant listed the plugins: %d", code)
	}
	for _, who := range []*servertest.Session{viewer, remover, outsider} {
		if code := in.Do(http.MethodPost, "/api/admin/plugins/"+Name+"/install", map[string]any{}, who).Code; code != http.StatusForbidden {
			t.Fatalf("installing without the manage grant: %d", code)
		}
	}
	if code := in.Do(http.MethodPost, "/api/admin/plugins/"+Name+"/install",
		map[string]any{"enable": true}, manager).Code; code != http.StatusOK {
		t.Fatalf("the manage grant could not install: %d", code)
	}
	if code := in.Do(http.MethodPost, "/api/admin/plugins/"+Name+"/uninstall",
		map[string]any{"two_factor_code": "123456"}, manager).Code; code != http.StatusForbidden {
		t.Fatalf("the manage grant reached uninstall: %d", code)
	}
	// The remove grant gets as far as its own two-step check.
	response := in.Do(http.MethodPost, "/api/admin/plugins/"+Name+"/uninstall", map[string]any{}, remover)
	if servertest.ErrorCode(t, response) != "two_factor_required" {
		t.Fatalf("the remove grant: %d %s", response.Code, response.Body.String())
	}
}
