package server

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// A failed prune of the usage counters is logged by the janitor. Nothing else
// can say so: the sweep runs with no request behind it, and the request log is
// written to the same database that has just failed. The table is dropped so the
// prune is refused, and the sweep's own log line is the only trace of it.
func TestAFailedCounterPruneIsLoggedBySweep(t *testing.T) {
	in := newInstance(t)
	ctx := context.Background()

	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	if _, err := in.db.Exec(ctx, `DROP TABLE usage_counters`); err != nil {
		t.Fatal(err)
	}
	in.server.sweep(ctx)

	out := logged.String()
	if !strings.Contains(out, "could not prune usage counters") {
		t.Fatalf("the sweep did not log the failed prune: %q", out)
	}
	if !strings.Contains(out, "usage_counters") {
		t.Fatalf("the logged prune failure does not name the cause: %q", out)
	}
}
