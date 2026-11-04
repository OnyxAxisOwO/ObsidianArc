package settings

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// When the re-read after a committed write fails, the cache falls back as if
// the rows were absent. Nothing returns that failure to anyone, so the log is
// the only place it can be seen.
func TestAFailedRereadAfterACommittedWriteIsLogged(t *testing.T) {
	ctx := t.Context()
	db, svc := openMigratedSettings(t)

	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	if _, err := db.Exec(ctx, `DROP TABLE settings`); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	svc.Forget([]string{SiteName})
	if out := logged.String(); !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "could not re-read") {
		t.Fatalf("a failed re-read after Forget was not logged:\n%s", out)
	}

	svc.Remember(map[string]string{SiteName: "installed"})
	if got := strings.Count(logged.String(), "could not re-read"); got != 2 {
		t.Fatalf("Remember's failed re-read was not logged; %d log lines in all", got)
	}
}
