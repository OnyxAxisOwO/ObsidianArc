package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
)

type probeResolver struct{ id, url string }

func (r *probeResolver) Resolve(_ context.Context, id string) (adapter.Provider, error) {
	r.id = id
	return adapter.Provider{Kind: adapter.KindOpenAI, BaseURL: r.url + "/v1", APIKey: "test"}, nil
}

func TestProbeUsesRoutedProviderAndModelButAttributesSource(t *testing.T) {
	checker, models, source := checkerFixture(t)
	ctx := context.Background()
	providers := checker.Providers.(*provider.Store)
	upstream, err := providers.Create(ctx, provider.CreateInput{Name: "Fallback", Kind: adapter.KindOpenAI, BaseURL: "https://fallback.example/v1", APIKey: "key", Enabled: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	target, err := models.Create(ctx, model.CreateInput{ProviderID: upstream.ID, ModelID: "target-model", DisplayName: "Target", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	source, err = models.Update(ctx, source.ID, model.Update{RouteToID: &target.ID})
	if err != nil {
		t.Fatal(err)
	}
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		seen = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	resolver := &probeResolver{url: server.URL}
	checker.Providers = resolver
	checker.Registry = adapter.NewRegistry(config.Upstream{DialTimeout: time.Second, ResponseHeaderTimeout: time.Second})
	checker.Timeout = time.Second
	sample, ok := checker.probe(ctx, source)
	if !ok || !sample.OK {
		t.Fatalf("probe = %+v", sample)
	}
	if resolver.id != upstream.ID || seen != "target-model" {
		t.Fatalf("provider %q model %q", resolver.id, seen)
	}
	rows, err := checker.Store.Samples(ctx, source.ID, 0, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("source samples = %+v err %v", rows, err)
	}
	rows, err = checker.Store.Samples(ctx, target.ID, 0, 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("target samples = %+v err %v", rows, err)
	}
}

func TestRoutedAutoDisabledModelCanRecoverWithSourceProviderOff(t *testing.T) {
	checker, models, source := checkerFixture(t)
	ctx := context.Background()
	providers := checker.Providers.(*provider.Store)
	upstream, err := providers.Create(ctx, provider.CreateInput{Name: "Fallback", Kind: adapter.KindOpenAI, BaseURL: "https://fallback.example/v1", APIKey: "key", Enabled: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	target, err := models.Create(ctx, model.CreateInput{ProviderID: upstream.ID, ModelID: "target-model", DisplayName: "Target", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	off, on := false, true
	source, err = models.Update(ctx, source.ID, model.Update{RouteToID: &target.ID, Enabled: &off, AutoDisabled: &on})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = checker.Store.db.Exec(ctx, `UPDATE providers SET enabled = ? WHERE id = ?`, false, source.ProviderID); err != nil {
		t.Fatal(err)
	}
	checker.apply(ctx, source, Summarise(source.ID, []Sample{pass(100, "system")}), Policy{})
	if got := reload(t, models, source.ID); !got.Enabled || got.AutoDisabled {
		t.Fatalf("routed model did not recover: %+v", got)
	}
}
