package consolessh

import (
	"io"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// stalledChannel is a client that has stopped taking bytes. A write to it does
// not return until the connection under it is closed, which is what happens
// when a write waits for a window adjustment that never comes: there is no
// deadline on it and no context to cancel it.
type stalledChannel struct {
	ssh.Channel
	gone <-chan struct{}
}

func (c *stalledChannel) Write(p []byte) (int, error) {
	<-c.gone
	return 0, io.ErrClosedPipe
}

func (c *stalledChannel) Read(p []byte) (int, error) {
	<-c.gone
	return 0, io.EOF
}

// closableConn closes the signal the stalled channel waits on, once.
type closableConn struct {
	ssh.Conn
	gone chan struct{}
	once sync.Once
}

func (c *closableConn) Close() error {
	c.once.Do(func() { close(c.gone) })
	return nil
}

// A console session whose client stops reading must end. The banner is the
// first thing written, before any idle timer exists, so a stall there held the
// session's slots until the process ended.
func TestAWriteThatStallsEndsTheSession(t *testing.T) {
	previous := writeStallTimeout
	writeStallTimeout = 50 * time.Millisecond
	t.Cleanup(func() { writeStallTimeout = previous })

	gone := make(chan struct{})
	srv := &Server{cfg: Config{Console: newTestConsole(t), IdleTimeout: time.Hour}}
	sess := newSSHSession(srv, &closableConn{gone: gone}, &stalledChannel{gone: gone},
		adminUser("admin"), "127.0.0.1", "")

	done := make(chan struct{})
	go func() {
		sess.runInteractive()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a session whose client stopped reading was never ended")
	}
}

// stalledCloseChannel is a client that has stopped taking bytes by the time the
// session closes: the close message cannot be written, so CloseWrite and Close
// wait on the connection the way a window that never reopens would.
type stalledCloseChannel struct {
	stalledChannel
}

func (c *stalledCloseChannel) CloseWrite() error {
	<-c.gone
	return io.ErrClosedPipe
}

func (c *stalledCloseChannel) Close() error {
	<-c.gone
	return io.ErrClosedPipe
}

// A command that finished while its client had stopped reading must not hold
// its session. closeChannel runs under closeOnce, so a stall timer that went
// through close would wait behind the write it was armed to end.
func TestAStalledChannelCloseEndsTheSession(t *testing.T) {
	previous := writeStallTimeout
	writeStallTimeout = 50 * time.Millisecond
	t.Cleanup(func() { writeStallTimeout = previous })

	gone := make(chan struct{})
	srv := &Server{cfg: Config{Console: newTestConsole(t), IdleTimeout: time.Hour}}
	channel := &stalledCloseChannel{stalledChannel: stalledChannel{gone: gone}}
	sess := newSSHSession(srv, &closableConn{gone: gone}, channel, adminUser("admin"), "127.0.0.1", "")

	done := make(chan struct{})
	go func() {
		sess.closeChannel()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a close stuck in a write to a client that stopped reading never returned")
	}
}
