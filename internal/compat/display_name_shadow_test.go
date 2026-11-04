package compat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

// A display name is a label, so it must not answer for another model's
// advertised id. The listing, GET /v1/models/{id} and a completion each have to
// name the same model, whichever of them the caller starts from.
func TestDisplayNameDoesNotShadowAnotherModelsAdvertisedID(t *testing.T) {
	f := newFixture(t)
	advertised := f.model.ModelID

	// Sorted first, so a single pass in sort order reaches it before the
	// model that advertises the id.
	if _, err := f.models.Create(context.Background(), model.CreateInput{
		ProviderID: f.model.ProviderID, ModelID: "x-upstream",
		DisplayName: advertised, Enabled: true, SortOrder: -1,
	}); err != nil {
		t.Fatal(err)
	}

	w := f.do(t, http.MethodGet, "/v1/models/"+advertised, f.token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", w.Code, w.Body.String())
	}
	var described modelObject
	if err := json.Unmarshal(w.Body.Bytes(), &described); err != nil {
		t.Fatal(err)
	}
	if described.DisplayName == advertised {
		t.Errorf("GET %q described the model whose display name it is, not the one the listing gives that id", advertised)
	}

	f.upstream.reply(answer)
	if response := f.do(t, http.MethodPost, "/v1/chat/completions", f.token,
		completionBody(advertised)); response.Code != http.StatusOK {
		t.Fatalf("POST status = %d: %s", response.Code, response.Body.String())
	}
	if got := f.upstream.received()["model"]; got != advertised {
		t.Errorf("POST %q reached upstream model %v, want %q", advertised, got, advertised)
	}
}

// Two rows under one unnamed upstream id are both qualified in the listing, so
// the bare id names neither of them. The second row's display name defaults to
// that id; GET used to answer the bare id from it while POST refused it, and
// the two have to agree. A display name the operator chose still resolves.
func TestSharedBareIDIsRefusedByGetAndPostAlike(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bare := f.model.ModelID

	if _, err := f.models.Create(ctx, model.CreateInput{
		ProviderID: f.model.ProviderID, ModelID: bare, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.models.Create(ctx, model.CreateInput{
		ProviderID: f.model.ProviderID, ModelID: "other-upstream",
		DisplayName: "House Fast", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	if w := f.do(t, http.MethodGet, "/v1/models/"+bare, f.token, ""); w.Code != http.StatusNotFound {
		t.Errorf("GET %q: status = %d, want 404: %s", bare, w.Code, w.Body.String())
	}
	f.upstream.reply(answer)
	if w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, completionBody(bare)); w.Code != http.StatusNotFound {
		t.Errorf("POST %q: status = %d, want 404: %s", bare, w.Code, w.Body.String())
	}

	for _, name := range []string{"Mock Fast", "House Fast"} {
		if w := f.do(t, http.MethodGet, "/v1/models/"+url.PathEscape(name), f.token, ""); w.Code != http.StatusOK {
			t.Errorf("GET %q: status = %d: %s", name, w.Code, w.Body.String())
		}
		f.upstream.reply(answer)
		if w := f.do(t, http.MethodPost, "/v1/chat/completions", f.token, completionBody(name)); w.Code != http.StatusOK {
			t.Errorf("POST %q: status = %d: %s", name, w.Code, w.Body.String())
		}
	}
}
