package server

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// The request logger's final drain is a database write that begins only after
// cancellation. Shutdown closes the database straight after stopping the
// janitor, so the done channel must not close before that write has landed:
// a write that has not begun by then fails with "database is closed", and the
// last requests of the run are lost. Done closing early is what this checks,
// because the logger's flush ticker has not fired yet when the cancel comes.
func TestJanitorDoneWaitsForTheFinalRequestLogWrite(t *testing.T) {
	in := newInstance(t)
	const requests = 3
	for range requests {
		if response := in.do(http.MethodGet, "/api/site", nil, nil); response.Code != http.StatusOK {
			t.Fatalf("GET /api/site: %d %s", response.Code, response.Body.String())
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := in.server.StartJanitor(ctx)

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the janitor did not stop after its context was cancelled")
	}

	var logged int
	if err := in.db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM request_log`).Scan(&logged); err != nil {
		t.Fatalf("count request log: %v", err)
	}
	if logged < requests {
		t.Fatalf("request log has %d rows after the janitor stopped, want at least %d", logged, requests)
	}
	if dropped := in.server.requests.Dropped(); dropped != 0 {
		t.Fatalf("request log dropped %d entries on shutdown", dropped)
	}
}
