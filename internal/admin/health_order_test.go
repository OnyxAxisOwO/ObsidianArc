package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/health"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// A reset that fails before its last step leaves the uptime baseline where it
// was. The baseline used to be written first, so a reset that reported failure
// had already moved the window the page measured from.
func TestAFailedHealthResetLeavesTheBaselineAlone(t *testing.T) {
	f := newImportFixture(t)
	f.h.health = health.NewStore(f.h.db)
	f.h.settings = settings.New(f.h.db)
	if err := f.h.settings.Set(f.ctx, settings.HealthResetAt, "42"); err != nil {
		t.Fatal(err)
	}
	// Clearing the probes is the step that fails.
	if _, err := f.h.db.Exec(f.ctx, `DROP TABLE model_probes`); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/admin/health/reset", nil)
	if err := f.h.resetHealth(httptest.NewRecorder(), request); err == nil {
		t.Fatal("the reset succeeded without its probe table")
	}
	if got := f.h.settings.Int(settings.HealthResetAt, 0); got != 42 {
		t.Errorf("baseline = %d after a failed reset, want 42 unchanged", got)
	}
}

// A model with no evidence in the window is answered without its per-model
// sample query, and must still come back with exactly the status it always had:
// the same summary of an empty sample list, with no samples and no uptime.
func TestAnIdleModelIsReportedAsItWasBeforeTheSkip(t *testing.T) {
	f := newImportFixture(t)
	f.h.health = health.NewStore(f.h.db)
	f.h.settings = settings.New(f.h.db)
	record, err := f.models.Create(f.ctx, model.CreateInput{
		ProviderID: f.provider, ModelID: "vendor/idle", DisplayName: "Idle", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Evidence older than the window must not count, so the model is idle as far
	// as the page is concerned even though a row exists.
	if _, err := f.h.db.Exec(f.ctx,
		`INSERT INTO model_probes (id, model_id, at, ok, code, message, latency_ms) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id.New(), record.ID, int64(1), false, "error", "", 0); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/admin/health?hours=24", nil)
	response := httptest.NewRecorder()
	if err := f.h.modelHealth(response, request); err != nil {
		t.Fatalf("modelHealth: %v", err)
	}
	var body struct {
		Models []struct {
			Status json.RawMessage `json:"status"`
		} `json:"models"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Models) != 1 {
		t.Fatalf("models = %d, want 1", len(body.Models))
	}

	// The status the page built before the skip: an empty, non-nil sample list.
	want, err := json.Marshal(health.Summarise(record.ID, []health.Sample{}))
	if err != nil {
		t.Fatal(err)
	}
	var got, expected any
	if err := json.Unmarshal(body.Models[0].Status, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("idle model's status = %v, want %v", got, expected)
	}
}
