package usage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// The history endpoint runs the same raw-ledger aggregates the analytics pages
// run, so it must take the same slot. With every slot held it answers 503 with
// the shared code rather than running the queries anyway.
func TestHistoryWaitsForTheSharedAggregationSlot(t *testing.T) {
	store, account := seriesFixture(t)

	// The shared semaphore holds three.
	for i := 0; i < 3; i++ {
		release, err := httpx.GlobalAggregationSemaphore.Acquire(context.Background())
		if err != nil {
			t.Fatalf("taking slot %d: %v", i+1, err)
		}
		defer release()
	}

	mux := http.NewServeMux()
	NewHandlers(store).Routes(mux)
	request := httptest.NewRequest(http.MethodGet, "/api/usage/me/history", nil)
	request = request.WithContext(auth.WithUser(request.Context(), account))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d with every slot held, want 503: %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, "too_many_concurrent_aggregations") {
		t.Fatalf("body = %s, want the shared busy code", body)
	}
}
