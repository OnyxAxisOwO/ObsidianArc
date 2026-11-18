package usage

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestHistoryCanReadBeyondTheFirstHundredTurns(t *testing.T) {
	store, account := seriesFixture(t)
	for i := range 101 {
		if err := store.Write(t.Context(), Record{ID: fmt.Sprintf("turn-%03d", i), UserID: account.ID, Status: StatusOK, StartedAt: 12000, FinishedAt: 12001}); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/usage/me/history?limit=100&offset=100", nil)
	req = req.WithContext(auth.WithUser(req.Context(), account))
	w := httptest.NewRecorder()
	if err := NewHandlers(store).history(w, req); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Totals Totals
		Turns  []Public
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Turns) != 1 || body.Turns[0].ID != "turn-000" || body.Totals.Requests != 101 {
		t.Fatalf("last page: %+v, %v", body, err)
	}
}
