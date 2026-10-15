package trial

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// openTrialSettings opens a fresh database with the front door set to the trial,
// so a test reaches the handler's own checks rather than the landing gate.
func openTrialSettings(t *testing.T) (*database.DB, *settings.Service) {
	t.Helper()
	ctx := t.Context()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "trial.db"),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	set := settings.New(db)
	if err := set.Load(ctx); err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if err := set.Set(ctx, settings.LandingMode, settings.LandingChat); err != nil {
		t.Fatalf("set landing mode: %v", err)
	}
	if err := set.Set(ctx, settings.TrialEnabled, "true"); err != nil {
		t.Fatalf("enable trial: %v", err)
	}
	return db, set
}

// A turn that finds no trial model is refused after the budget has admitted it,
// and the charge is handed back. Without the hand-back, every visitor to an
// instance that has not chosen a model would spend the hourly ceiling on 503s
// and then be refused with 429 for the rest of the window.
func TestTurnsWithNoTrialModelGiveTheirChargeBack(t *testing.T) {
	_, set := openTrialSettings(t)

	h := &Handlers{
		settings: set,
		budget:   newBudget(),
		signer:   newSigner([]byte("test-secret")),
	}
	handler := httpx.Wrap(h.chat)

	for i := 0; i < 2*burstPerInstance; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/trial/chat",
			bytes.NewReader([]byte(`{"messages":[{"role":"user","content":"hello"}]}`)))
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("turn %d: status %d, want 503 with no trial model: %s", i+1, w.Code, w.Body.String())
		}
	}

	h.budget.mu.Lock()
	defer h.budget.mu.Unlock()
	if h.budget.instance.spent != 0 {
		t.Errorf("instance spent %d across refused turns, want 0", h.budget.instance.spent)
	}
	if h.budget.inFlight != 0 {
		t.Errorf("%d slots held after refused turns, want 0", h.budget.inFlight)
	}
}

// A turn whose stream cannot open has resolved a model but never reaches the
// provider, so its charge is handed back, as a turn with no model is. The
// stream is refused by a writer that cannot flush, which is the only way
// httpx.NewSSE fails, and the base URL below is never dialled.
func TestTurnsThatCannotStreamGiveTheirChargeBack(t *testing.T) {
	ctx := t.Context()
	db, set := openTrialSettings(t)

	box, err := secret.New([]byte("a-test-instance-secret-value"), secret.PurposeProviderKey)
	if err != nil {
		t.Fatal(err)
	}
	providers := provider.NewStore(db, box)
	upstream, err := providers.Create(ctx, provider.CreateInput{
		Name: "Unreached", Kind: adapter.KindOpenAI,
		BaseURL: "http://127.0.0.1:1/v1", APIKey: "sk-test", Enabled: true,
	}, true)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	models := model.NewStore(db, providers)
	trialModel, err := models.Create(ctx, model.CreateInput{
		ProviderID: upstream.ID, ModelID: "trial-model", DisplayName: "Trial Model",
		Enabled:      true,
		Capabilities: model.Capabilities{SupportsStreaming: true},
		Weights:      model.Weights{InputToken: 1, OutputToken: 1},
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := set.Set(ctx, settings.TrialModel, trialModel.ID); err != nil {
		t.Fatalf("set trial model: %v", err)
	}

	h := &Handlers{
		settings: set,
		models:   models,
		registry: adapter.NewRegistry(config.Upstream{}),
		budget:   newBudget(),
		signer:   newSigner([]byte("test-secret")),
	}
	r := httptest.NewRequest(http.MethodPost, "/api/trial/chat",
		bytes.NewReader([]byte(`{"messages":[{"role":"user","content":"hello"}]}`)))
	w := &unstreamable{header: http.Header{}}
	httpx.Wrap(h.chat)(w, r)

	if w.status != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 for a stream that cannot open", w.status)
	}
	address := httpx.ClientIP(r, h.trust)
	h.budget.mu.Lock()
	defer h.budget.mu.Unlock()
	if h.budget.instance.spent != 0 {
		t.Errorf("instance spent %d after a turn that never streamed, want 0", h.budget.instance.spent)
	}
	if entry := h.budget.windows[budgetKey(address)]; entry == nil || entry.spent != 0 {
		t.Errorf("address window not refunded: %+v", entry)
	}
	if h.budget.inFlight != 0 {
		t.Errorf("%d slots held after the refused stream, want 0", h.budget.inFlight)
	}
}

// unstreamable is a ResponseWriter with no way to flush, which httpx.NewSSE
// refuses.
type unstreamable struct {
	header http.Header
	status int
}

func (u *unstreamable) Header() http.Header { return u.header }

func (u *unstreamable) Write(p []byte) (int, error) { return len(p), nil }

func (u *unstreamable) WriteHeader(code int) { u.status = code }
