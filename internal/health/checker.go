package health

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

// Policy is what an operator has asked for. Read fresh on every pass, so a
// change in the settings screen takes effect on the next one rather than at
// the next restart.
type Policy struct {
	// Ask models nobody has used. Off means uptime comes from readers only,
	// and a model with no readers stays unknown.
	Probe bool
	// How far back counts as evidence, and therefore how quiet a model has to
	// be before it is asked directly.
	Window time.Duration
	// Turn a model off after this many failures in a row, and on again when
	// it answers. Zero means never.
	DisableAfter int
	// Turn a model off when its success rate over the window falls below this
	// percentage. Zero means never. Separate from DisableAfter because a
	// model can be badly broken without ever failing twice in a row — one
	// turn in four, all afternoon, is an outage nobody's counter catches.
	DisableBelow int
	// How long probe rows are kept.
	Retain time.Duration
	// When uptime was last reset by an administrator. Evidence before this
	// moment is excluded so a reset does not re-disable a model on old rows.
	ResetAt int64
}

// Checker runs one pass of the liveness policy.
//
// It is a plain method rather than a goroutine with a ticker of its own: the
// server already has a sweep on a timer, and a second one would be a second
// thing to reason about when something runs at the wrong moment.
type Checker struct {
	Store     *Store
	Models    *model.Store
	Providers ProviderResolver
	Registry  *adapter.Registry
	// How long one probe may take. A model that has not answered in this long
	// has failed as far as a reader is concerned.
	Timeout time.Duration
}

// ProviderResolver is the one thing the checker needs from the provider store:
// a provider it can actually call, key and all. Named here rather than taking
// the store itself so this package does not depend on the whole of that one.
type ProviderResolver interface {
	Resolve(ctx context.Context, providerID string) (adapter.Provider, error)
}

// Run does one pass: probe what is quiet, then apply the enable and disable
// rules to everything.
//
// Errors are logged and not returned. A pass is a background sweep with
// nobody waiting on it, and one unreachable provider must not stop the models
// after it in the list from being checked.
func (c *Checker) Run(ctx context.Context, policy Policy) {
	if policy.Window <= 0 {
		policy.Window = 15 * time.Minute
	}
	if c.Timeout <= 0 {
		c.Timeout = 20 * time.Second
	}

	models, err := c.Models.ListAll(ctx, "")
	if err != nil {
		slog.ErrorContext(ctx, "health: list models", "error", err)
		return
	}
	since := time.Now().Add(-policy.Window).UnixMilli()
	if policy.ResetAt > since {
		since = policy.ResetAt
	}

	for _, record := range models {
		// A model an administrator switched off is not a model that is down.
		// The exception is one this checker switched off, which has to keep
		// being asked or it could never come back.
		if !record.Enabled && !record.AutoDisabled {
			continue
		}

		samples, err := c.Store.Samples(ctx, record.ID, since, 200)
		if err != nil {
			slog.ErrorContext(ctx, "health: read samples", "model", record.ID, "error", err)
			continue
		}

		// Readers first. A model with traffic has better evidence than
		// anything a probe could add, and asking it again costs tokens to
		// learn nothing.
		if len(samples) == 0 && policy.Probe {
			if sample, ok := c.probe(ctx, record); ok {
				samples = append([]Sample{sample}, samples...)
			}
		}
		if len(samples) == 0 {
			continue
		}

		c.apply(ctx, record, Summarise(record.ID, samples), policy)
	}

	if policy.Retain > 0 {
		if _, err := c.Store.Prune(ctx, time.Now().Add(-policy.Retain).UnixMilli()); err != nil {
			slog.ErrorContext(ctx, "health: prune probes", "error", err)
		}
	}
}

func (c *Checker) probe(ctx context.Context, record model.Model) (Sample, bool) {
	upstream, err := c.Providers.Resolve(ctx, record.ProviderID)
	if err != nil {
		// The provider is unreadable — a changed secret key, or it has been
		// deleted out from under the model. That is a failure of this model
		// from where a reader stands, so it is recorded as one.
		_ = c.Store.Record(ctx, record.ID, false, "provider_unavailable", err.Error(), 0)
		return Sample{At: time.Now().UnixMilli(), Code: "provider_unavailable",
			Message: err.Error(), Source: "system"}, true
	}

	probeCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	latency, err := Probe(probeCtx, c.Registry, upstream, record.Spec())
	code, message := "", ""
	if err != nil {
		code, message = Code(err), err.Error()
	}
	if writeErr := c.Store.Record(ctx, record.ID, err == nil, code, message, latency); writeErr != nil {
		slog.ErrorContext(ctx, "health: record probe", "model", record.ID, "error", writeErr)
	}
	return Sample{
		At: time.Now().UnixMilli(), OK: err == nil,
		Code: code, Message: message, Source: "system",
	}, true
}

// apply is the only place a model's enabled flag moves on its own.
func (c *Checker) apply(ctx context.Context, record model.Model, status Status, policy Policy) {
	on, off := true, false

	// Back from the dead. Only a model this checker turned off: an operator
	// who disabled one has said something, and a model answering again is not
	// an argument against it.
	if record.AutoDisabled && status.State == StateUp {
		if _, err := c.Models.Update(ctx, record.ID, model.Update{
			Enabled: &on, AutoDisabled: &off,
		}); err != nil {
			slog.ErrorContext(ctx, "health: re-enable", "model", record.ID, "error", err)
			return
		}
		slog.InfoContext(ctx, "model answering again, enabled",
			"model", record.ID, "name", record.DisplayName)
		return
	}

	if !record.Enabled {
		return
	}

	reason, why := "", ""
	switch {
	case policy.DisableAfter > 0 && status.FailuresInARow >= policy.DisableAfter:
		reason = "failures in a row"
		why = fmt.Sprintf("%d in a row", status.FailuresInARow)
	case policy.DisableBelow > 0 &&
		status.Samples >= MinSamplesToJudge &&
		status.Uptime*100 < float64(policy.DisableBelow):
		reason = "success rate"
		why = fmt.Sprintf("%.0f%% of %d", status.Uptime*100, status.Samples)
	default:
		return
	}

	if _, err := c.Models.Update(ctx, record.ID, model.Update{
		Enabled: &off, AutoDisabled: &on,
	}); err != nil {
		slog.ErrorContext(ctx, "health: disable", "model", record.ID, "error", err)
		return
	}
	slog.WarnContext(ctx, "model disabled",
		"model", record.ID, "name", record.DisplayName,
		"reason", reason, "detail", why, "code", status.LastCode)
}
