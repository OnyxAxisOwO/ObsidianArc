package admin

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/health"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// The page's uptime is the success rate of the whole window, not of the newest
// rows the page reads for its error list. Here the newest 500 probes all failed
// and 100 older ones passed, so a rate taken from the capped sample would read
// as 0 where the window's true rate is 100 of 600.
func TestHealthUptimeCountsTheWholeWindow(t *testing.T) {
	f := newImportFixture(t)
	f.h.health = health.NewStore(f.h.db)
	f.h.settings = settings.New(f.h.db)
	record, err := f.models.Create(f.ctx, model.CreateInput{
		ProviderID: f.provider, ModelID: "vendor/window", DisplayName: "Window", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UnixMilli()
	for i := range 600 {
		ok := i >= 500
		if _, err := f.h.db.Exec(f.ctx,
			`INSERT INTO model_probes (id, model_id, at, ok, code, message, latency_ms) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id.New(), record.ID, now-int64(i)*1000, ok, "error", "", 0); err != nil {
			t.Fatal(err)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/admin/health?hours=24", nil)
	response := httptest.NewRecorder()
	if err := f.h.modelHealth(response, request); err != nil {
		t.Fatalf("modelHealth: %v", err)
	}
	var body struct {
		Models []struct {
			Status struct {
				Uptime  float64 `json:"uptime"`
				Samples int     `json:"samples"`
			} `json:"status"`
		} `json:"models"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Models) != 1 {
		t.Fatalf("models = %d, want 1", len(body.Models))
	}
	status := body.Models[0].Status
	if status.Samples != 600 {
		t.Errorf("samples = %d, want the window's 600", status.Samples)
	}
	if want := 100.0 / 600.0; math.Abs(status.Uptime-want) > 1e-9 {
		t.Errorf("uptime = %v, want %v from the whole window", status.Uptime, want)
	}
}
