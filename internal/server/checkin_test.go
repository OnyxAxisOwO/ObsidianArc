package server

import (
	"net/http"
	"testing"
)

func TestCheckInEndToEndThroughTheAPI(t *testing.T) {
	in := newInstance(t)
	admin := in.register("checkin-admin", "a-good-password")
	reader := in.register("checkin-reader", "a-good-password")

	// Off until it is switched on.
	if res := in.do(http.MethodPost, "/api/checkin", nil, reader); res.Code != http.StatusForbidden {
		t.Fatalf("check-in while off: %d %s", res.Code, res.Body.String())
	}

	bar := decode[struct {
		Bar struct{ ID string } `json:"bar"`
	}](t, in.do(http.MethodPost, "/api/admin/bonus/bars", map[string]any{"name": "签到赠金"}, admin)).Bar
	saved := in.do(http.MethodPut, "/api/admin/checkin", map[string]any{
		"enabled": true, "timezone": "Asia/Shanghai",
		"daily": map[string]any{"kind": "bonus", "bar_id": bar.ID, "amount": 0.5, "valid_days": 7},
		"rules": []map[string]any{{
			"id": "d1", "title": "签到 1 天", "basis": "streak", "days": 1,
			"reward": map[string]any{"kind": "card", "name": "签到卡", "windows": []string{"5h"}, "cards": 1, "valid_days": 30},
		}},
	}, admin)
	if saved.Code != http.StatusOK {
		t.Fatalf("save: %d %s", saved.Code, saved.Body.String())
	}
	if bad := in.do(http.MethodPut, "/api/admin/checkin", map[string]any{"enabled": true, "timezone": "Mars/Base"}, admin); bad.Code != http.StatusBadRequest {
		t.Fatalf("a time zone that does not exist: %d", bad.Code)
	}

	status := decode[struct {
		Enabled bool `json:"enabled"`
		Rules   []struct {
			ID        string `json:"id"`
			Claimable bool   `json:"claimable"`
		} `json:"rules"`
	}](t, in.do(http.MethodGet, "/api/checkin", nil, reader))
	if !status.Enabled || len(status.Rules) != 1 || status.Rules[0].Claimable {
		t.Fatalf("status before checking in: %+v", status)
	}
	if res := in.do(http.MethodPost, "/api/checkin/claims/d1", nil, reader); res.Code != http.StatusConflict {
		t.Fatalf("claiming before it is reached: %d %s", res.Code, res.Body.String())
	}

	if res := in.do(http.MethodPost, "/api/checkin", nil, reader); res.Code != http.StatusCreated {
		t.Fatalf("check in: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodPost, "/api/checkin", nil, reader); res.Code != http.StatusConflict {
		t.Fatalf("twice in a day: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodPost, "/api/checkin/claims/d1", nil, reader); res.Code != http.StatusCreated {
		t.Fatalf("claim: %d %s", res.Code, res.Body.String())
	}
	if res := in.do(http.MethodPost, "/api/checkin/claims/d1", nil, reader); res.Code != http.StatusConflict {
		t.Fatalf("claiming twice: %d", res.Code)
	}

	// The reward arrived: the daily grant in the bar, the milestone as a card.
	views := decode[struct {
		Bars []bonusView `json:"bars"`
	}](t, in.do(http.MethodGet, "/api/bonus", nil, reader)).Bars
	if len(views) != 1 || views[0].Remaining == nil || *views[0].Remaining != 0.5 {
		t.Fatalf("the bonus after checking in: %+v", views)
	}
	cards := decode[struct {
		Cards []struct{ Name string } `json:"cards"`
	}](t, in.do(http.MethodGet, "/api/usage/cards", nil, reader)).Cards
	if len(cards) != 1 || cards[0].Name != "签到卡" {
		t.Fatalf("cards: %+v", cards)
	}
}
