package server

import (
	"net/http"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/health"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// uptimeHandler answers the availability card. It lives in this package
// rather than in internal/health because what it decides is the operator's
// policy — who may see the card, which models it lists, when a rate counts as
// degraded — and the health package records probes without reading settings.
func uptimeHandler(settingsService *settings.Service, healthStore *health.Store, models *model.Store, started time.Time) http.HandlerFunc {
	return httpx.Wrap(func(w http.ResponseWriter, r *http.Request) error {
		account, ok := auth.UserFrom(r.Context())
		if !ok {
			return httpx.Unauthorized("Sign in to view uptime.")
		}
		show := settingsService.Bool(settings.HealthShowUsers)
		if !account.CanAdmin("availability") && !show {
			return httpx.Forbidden("Uptime is not visible to users.")
		}

		resetAt := int64(settingsService.Int(settings.HealthResetAt, 0))
		// The card compares the last hour with a fixed day of history, so
		// changing the probe policy must not change either displayed window.
		now := time.Now()
		windowStart := now.Add(-24 * time.Hour).UnixMilli()
		since := windowStart
		if resetAt > since {
			since = resetAt
		}
		rates, err := healthStore.Rates(r.Context(), since)
		if err != nil {
			return httpx.Internal(err)
		}
		hourSince := max(now.Add(-time.Hour).UnixMilli(), resetAt)
		hourRates, err := healthStore.Rates(r.Context(), hourSince)
		if err != nil {
			return httpx.Internal(err)
		}
		timeline, err := healthStore.Timeline(r.Context(), windowStart, resetAt, 24)
		if err != nil {
			return httpx.Internal(err)
		}

		var (
			modelList []model.Model
		)
		if account.CanAdmin("availability") {
			modelList, err = models.ListAll(r.Context(), "")
		} else {
			modelList, err = models.ListForUser(r.Context(), account.GroupID, false)
		}
		if err != nil {
			return httpx.Internal(err)
		}

		type modelUptimeItem struct {
			ID          string             `json:"id"`
			DisplayName string             `json:"display_name"`
			Provider    string             `json:"provider_name,omitempty"`
			Enabled     bool               `json:"enabled"`
			Uptime      *float64           `json:"uptime,omitempty"`
			UptimeHour  *float64           `json:"uptime_hour,omitempty"`
			State       string             `json:"state"`
			Total       int                `json:"total"`
			History     []health.TimePoint `json:"history"`
		}

		warnBelow := settingsService.Int(settings.HealthWarnBelow, 0)
		result := make([]modelUptimeItem, 0, len(modelList))
		for _, m := range modelList {
			if account.CanAdmin("availability") && !m.Enabled && !m.AutoDisabled {
				continue
			}
			// A model marked hidden is withdrawn from the people using the
			// site; the card is what they read, and an administrator looking
			// at it would otherwise see rows the model list never shows.
			if m.Hidden {
				continue
			}
			rate, hasRate := rates[m.ID]
			item := modelUptimeItem{
				ID:          m.ID,
				DisplayName: m.DisplayName,
				Enabled:     m.Enabled,
				State:       "unknown",
			}
			if account.CanAdmin("availability") {
				item.Provider = m.ProviderName
			}
			if m.AutoDisabled {
				item.State = "down"
			}
			if hasRate && rate.Total > 0 {
				share := rate.Share()
				item.Uptime = &share
				item.Total = rate.Total
			}
			// The card's status badge ("operational", "degraded", "outage")
			// evaluates against the recent one-hour window matching the hourly
			// percentage shown beside it, rather than the 24-hour aggregate.
			if hourRate, hasHour := hourRates[m.ID]; hasHour && hourRate.Total > 0 {
				share := hourRate.Share()
				item.UptimeHour = &share
				if m.AutoDisabled {
					item.State = "down"
				} else {
					item.State = uptimeStateOf(hourRate, warnBelow)
				}
			}

			pts, hasPts := timeline[m.ID]
			if !hasPts || len(pts) == 0 {
				span := (now.UnixMilli() - windowStart) / 24
				if span <= 0 {
					span = 1
				}
				pts = make([]health.TimePoint, 24)
				for i := 0; i < 24; i++ {
					pts[i] = health.TimePoint{At: windowStart + int64(i)*span + span/2, Total: 0}
				}
			}
			item.History = pts

			result = append(result, item)
		}

		uptimeStart := started
		if resetAt > 0 && resetAt > uptimeStart.UnixMilli() {
			uptimeStart = time.UnixMilli(resetAt)
		}
		sec := int64(time.Since(uptimeStart).Seconds())
		if sec < 0 {
			sec = 0
		}

		resp := map[string]any{
			"models": result,
		}
		if account.CanAdmin("availability") || settingsService.Bool(settings.AboutShowSoftwareInfo) {
			resp["uptime_sec"] = sec
		} else {
			resp["uptime_sec"] = 0
		}

		return httpx.WriteJSON(w, http.StatusOK, resp)
	})
}

// uptimeHealthyShare is the hourly share at which a model is up whatever the
// operator's warn_below says: a few failed answers in a busy hour are not a
// reason to warn anyone.
const uptimeHealthyShare = 0.99

// uptimeStateOf classifies a model from its answers in the last hour, which
// must hold at least one answer. Zero answers is an outage rather than
// degraded service, because the evidence is recent and nothing came back.
// Below the healthy share a model is degraded only when the operator asked to
// be warned: a warn_below of 0 means never warn, as the setting's own hint
// says, so nothing below the healthy share is degraded then.
func uptimeStateOf(hour health.Rate, warnBelow int) string {
	switch {
	case hour.OK == 0:
		return "down"
	case hour.Share() >= uptimeHealthyShare:
		return "up"
	case warnBelow > 0 && hour.Share()*100 < float64(warnBelow):
		return "degraded"
	default:
		return "up"
	}
}
