package server

import (
	"net/http"
	"strings"
	"testing"
)

type bonusView struct {
	BarID     string   `json:"bar_id"`
	Name      string   `json:"name"`
	Choosable bool     `json:"choosable"`
	Enabled   bool     `json:"enabled"`
	Total     *float64 `json:"total"`
	Remaining *float64 `json:"remaining"`
	Percent   int      `json:"percent"`
}

func TestAnAdministratorMakesABarGrantsIntoItAndTheAccountSeesIt(t *testing.T) {
	in := newInstance(t)
	admin := in.register("bonus-admin", "a-good-password")
	reader := in.register("bonus-reader", "a-good-password")
	other := in.register("bonus-other", "a-good-password")

	created := in.do(http.MethodPost, "/api/admin/bonus/bars", map[string]any{
		"name": "新用户礼包", "toggle_mode": "user", "show_total": true,
	}, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	bar := decode[struct {
		Bar struct{ ID string } `json:"bar"`
	}](t, created).Bar

	// A grant needs to say who it is for.
	if res := in.do(http.MethodPost, "/api/admin/bonus/bars/"+bar.ID+"/grants", map[string]any{"amount": 5}, admin); res.Code != http.StatusBadRequest {
		t.Fatalf("a grant with nobody to go to: %d", res.Code)
	}
	granted := in.do(http.MethodPost, "/api/admin/bonus/bars/"+bar.ID+"/grants", map[string]any{
		"user_ids": []string{reader.userID}, "amount": 1000, "days": 30, "note": "welcome",
	}, admin)
	if granted.Code != http.StatusCreated {
		t.Fatalf("grant: %d %s", granted.Code, granted.Body.String())
	}

	views := decode[struct {
		Bars []bonusView `json:"bars"`
	}](t, in.do(http.MethodGet, "/api/bonus", nil, reader)).Bars
	if len(views) != 1 || views[0].Name != "新用户礼包" || views[0].Total == nil || *views[0].Total != 1000 || !views[0].Choosable || !views[0].Enabled {
		t.Fatalf("the account sees %+v", views)
	}
	if got := decode[struct {
		Bars []bonusView `json:"bars"`
	}](t, in.do(http.MethodGet, "/api/bonus", nil, other)).Bars; len(got) != 0 {
		t.Fatalf("an account nothing was granted to sees %+v", got)
	}

	// The account switches it off and back on.
	off := in.do(http.MethodPut, "/api/bonus/"+bar.ID+"/choice", map[string]any{"enabled": false}, reader)
	if off.Code != http.StatusNoContent {
		t.Fatalf("switch off: %d %s", off.Code, off.Body.String())
	}
	views = decode[struct {
		Bars []bonusView `json:"bars"`
	}](t, in.do(http.MethodGet, "/api/bonus", nil, reader)).Bars
	if views[0].Enabled {
		t.Fatal("still switched on")
	}
	// Somebody who holds nothing in it cannot switch it.
	if res := in.do(http.MethodPut, "/api/bonus/"+bar.ID+"/choice", map[string]any{"enabled": true}, other); res.Code != http.StatusNotFound {
		t.Fatalf("an account with nothing in the bar: %d", res.Code)
	}

	// And was told it had arrived.
	feed := in.do(http.MethodGet, "/api/notifications", nil, reader)
	if feed.Code != http.StatusOK || !containsKind(feed.Body.String(), "bonus_granted") {
		t.Fatalf("notifications: %d %s", feed.Code, feed.Body.String())
	}
}

func containsKind(body, kind string) bool { return strings.Contains(body, `"`+kind+`"`) }

func TestABarCannotBeSwitchedByAnAccountWhenItIsForced(t *testing.T) {
	in := newInstance(t)
	admin := in.register("bonus-admin", "a-good-password")
	reader := in.register("bonus-reader", "a-good-password")
	bar := decode[struct {
		Bar struct{ ID string } `json:"bar"`
	}](t, in.do(http.MethodPost, "/api/admin/bonus/bars", map[string]any{"name": "forced", "toggle_mode": "on", "show_total": false}, admin)).Bar
	in.do(http.MethodPost, "/api/admin/bonus/bars/"+bar.ID+"/grants", map[string]any{"all": true, "amount": 10}, admin)

	if res := in.do(http.MethodPut, "/api/bonus/"+bar.ID+"/choice", map[string]any{"enabled": false}, reader); res.Code != http.StatusConflict {
		t.Fatalf("switching a forced bar: %d %s", res.Code, res.Body.String())
	}
	views := decode[struct {
		Bars []bonusView `json:"bars"`
	}](t, in.do(http.MethodGet, "/api/bonus", nil, reader)).Bars
	if len(views) != 1 || views[0].Total != nil || views[0].Remaining != nil || views[0].Percent != 100 {
		t.Fatalf("a bar that keeps its total to itself showed %+v", views)
	}
}

func TestOnlyAnAdministratorWithTheUsageGrantMakesBars(t *testing.T) {
	in := newInstance(t)
	in.register("bonus-admin", "a-good-password")
	reader := in.register("bonus-reader", "a-good-password")
	if res := in.do(http.MethodPost, "/api/admin/bonus/bars", map[string]any{"name": "x"}, reader); res.Code != http.StatusForbidden {
		t.Fatalf("a plain account made a bar: %d", res.Code)
	}
}

// The bars are worded the way the allowance is, so the display setting travels
// with them — the same value the allowance's own summary carries, from the same
// place — rather than being something the screen has to fetch separately and
// draw a moment late.
func TestTheBarsAreSentWithTheWayTheInstanceWordsAnAllowance(t *testing.T) {
	in := newInstance(t)
	admin := in.register("bonus-admin", "a-good-password")
	reader := in.register("bonus-reader", "a-good-password")

	word := func(path string) string {
		t.Helper()
		res := in.do(http.MethodGet, path, nil, reader)
		if res.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, res.Code, res.Body.String())
		}
		return decode[struct {
			Display string `json:"display"`
		}](t, res).Display
	}

	if got := word("/api/bonus"); got != "absolute" {
		t.Fatalf("with nothing chosen the bars are worded as %q, want the figures", got)
	}
	for _, chosen := range []string{"remaining", "used", "absolute"} {
		if res := in.do(http.MethodPut, "/api/admin/settings", map[string]string{"quota.usage_display": chosen}, admin); res.Code != http.StatusOK {
			t.Fatalf("choose %s: %d %s", chosen, res.Code, res.Body.String())
		}
		if bars, allowance := word("/api/bonus"), word("/api/usage/me"); bars != chosen || allowance != chosen {
			t.Fatalf("chosen %q: the bars say %q and the allowance %q", chosen, bars, allowance)
		}
	}
}
