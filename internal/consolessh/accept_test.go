package consolessh

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"
)

// scriptedListener is a net.Listener whose Accept results the test decides.
// Once the script runs out, Accept reports that through exhausted and then
// blocks until Close, the way a real listener does until Shutdown closes it.
// The loop can therefore only leave by the path under test, never by running
// off the end of the script.
type scriptedListener struct {
	script []acceptStep
	next   int // touched only by the accept loop

	exhausted     chan struct{}
	exhaustedOnce sync.Once
	closed        chan struct{}
	closeOnce     sync.Once
}

type acceptStep struct {
	conn net.Conn
	err  error
}

func newScriptedListener(steps ...acceptStep) *scriptedListener {
	return &scriptedListener{
		script:    steps,
		exhausted: make(chan struct{}),
		closed:    make(chan struct{}),
	}
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	if l.next < len(l.script) {
		step := l.script[l.next]
		l.next++
		return step.conn, step.err
	}
	l.exhaustedOnce.Do(func() { close(l.exhausted) })
	<-l.closed
	return nil, net.ErrClosed
}

func (l *scriptedListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *scriptedListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
}

// temporaryError is an accept failure the platform calls transient without a
// more specific errno, such as a connection reset before it was accepted.
type temporaryError struct{}

func (temporaryError) Error() string   { return "accept: connection aborted" }
func (temporaryError) Timeout() bool   { return false }
func (temporaryError) Temporary() bool { return true }

// acceptLog keeps the backoff each retry logged, so the tests check the
// schedule the loop actually used instead of inferring it from wall-clock
// time, which is too noisy to tell 5ms from 10ms reliably.
type acceptLog struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (a *acceptLog) logf(_ context.Context, _ string, args ...any) {
	for i := 0; i+1 < len(args); i += 2 {
		if args[i] != "delay" {
			continue
		}
		if d, ok := args[i+1].(time.Duration); ok {
			a.mu.Lock()
			a.delays = append(a.delays, d)
			a.mu.Unlock()
		}
	}
}

func (a *acceptLog) recordedDelays() []time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]time.Duration(nil), a.delays...)
}

// newAcceptTestServer builds a Server the way production does, so a connection
// the loop hands on goes through the real handshake path.
func newAcceptTestServer(t *testing.T) (*Server, *acceptLog) {
	t.Helper()
	logged := &acceptLog{}
	srv, err := New(Config{
		Console:      newTestConsole(t),
		Authenticate: fakeAuthenticate(nil),
		HostKeyPath:  filepath.Join(t.TempDir(), "ssh_host_ed25519_key"),
		Logf:         logged.logf,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, logged
}

// deadConn is a connection the peer has already hung up on, so its handshake
// fails at once and the accept loop is left free to carry on.
func deadConn() net.Conn {
	server, peer := net.Pipe()
	_ = peer.Close()
	return server
}

// A transient Accept failure is waited out and Accept is tried again, and the
// loop only ends when Shutdown closes the listener. The recorded delays show the
// schedule starting at 5ms, doubling, and starting over once a connection gets
// through.
func TestAcceptKeepsGoingThroughTransientErrors(t *testing.T) {
	srv, logged := newAcceptTestServer(t)
	emfile := &net.OpError{Op: "accept", Net: "tcp", Err: os.NewSyscallError("accept", syscall.EMFILE)}
	listener := newScriptedListener(
		acceptStep{err: emfile},
		acceptStep{err: syscall.ENFILE},
		acceptStep{err: temporaryError{}},
		acceptStep{conn: deadConn()},
		acceptStep{err: emfile},
	)
	t.Cleanup(func() { _ = listener.Close() })

	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.serve(listener) }()

	select {
	case err := <-serveDone:
		t.Fatalf("serve returned %v while the listener was still open; a transient accept error ended the loop", err)
	case <-listener.exhausted:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not get through the scripted accepts")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("serve returned %v after Shutdown, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not return after Shutdown")
	}

	want := []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond, 5 * time.Millisecond}
	if got := logged.recordedDelays(); !slices.Equal(got, want) {
		t.Fatalf("backoff delays = %v, want %v", got, want)
	}
}

// An Accept failure that is not transient is not a reason to wait: the listener
// is broken, and the loop returns that error the way it always did.
func TestAcceptErrorThatIsNotTransientEndsTheLoop(t *testing.T) {
	srv, _ := newAcceptTestServer(t)
	broken := errors.New("listener broke")
	listener := newScriptedListener(acceptStep{err: broken})
	t.Cleanup(func() { _ = listener.Close() })

	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.serve(listener) }()

	select {
	case err := <-serveDone:
		if !errors.Is(err, broken) {
			t.Fatalf("serve returned %v, want the accept error itself", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve kept retrying an accept error that is not transient")
	}
}

func TestNextAcceptDelayDoublesFromFiveMillisecondsAndCapsAtOneSecond(t *testing.T) {
	want := []time.Duration{
		5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond,
		40 * time.Millisecond, 80 * time.Millisecond, 160 * time.Millisecond,
		320 * time.Millisecond, 640 * time.Millisecond,
		time.Second, time.Second,
	}
	var delay time.Duration
	for i, w := range want {
		delay = nextAcceptDelay(delay)
		if delay != w {
			t.Fatalf("retry %d: delay = %v, want %v", i+1, delay, w)
		}
	}
}
