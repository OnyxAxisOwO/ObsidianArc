package consolessh

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

// A server built without a Logf, which is how production builds it, still puts
// its operational warnings in the process log. The default used to discard
// them, so an accept loop retrying or connections turned away at a cap left no
// trace anywhere.
func TestUnsetLogfWritesToTheProcessLog(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	srv, err := New(Config{
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(nil),
		Reauthorize:  noReauthorize,
		HostKeyPath:  filepath.Join(t.TempDir(), "ssh_host_ed25519_key"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv.cfg.Logf(context.Background(), "consolessh: too many connections for this account", "remote", "192.0.2.1:22")

	out := buf.String()
	if !strings.Contains(out, "too many connections for this account") || !strings.Contains(out, "level=WARN") {
		t.Fatalf("process log = %q, want the warning in it", out)
	}
}
