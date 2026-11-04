package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

type updateBody struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	Name            string `json:"name"`
	Notes           string `json:"notes"`
	URL             string `json:"url"`
	PublishedAt     string `json:"published_at"`
	CheckDisabled   bool   `json:"check_disabled"`
	Notices         []struct {
		Kind string `json:"kind"`
	} `json:"notices"`
}

// countingFeed serves one release and counts how often anything asks.
func countingFeed(t *testing.T, tag string) (string, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     tag,
			"name":         "Obsidian Arc " + tag,
			"body":         "- faster",
			"html_url":     "https://github.com/OnyxAxisOwO/ObsidianArc/releases/tag/" + tag,
			"published_at": "2026-10-01T12:00:00Z",
		})
	}))
	t.Cleanup(feed.Close)
	return feed.URL, &requests
}

func TestUpdateNoticeReportsANewerRelease(t *testing.T) {
	feed, _ := countingFeed(t, "v0.10.0")
	in := newInstanceAt(t, "v0.9.2", feed)
	founder := in.register("founder", "a-good-password")

	response := in.do(http.MethodGet, "/api/admin/update", nil, founder)
	if response.Code != http.StatusOK {
		t.Fatalf("update: %d %s", response.Code, response.Body.String())
	}
	got := decode[updateBody](t, response)
	if got.Current != "v0.9.2" || got.Latest != "v0.10.0" || !got.UpdateAvailable {
		t.Errorf("current %q latest %q update_available %v; want v0.9.2, v0.10.0, true",
			got.Current, got.Latest, got.UpdateAvailable)
	}
	if got.Name != "Obsidian Arc v0.10.0" || got.Notes != "- faster" || got.PublishedAt == "" ||
		got.URL != "https://github.com/OnyxAxisOwO/ObsidianArc/releases/tag/v0.10.0" {
		t.Errorf("release details not carried through: %+v", got)
	}
	if got.CheckDisabled {
		t.Error("check_disabled is true while the check is on")
	}
	if !strings.Contains(response.Body.String(), `"notices":[]`) {
		t.Errorf("notices must be an empty array, not null: %s", response.Body.String())
	}
}

// A build that is already the newest release, or one past a tag, is not told
// it is behind.
func TestUpdateNoticeStaysQuietOnTheNewestBuild(t *testing.T) {
	feed, _ := countingFeed(t, "v0.10.0")
	in := newInstanceAt(t, "v0.10.0-64-gdeadbee", feed)
	founder := in.register("founder", "a-good-password")

	got := decode[updateBody](t, in.do(http.MethodGet, "/api/admin/update", nil, founder))
	if got.UpdateAvailable {
		t.Errorf("update_available is true for a build past v0.10.0: %+v", got)
	}
	if got.Latest != "v0.10.0" {
		t.Errorf("latest = %q, want v0.10.0 even when nothing is offered", got.Latest)
	}
}

func TestUpdateNoticeIsUnknownWhenTheFeedFails(t *testing.T) {
	in := newInstanceAt(t, "v0.9.2", stubReleaseFeed(t, http.StatusServiceUnavailable, ""))
	founder := in.register("founder", "a-good-password")

	response := in.do(http.MethodGet, "/api/admin/update", nil, founder)
	if response.Code != http.StatusOK {
		t.Fatalf("a dead release feed reached the client as %d, want 200 with unknown", response.Code)
	}
	got := decode[updateBody](t, response)
	if got.UpdateAvailable || got.Latest != "" || got.Name != "" || got.Notes != "" {
		t.Errorf("an unreachable feed produced release details: %+v", got)
	}
}

// Turning the check off means the server does not ask GitHub at all, not
// merely that it does not show the answer.
func TestUpdateCheckOffNeverAsksTheFeed(t *testing.T) {
	feed, requests := countingFeed(t, "v0.10.0")
	in := newInstanceAt(t, "v0.9.2", feed)
	founder := in.register("founder", "a-good-password")

	if response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"update.check": "false"}, founder); response.Code != http.StatusOK {
		t.Fatalf("switch the check off: %d %s", response.Code, response.Body.String())
	}
	for i := 0; i < 3; i++ {
		got := decode[updateBody](t, in.do(http.MethodGet, "/api/admin/update", nil, founder))
		if !got.CheckDisabled || got.UpdateAvailable || got.Latest != "" {
			t.Fatalf("with the check off: %+v, want check_disabled and nothing else", got)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the release feed was asked %d times with the check off", n)
	}
}

func TestUpdateCheckIsOnByDefault(t *testing.T) {
	in := newInstanceAt(t, "v0.9.2", stubReleaseFeed(t, http.StatusServiceUnavailable, ""))
	founder := in.register("founder", "a-good-password")

	got := decode[updateBody](t, in.do(http.MethodGet, "/api/admin/update", nil, founder))
	if got.CheckDisabled {
		t.Error("a fresh instance has the update check off; it must default on")
	}
}

// A delegated operator with every grant still does not see the release
// notice: it is the instance's own business, and the grants were never meant
// to cover it.
func TestUpdateNoticeIsRefusedToDelegatedAdministrators(t *testing.T) {
	feed, _ := countingFeed(t, "v0.10.0")
	in := newInstanceAt(t, "v0.9.2", feed)
	founder := in.register("founder", "a-good-password")
	operator := in.register("operator", "a-good-password")
	if response := in.do(http.MethodPatch, "/api/admin/users/"+operator.userID,
		map[string]any{"role": "admin", "admin_permissions": user.AdminPermissions}, founder); response.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", response.Code, response.Body.String())
	}

	response := in.do(http.MethodGet, "/api/admin/update", nil, operator)
	if response.Code != http.StatusForbidden {
		t.Errorf("a delegated administrator reached the update notice: %d %s", response.Code, response.Body.String())
	}
}

// The proxy observes a Cloudflare header from a trusted peer, and the
// backoffice says so until the operator claims Cloudflare. The notice does
// not depend on GitHub, so it still shows with the check switched off.
func TestUnclaimedCloudflareHeaderIsNoticedByTheSuperAdministrator(t *testing.T) {
	trustProxy := func(cfg *config.Config) {
		cfg.TrustProxy = true
		cfg.TrustedProxies = []string{"192.0.2.1"}
	}
	claimCloudflare := func(cfg *config.Config) {
		trustProxy(cfg)
		cfg.TrustCloudflare = true
	}

	for _, tc := range []struct {
		name   string
		tweak  func(*config.Config)
		notice bool
	}{
		{"header from a trusted proxy, unclaimed", trustProxy, true},
		{"the operator claimed Cloudflare", claimCloudflare, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := newInstanceAt(t, "v0.9.2", stubReleaseFeed(t, http.StatusServiceUnavailable, ""), tc.tweak)
			founder := in.register("founder", "a-good-password")

			request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
				strings.NewReader(`{"username":"nobody","password":"wrong-password"}`))
			request.RemoteAddr = "192.0.2.1:40000"
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			request.Header.Set("CF-Connecting-IP", "198.51.100.7")
			in.handler.ServeHTTP(httptest.NewRecorder(), request)

			got := decode[updateBody](t, in.do(http.MethodGet, "/api/admin/update", nil, founder))
			found := false
			for _, notice := range got.Notices {
				if notice.Kind == "cloudflare_unclaimed" {
					found = true
				}
			}
			if found != tc.notice {
				t.Errorf("cloudflare_unclaimed notice = %v, want %v (notices %+v)", found, tc.notice, got.Notices)
			}
		})
	}
}

func TestCloudflareNoticeShowsWithTheCheckOff(t *testing.T) {
	feed, requests := countingFeed(t, "v0.10.0")
	in := newInstanceAt(t, "v0.9.2", feed, func(cfg *config.Config) {
		cfg.TrustProxy = true
		cfg.TrustedProxies = []string{"192.0.2.1"}
	})
	founder := in.register("founder", "a-good-password")
	if response := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"update.check": "false"}, founder); response.Code != http.StatusOK {
		t.Fatalf("switch the check off: %d %s", response.Code, response.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"nobody","password":"wrong-password"}`))
	request.RemoteAddr = "192.0.2.1:40000"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("CF-Connecting-IP", "198.51.100.7")
	in.handler.ServeHTTP(httptest.NewRecorder(), request)

	got := decode[updateBody](t, in.do(http.MethodGet, "/api/admin/update", nil, founder))
	if !got.CheckDisabled || len(got.Notices) != 1 || got.Notices[0].Kind != "cloudflare_unclaimed" {
		t.Errorf("with the check off: %+v, want check_disabled and the Cloudflare notice", got)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the feed was asked %d times with the check off", n)
	}
}
