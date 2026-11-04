package console

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A watch repeats the command it was started with, and that command can change
// while the watch runs: a plugin may be installed, switched or removed. A run
// that finds the command now writes stops the watch when no --count was given,
// as a watch started on a write would have been refused.
func TestAWatchStopsWhenItsCommandTurnsIntoAWrite(t *testing.T) {
	var (
		mu    sync.Mutex
		calls []string
	)
	var c *Console
	c = New(Options{Dispatch: func(_ context.Context, _ user.User, method, path string, _ any) (Response, error) {
		mu.Lock()
		calls = append(calls, method+" "+path)
		first := len(calls) == 1
		mu.Unlock()
		if first {
			// The plugin changes its command between the first run and the next.
			c.RemoveCommands("probe")
			if err := c.AddCommands("probe", []Command{probeCommand(http.MethodPost)}); err != nil {
				t.Errorf("AddCommands: %v", err)
			}
		}
		return Response{Status: http.StatusOK, Body: []byte(`{"ok":true}`)}, nil
	}})
	if err := c.AddCommands("probe", []Command{probeCommand(http.MethodGet)}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	session := &Session{Actor: hardeningAdmin(), Transport: "web", Lang: "en", Width: 100}
	result := c.Execute(context.Background(), session, &out, "watch --interval 1s --for 1m -- probe ping")

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Errorf("the watch dispatched %v; want the first run only, then a stop before the write", calls)
	}
	if result.OK {
		t.Errorf("the watch reported success after its command became a write:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "watch stopped") {
		t.Errorf("the stop was not explained:\n%s", out.String())
	}
}

// probeCommand is a command a plugin would register, reading with GET or writing
// with POST at the same path.
func probeCommand(method string) Command {
	return Command{
		Name:       "probe ping",
		Group:      "session",
		Summary:    Text{EN: "Probe", ZH: "探测"},
		Usage:      "probe ping",
		Permission: Anyone,
		Endpoints:  []string{method + " /api/admin/probe"},
		Run: func(_ context.Context, rt *Runtime) error {
			_, _, err := rt.Call(method, "/api/admin/probe", nil)
			return err
		},
	}
}
