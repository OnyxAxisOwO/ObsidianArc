package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/health"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// The default window an operator sees, and the widest they may ask for. A
// month of samples for every model is a page nobody reads and a query nobody
// enjoys.
const (
	defaultHealthHours = 24
	maxHealthHours     = 24 * 30
)

// modelHealth answers with one entry per model, whether or not it has any
// evidence: a model missing from the list would read as a model that is fine.
func (h *Handlers) modelHealth(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	hours := defaultHealthHours
	if raw := r.URL.Query().Get("hours"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > maxHealthHours {
			return httpx.BadRequest("hours must be between 1 and 720.")
		}
		hours = parsed
	}

	models, err := h.models.ListAll(ctx, "")
	if err != nil {
		return httpx.Internal(err)
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour).UnixMilli()
	resetAt := int64(h.settings.Int(settings.HealthResetAt, 0))
	if resetAt > since {
		since = resetAt
	}

	entries := make([]map[string]any, 0, len(models))
	for _, record := range models {
		samples, err := h.health.Samples(ctx, record.ID, since, 500)
		if err != nil {
			return httpx.Internal(err)
		}
		status := health.Summarise(record.ID, samples)
		entries = append(entries, map[string]any{
			"model_id":      record.ID,
			"name":          record.DisplayName,
			"provider":      record.ProviderName,
			"enabled":       record.Enabled,
			"auto_disabled": record.AutoDisabled,
			"status":        status,
		})
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"hours":  hours,
		"models": entries,
		// So the page can say what it is measuring against rather than
		// leaving an operator to guess why everything is "unknown".
		"policy": map[string]any{
			"probe":         h.settings.Bool(settings.HealthProbe),
			"window_mins":   h.settings.Int(settings.HealthWindowMins, 30),
			"disable_after": h.settings.Int(settings.HealthDisableAfter, 0),
			"disable_below": h.settings.Int(settings.HealthDisableBelow, 0),
			"warn_below":    h.settings.Int(settings.HealthWarnBelow, 90),
			"show_users":    h.settings.Bool(settings.HealthShowUsers),
			"retain_days":   h.settings.Int(settings.HealthRetainDays, 14),
			"reset_at":      resetAt,
		},
	})
}

// resetHealth resets the instance's uptime baseline, clears past probe samples,
// and re-enables models that were auto-disabled due to previous failures.
func (h *Handlers) resetHealth(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	now := time.Now().UnixMilli()

	// Record when uptime was reset so that readers and administrators alike
	// measure from this moment rather than from server start.
	if err := h.settings.Set(ctx, settings.HealthResetAt, strconv.FormatInt(now, 10)); err != nil {
		return httpx.Internal(err)
	}

	// Drop past probes so that the health store does not carry dead
	// samples across a reset.
	dropped, err := h.health.Reset(ctx)
	if err != nil {
		return httpx.Internal(err)
	}

	// Re-enable any models the checker auto-disabled for past failures, so
	// that a reset gives them a clean start.
	models, err := h.models.ListAll(ctx, "")
	if err != nil {
		return httpx.Internal(err)
	}
	reenabled := 0
	for _, m := range models {
		if m.AutoDisabled {
			on, off := true, false
			if _, err := h.models.Update(ctx, m.ID, model.Update{
				Enabled:      &on,
				AutoDisabled: &off,
			}); err == nil {
				reenabled++
			}
		}
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"reset_at":         now,
		"probes_cleared":   dropped,
		"models_reenabled": reenabled,
	})
}
