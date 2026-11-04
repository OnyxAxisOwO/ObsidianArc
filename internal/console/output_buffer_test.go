package console

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// chunkSink counts the writes that reach a transport, and keeps what they carried.
// On a browser each write is an SSE frame and a flush; on SSH it is a channel
// packet and a stall timer.
type chunkSink struct {
	mu     sync.Mutex
	writes int
	text   strings.Builder
}

func (s *chunkSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	s.text.Write(p)
	return len(p), nil
}

func (s *chunkSink) snapshot() (writes int, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes, s.text.String()
}

// A table is written a cell at a time, and the line's output must reach the
// transport in a few chunks, not one write per cell.
func TestAOneLineReachesItsTransportInFewWrites(t *testing.T) {
	var body strings.Builder
	body.WriteString(`{"users":[`)
	for i := 0; i < 300; i++ {
		if i > 0 {
			body.WriteString(",")
		}
		fmt.Fprintf(&body, `{"id":%q,"username":"user%03d","role":"user","status":"active"}`, id.New(), i)
	}
	body.WriteString(`],"total":300}`)
	rig := newStubRig(func(string, string) (int, string) { return 200, body.String() })

	var sink chunkSink
	session := &Session{Actor: hardeningAdmin(), Transport: "web", Lang: "en", Width: 100}
	result := rig.console.Execute(context.Background(), session, &sink, "user list")
	if !result.OK {
		t.Fatalf("user list failed: %+v", result)
	}
	writes, text := sink.snapshot()
	if writes > 10 {
		t.Errorf("the listing reached the transport in %d writes; want a few buffered chunks", writes)
	}
	if !strings.Contains(text, "user000") {
		t.Errorf("the listing is missing its first row:\n%s", text)
	}
}

// A watch shows each run's output as that run ends. The output of one tick must
// already be with the transport when the next tick's command is dispatched, or a
// long watch would show nothing until it stopped.
func TestAWatchShowsEachRunBeforeTheNextOne(t *testing.T) {
	var sink chunkSink
	var (
		mu        sync.Mutex
		runs      int
		seenByTwo string
	)
	c := New(Options{Dispatch: func(_ context.Context, _ user.User, _, _ string, _ any) (Response, error) {
		mu.Lock()
		runs++
		if runs == 2 {
			_, seenByTwo = sink.snapshot()
		}
		mu.Unlock()
		return Response{Status: 200, Body: []byte(`{"users":[{"id":"u1","username":"alice","role":"user","status":"active"}],"total":1}`)}, nil
	}})

	session := &Session{Actor: hardeningAdmin(), Transport: "web", Lang: "en", Width: 100}
	result := c.Execute(context.Background(), session, &sink, "watch --interval 1s --count 2 -- user list")
	if !result.OK {
		t.Fatalf("the watch failed: %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if runs != 2 {
		t.Fatalf("the watch ran %d times, want 2", runs)
	}
	if !strings.Contains(seenByTwo, "alice") {
		t.Errorf("the second run began before the first run's output reached the transport:\n%q", seenByTwo)
	}
}
