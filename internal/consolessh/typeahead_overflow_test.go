package consolessh

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Keys typed while a command runs wait in a queue bounded like a line. Past the
// bound they are discarded, and the discarded keys include the Enter that would
// have ended the line, so what the queue holds is a truncated prefix. That prefix
// must not run when the next Enter comes: the line is refused as an overlong one
// is, and the line typed after it runs.
func TestTypeAheadOverflowRefusesTheTruncatedLine(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce atomic.Bool
	var dispatched atomic.Int32
	engine := console.New(console.Options{
		Dispatch: func(ctx context.Context, _ user.User, _, _ string, _ any) (console.Response, error) {
			dispatched.Add(1)
			if enteredOnce.CompareAndSwap(false, true) {
				close(entered)
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return console.Response{Status: 200, Body: []byte(`{"users":[],"total":0}`)}, nil
		},
		Version:  "test",
		SiteName: func() string { return "Test Arc" },
	})
	accounts := map[string]testAccount{"admin": {password: "s3cret-pass", account: adminUser("admin")}}
	srv := startTestServer(t, Config{
		Console:      engine,
		Authenticate: fakeAuthenticate(accounts),
	})
	client := dialInsecure(t, srv, "admin", "s3cret-pass")

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	if err := session.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatalf("RequestPty: %v", err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	out := &syncBuffer{}
	go func() { _, _ = io.Copy(out, stdout) }()
	if err := session.Shell(); err != nil {
		t.Fatalf("Shell: %v", err)
	}

	if _, err := stdin.Write([]byte("user list\r")); err != nil {
		t.Fatalf("write command: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the command never reached the admin API")
	}

	// The queue keeps the first maxLineRunes keys. "user list" and the spaces
	// after it fill it; the spaces beyond the bound and the Enter are discarded.
	overflow := "user list" + strings.Repeat(" ", maxLineRunes) + "\r"
	if _, err := stdin.Write([]byte(overflow)); err != nil {
		t.Fatalf("type ahead: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for strings.Count(out.String(), "user list") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the kept prefix never reached the editor; output:\n%q", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := stdin.Write([]byte("\r")); err != nil {
		t.Fatalf("write Enter: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for !strings.Contains(out.String(), "[line not run") {
		if time.Now().After(deadline) {
			t.Fatalf("the truncated line was not refused; output:\n%q", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := dispatched.Load(); n != 1 {
		t.Fatalf("the truncated line ran: %d dispatches, want only the first command", n)
	}

	if _, err := stdin.Write([]byte("user list\r")); err != nil {
		t.Fatalf("write the next command: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for dispatched.Load() != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the line after the refusal did not run; dispatches = %d", dispatched.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := stdin.Write([]byte{0x04}); err != nil {
		t.Fatalf("write Ctrl-D: %v", err)
	}
	runWithin(t, 3*time.Second, "the session after Ctrl-D", func() {
		_ = session.Wait()
	})
}
