package admin

import (
	"context"
	"log/slog"
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
	manualProbeWorkers = 4
	manualProbeTimeout = 20 * time.Second
	// The newest evidence per model that the page reads for its error list
	// and its current state. Counting the window is Rates' job.
	healthSampleCap = 500
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

	// Uptime and counts come from the whole window in one query. The capped
	// sample below only feeds the error list and the current state, and
	// reading the uptime from it would describe the newest few hours as the
	// period's figure.
	rates, err := h.health.Rates(ctx, since)
	if err != nil {
		return httpx.Internal(err)
	}

	entries := make([]map[string]any, 0, len(models))
	for _, record := range models {
		rate := rates[record.ID]
		// Rates counts exactly the rows Samples reads for a model: the same window
		// and the same statuses. A model it has none for has no samples either, so
		// the per-model query is skipped for idle models rather than run to find
		// nothing.
		samples := []health.Sample{}
		if rate.Total > 0 {
			samples, err = h.health.Samples(ctx, record.ID, since, healthSampleCap)
			if err != nil {
				return httpx.Internal(err)
			}
		}
		status := health.Summarise(record.ID, samples)
		status.Samples = rate.Total
		status.Uptime = rate.Share()
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

// probeAllHealth gives an operator a fresh observation of every configured
// model, including disabled ones. A manual diagnostic should be able to show
// that a model is healthy before the operator decides to enable it again.
func (h *Handlers) probeAllHealth(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if h.probeBudget != nil && !h.probeBudget.Allow("probe") {
		return httpx.TooManyRequests("probe_cooldown", "All models can be tested once every thirty seconds.")
	}
	records, err := h.models.ListAll(ctx, "")
	if err != nil {
		return httpx.Internal(err)
	}
	stream, err := httpx.NewSSE(w)
	if err != nil {
		return httpx.Internal(err)
	}

	state := probeProgress{Total: len(records)}
	if err := stream.Event("progress", state); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}

	jobs := make(chan model.Model, len(records))
	results := make(chan probeOutcome, len(records))
	for _, record := range records {
		jobs <- record
	}
	close(jobs)

	workers := min(manualProbeWorkers, len(records))
	for range workers {
		go func() {
			for record := range jobs {
				result := probeOutcome{modelID: record.ID}
				// The operator switched this provider's key off, so probing through
				// it would spend credit they stopped and record the refusal as
				// health evidence. Probing a disabled model stays allowed: that is
				// the diagnostic an operator asks for before turning it back on.
				owner, ownerErr := h.providers.ByID(ctx, record.ProviderID)
				if ownerErr != nil {
					result.code = "provider_unavailable"
					result.message = ownerErr.Error()
					results <- result
					continue
				}
				if !owner.Enabled {
					result.skipped = true
					results <- result
					continue
				}
				upstream, resolveErr := h.providers.Resolve(ctx, record.ProviderID)
				if resolveErr != nil {
					result.code = "provider_unavailable"
					result.message = resolveErr.Error()
					results <- result
					continue
				}

				probeCtx, cancel := context.WithTimeout(ctx, manualProbeTimeout)
				var probeErr error
				result.latency, probeErr = health.Probe(probeCtx, h.registry, upstream, record.Spec())
				cancel()
				result.ok = probeErr == nil
				if probeErr != nil {
					result.code = health.Code(probeErr)
					result.message = probeErr.Error()
				}
				results <- result
			}
		}()
	}

	final, err := h.recordProbes(ctx, results, len(records), stream)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	// A client that left has no one to read the closing event, and the write
	// would fail after the response is already streaming, which httpx.Wrap
	// would then record as a failed request.
	if ctx.Err() != nil {
		return nil
	}
	return stream.Event("done", final)
}

// probeOutcome is one probe as a worker hands it to the loop that records it.
type probeOutcome struct {
	modelID string
	ok      bool
	// skipped means nothing was sent to the provider, so there is nothing to
	// record.
	skipped bool
	code    string
	message string
	latency time.Duration
}

// probeProgress is what the screen is told after each probe.
type probeProgress struct {
	Completed int `json:"completed"`
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
}

// probeRecordTimeout bounds the write for a probe that finished after the
// client left. It is one row; a database that cannot take it in this long
// should not hold the request's goroutine.
const probeRecordTimeout = 5 * time.Second

// cutShort reports a result the disconnect itself produced. A probe the
// cancellation ended reads as cancelled, and a resolve it ended reads as an
// unavailable provider, which the provider may not be.
func (o probeOutcome) cutShort() bool {
	return o.code == "cancelled" || o.code == "provider_unavailable"
}

// recordProbes stores each probe as its result arrives and returns the
// progress it reached. Once the client has gone nothing more is streamed, but
// a probe that finished before it left has spent provider credit, so its
// outcome is still recorded, on a context the disconnect cannot cancel. Every
// result is read even after that, because each worker sends exactly one.
func (h *Handlers) recordProbes(ctx context.Context, results <-chan probeOutcome, total int, stream *httpx.SSE) (probeProgress, error) {
	state := probeProgress{Total: total}
	for range total {
		result := <-results
		gone := ctx.Err() != nil
		if gone && result.cutShort() {
			continue
		}
		if !result.skipped {
			recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeRecordTimeout)
			err := h.health.Record(recordCtx, result.modelID, result.ok, result.code, result.message, result.latency)
			cancel()
			if err != nil {
				if !gone {
					_ = stream.Event("error", map[string]string{"code": "record_failed"})
				}
				return state, httpx.Internal(err)
			}
		}
		state.Completed++
		switch {
		case result.skipped:
			state.Skipped++
		case result.ok:
			state.Succeeded++
		default:
			state.Failed++
		}
		if gone {
			continue
		}
		if err := stream.Event("progress", state); err != nil {
			return state, err
		}
	}
	return state, nil
}

// resetHealth resets the instance's uptime baseline, clears past probe samples,
// and re-enables models that were auto-disabled due to previous failures.
func (h *Handlers) resetHealth(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	now := time.Now().UnixMilli()

	// Read first, so a failure here changes nothing at all.
	models, err := h.models.ListAll(ctx, "")
	if err != nil {
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
	reenabled, refused := reenableAutoDisabled(ctx, models, h.models.Update)

	// Record when uptime was reset so that readers and administrators alike
	// measure from this moment rather than from server start. Written last: until
	// it lands the page still measures from the old baseline, so a failure before
	// this point leaves the baseline where it was, and a retry runs the reset
	// through again.
	if err := h.settings.Set(ctx, settings.HealthResetAt, strconv.FormatInt(now, 10)); err != nil {
		return httpx.Internal(err)
	}

	response := map[string]any{
		"reset_at":         now,
		"probes_cleared":   dropped,
		"models_reenabled": reenabled,
	}
	// Only when there is something to say, so the ordinary response does not
	// grow a field that is always empty.
	if len(refused) > 0 {
		response["models_not_reenabled"] = refused
	}
	return httpx.WriteJSON(w, http.StatusOK, response)
}

// reenableAutoDisabled clears the checker's own switch on every model it set,
// and returns the ones whose write was refused.
//
// The count on its own was a lie in the making: a database that refused the
// update left the model disabled while the endpoint still answered 200 with a
// smaller number, so an operator could not tell a model that was already
// healthy from one the reset failed to reach. Naming them makes a partial
// reset legible, and each failure is logged with its cause.
//
// The update is a parameter rather than the store so that both halves — the
// one that succeeds and the one that does not — are reachable without a
// database.
func reenableAutoDisabled(
	ctx context.Context,
	models []model.Model,
	update func(context.Context, string, model.Update) (model.Model, error),
) (reenabled int, refused []string) {
	for _, m := range models {
		if !m.AutoDisabled {
			continue
		}
		on, off := true, false
		updated, err := update(ctx, m.ID, model.Update{Enabled: &on, AutoDisabled: &off})
		if err != nil {
			slog.ErrorContext(ctx, "could not re-enable a model after a health reset",
				"error", err, "model", m.ID)
			refused = append(refused, m.ID)
			continue
		}
		// A write that succeeds can still leave the model off: the store keeps
		// a model of a disabled provider off whatever it is asked. That is a
		// refusal too, and not one the count should claim.
		if !updated.Enabled {
			slog.WarnContext(ctx, "a health reset could not re-enable a model; its provider is off",
				"model", m.ID)
			refused = append(refused, m.ID)
			continue
		}
		reenabled++
	}
	return reenabled, refused
}
