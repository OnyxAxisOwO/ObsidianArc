package adapter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Anthropic pages its model list and says so with has_more and last_id. A
// listing that read only the first page would drop every model past it, and
// nothing in the result would say so.
func TestAnthropicModelListingFollowsTheCursor(t *testing.T) {
	var (
		mu    sync.Mutex
		query []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		query = append(query, r.URL.RawQuery)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("after_id") {
		case "":
			fmt.Fprint(w, `{"data":[{"id":"claude-a","display_name":"Claude A"}],"has_more":true,"first_id":"claude-a","last_id":"claude-a"}`)
		case "claude-a":
			fmt.Fprint(w, `{"data":[{"id":"claude-b"}],"has_more":false,"first_id":"claude-b","last_id":"claude-b"}`)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	models, err := testRegistry().ListModels(context.Background(),
		Provider{Kind: KindAnthropic, BaseURL: server.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 2 || models[0].ID != "claude-a" || models[1].ID != "claude-b" {
		t.Fatalf("models = %+v, want the models from both pages", models)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(query) != 2 {
		t.Fatalf("requests = %v, want one per page", query)
	}
	if query[1] != "limit=200&after_id=claude-a" {
		t.Errorf("second page asked with %q, want the cursor after the first page's last id", query[1])
	}
}

// A listing that always says there is more is cut off at the page cap rather
// than held open by the endpoint.
func TestAnthropicModelListingStopsAtThePageCap(t *testing.T) {
	var (
		mu    sync.Mutex
		pages int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		pages++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"claude-x"}],"has_more":true,"last_id":"claude-x"}`)
	}))
	defer server.Close()

	models, err := testRegistry().ListModels(context.Background(),
		Provider{Kind: KindAnthropic, BaseURL: server.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if pages != maxModelPages {
		t.Errorf("requested %d pages, want the cap of %d", pages, maxModelPages)
	}
	if len(models) != maxModelPages {
		t.Errorf("got %d models, want one per page read", len(models))
	}
}

// The OpenAI listing has no cursor, so a has_more field in one of its answers
// is not a request for the next page.
func TestOpenAIModelListingReadsOnlyTheFirstPage(t *testing.T) {
	var (
		mu       sync.Mutex
		requests int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpt-a"}],"has_more":true,"last_id":"gpt-a"}`)
	}))
	defer server.Close()

	models, err := testRegistry().ListModels(context.Background(),
		Provider{Kind: KindOpenAI, BaseURL: server.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 || len(models) != 1 {
		t.Errorf("requests = %d, models = %+v; want one page", requests, models)
	}
}
