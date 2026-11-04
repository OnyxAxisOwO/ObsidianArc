package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/health"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
)

// The probes that finished before the client left are the ones that cost
// provider credit, so their outcomes are recorded after the disconnect. The
// two that the cancellation itself ended say nothing about the model and are
// dropped. The results are queued in advance, so the loop reads a finished
// result with the context already cancelled, which is the case that used to
// drop everything.
func TestProbesFinishedBeforeADisconnectAreStillRecorded(t *testing.T) {
	f := newImportFixture(t)
	f.h.health = health.NewStore(f.h.db)

	ids := make([]string, 4)
	for i := range ids {
		record, err := f.models.Create(f.ctx, model.CreateInput{
			ProviderID: f.provider, ModelID: fmt.Sprintf("vendor/probe-%d", i), DisplayName: "Probe", Enabled: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = record.ID
	}

	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	results := make(chan probeOutcome, len(ids))
	results <- probeOutcome{modelID: ids[0], ok: true, latency: 5 * time.Millisecond}
	results <- probeOutcome{modelID: ids[1], code: "unauthorized", message: "rejected"}
	results <- probeOutcome{modelID: ids[2], code: "cancelled", message: "context canceled"}
	results <- probeOutcome{modelID: ids[3], code: "provider_unavailable", message: "context canceled"}

	stream, err := httpx.NewSSE(httptest.NewRecorder())
	if err != nil {
		t.Fatal(err)
	}
	final, err := f.h.recordProbes(ctx, results, len(ids), stream)
	if err != nil {
		t.Fatalf("recordProbes: %v", err)
	}
	if final.Completed != 2 || final.Succeeded != 1 || final.Failed != 1 {
		t.Fatalf("progress = %+v, want 2 completed, 1 succeeded, 1 failed", final)
	}

	for i, want := range []int{1, 1, 0, 0} {
		var stored int
		if err := f.h.db.QueryRow(f.ctx, `SELECT COUNT(*) FROM model_probes WHERE model_id = ?`, ids[i]).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != want {
			t.Errorf("probe %d recorded %d times, want %d", i, stored, want)
		}
	}
}

// The operator switched this provider's key off. A manual probe must not send
// that key upstream, and must not record the refusal as health evidence: the
// model is reported as skipped instead. The upstream here is a local server
// that counts what reaches it, so a probe that goes out is visible.
func TestAProviderThatIsOffIsNotProbed(t *testing.T) {
	var reached atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)

	f := newImportFixture(t)
	f.h.health = health.NewStore(f.h.db)
	f.h.registry = adapter.NewRegistry(config.Upstream{})
	f.h.probeBudget = unlimitedBudget()
	off, err := f.providers.Create(f.ctx, provider.CreateInput{
		Name: "Switched off", Kind: adapter.KindOpenAI,
		BaseURL: upstream.URL, APIKey: "sk-test-0456", Enabled: false,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	record, err := f.models.Create(f.ctx, model.CreateInput{
		ProviderID: off.ID, ModelID: "vendor/off", DisplayName: "Off", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/models/health/probe", nil)
	if err := f.h.probeAllHealth(response, request); err != nil {
		t.Fatalf("probeAllHealth: %v", err)
	}

	if n := reached.Load(); n != 0 {
		t.Errorf("the upstream of a switched-off provider was reached %d times, want 0", n)
	}
	var recorded int
	if err := f.h.db.QueryRow(f.ctx, `SELECT COUNT(*) FROM model_probes WHERE model_id = ?`, record.ID).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 0 {
		t.Errorf("a skipped probe was recorded %d times, want 0", recorded)
	}
	if !strings.Contains(response.Body.String(), `"skipped":1`) {
		t.Errorf("done event %q does not count the skipped model", response.Body.String())
	}
}

// Listing a switched-off provider's models would send its key upstream just as
// a probe would, so the listing is refused before the key is read.
func TestDetectingModelsThroughAProviderThatIsOffIsRefused(t *testing.T) {
	var reached atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)

	f := newImportFixture(t)
	f.h.registry = adapter.NewRegistry(config.Upstream{})
	off, err := f.providers.Create(f.ctx, provider.CreateInput{
		Name: "Switched off", Kind: adapter.KindOpenAI,
		BaseURL: upstream.URL, APIKey: "sk-test-0789", Enabled: false,
	}, true)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/admin/providers/"+off.ID+"/models/detect", nil)
	request.SetPathValue("id", off.ID)
	err = f.h.detectModels(httptest.NewRecorder(), request)
	var refused *httpx.Error
	if !errors.As(err, &refused) || refused.Status != http.StatusConflict {
		t.Fatalf("detect through a switched-off provider = %v, want a 409", err)
	}
	if n := reached.Load(); n != 0 {
		t.Errorf("the upstream of a switched-off provider was reached %d times, want 0", n)
	}
}
