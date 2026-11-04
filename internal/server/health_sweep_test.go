package server

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// The checker acts only on the Policy the sweep hands it, and the sweep is
// where the operator's health settings are read. These go through the sweep,
// so a setting the screen accepts but the sweep forgets fails here rather than
// quietly doing nothing; the checker's own tests set the Policy fields
// directly and cannot see that.

func addHealthModel(t *testing.T, in *instance, admin *session, modelID string) string {
	t.Helper()
	provider := decode[map[string]any](t, in.do(http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Upstream", "kind": "openai",
		"base_url": "https://api.example.com/v1", "api_key": "sk-test-key-0123",
	}, admin))["provider"].(map[string]any)["id"].(string)
	created := in.do(http.MethodPost, "/api/admin/models", map[string]any{
		"provider_id": provider, "model_id": modelID, "display_name": "Watched", "enabled": true,
	}, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("create the model: %d %s", created.Code, created.Body.String())
	}
	return decode[map[string]any](t, created)["model"].(map[string]any)["id"].(string)
}

// insertProbe places a verdict at a chosen moment. Recording one through the
// checker stamps it with the current time, which cannot put evidence before a
// reset.
func insertProbe(t *testing.T, in *instance, modelID string, at time.Time, ok bool) {
	t.Helper()
	code := ""
	if !ok {
		code = "timeout"
	}
	if _, err := in.db.Exec(t.Context(),
		`INSERT INTO model_probes (id, model_id, at, ok, code, message, latency_ms)
		 VALUES (?, ?, ?, ?, ?, '', 0)`,
		id.New(), modelID, at.UnixMilli(), ok, code); err != nil {
		t.Fatal(err)
	}
}

func modelEnabled(t *testing.T, in *instance, admin *session, catalogueID string) bool {
	t.Helper()
	listing := decode[map[string]any](t, in.do(http.MethodGet, "/api/admin/models", nil, admin))
	rows, _ := listing["models"].([]any)
	for _, entry := range rows {
		row := entry.(map[string]any)
		if row["id"] == catalogueID {
			return row["enabled"].(bool)
		}
	}
	t.Fatalf("model %s is not in the catalogue", catalogueID)
	return false
}

func TestSweepSwitchesOffAModelBelowTheSuccessThreshold(t *testing.T) {
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	for key, value := range map[string]string{
		settings.HealthProbe:        "false",
		settings.HealthDisableBelow: "80",
	} {
		if err := in.server.settings.Set(t.Context(), key, value); err != nil {
			t.Fatal(err)
		}
	}
	catalogueID := addHealthModel(t, in, admin, "vendor/flaky")

	// Two answers in eight is a model failing three turns in four. The sequence
	// does contain runs of failures, but HealthDisableAfter keeps its default of
	// 0, so only the success-rate rule can switch the model off.
	now := time.Now()
	for i := range 8 {
		insertProbe(t, in, catalogueID, now.Add(-time.Duration(8-i)*time.Minute), i%4 == 0)
	}
	in.server.sweepHealth(t.Context())

	if modelEnabled(t, in, admin, catalogueID) {
		t.Fatal("a model answering a quarter of the time stayed on under an 80% threshold")
	}
}

// Two failures in a row switch a model off. Whether an administrator's reset
// keeps them from doing it again is what the second case checks.
func sweepAfterTwoFailures(t *testing.T, reset bool) bool {
	t.Helper()
	in := newInstance(t)
	admin := in.register("founder", "a-good-password")
	for key, value := range map[string]string{
		settings.HealthProbe:        "false",
		settings.HealthDisableAfter: "2",
	} {
		if err := in.server.settings.Set(t.Context(), key, value); err != nil {
			t.Fatal(err)
		}
	}
	catalogueID := addHealthModel(t, in, admin, "vendor/down")

	failed := time.Now().Add(-2 * time.Minute)
	insertProbe(t, in, catalogueID, failed, false)
	insertProbe(t, in, catalogueID, failed.Add(time.Second), false)
	if reset {
		resetAt := strconv.FormatInt(time.Now().Add(-time.Minute).UnixMilli(), 10)
		if err := in.server.settings.Set(t.Context(), settings.HealthResetAt, resetAt); err != nil {
			t.Fatal(err)
		}
	}

	in.server.sweepHealth(t.Context())
	return modelEnabled(t, in, admin, catalogueID)
}

func TestSweepCountsFailuresBeforeAResetAsEvidence(t *testing.T) {
	// Without a reset the failures are evidence, which is what keeps the
	// second case from passing for the wrong reason.
	t.Run("without a reset the failures switch the model off", func(t *testing.T) {
		if sweepAfterTwoFailures(t, false) {
			t.Fatal("two failures in a row left the model on")
		}
	})
	t.Run("after a reset the same failures do not switch it off again", func(t *testing.T) {
		if !sweepAfterTwoFailures(t, true) {
			t.Fatal("failures from before an administrator's reset switched the model off again")
		}
	})
}
