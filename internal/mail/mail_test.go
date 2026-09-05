package mail

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// A relay that strips STARTTLS is the active-attacker version of a
// misconfigured relay. Neither the message nor the envelope may be sent.
func TestSendRefusesSMTPWithoutSTARTTLS(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	command := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		_, _ = fmt.Fprint(conn, "220 local.test ESMTP\r\n")
		if _, readErr := reader.ReadString('\n'); readErr != nil { // EHLO
			return
		}
		_, _ = fmt.Fprint(conn, "250-local.test\r\n250 SIZE 1048576\r\n")
		if line, readErr := reader.ReadString('\n'); readErr == nil {
			command <- strings.TrimSpace(line)
		}
	}()

	address := listener.Addr().(*net.TCPAddr)
	sender := New(Config{
		Host: "127.0.0.1", Port: address.Port, From: "sender@example.com",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = sender.Send(ctx, Message{
		To: "reader@example.com", Subject: "Verify", Body: "secret link",
	})
	if !errors.Is(err, ErrTLSRequired) {
		t.Fatalf("Send error = %v, want ErrTLSRequired", err)
	}

	select {
	case line := <-command:
		t.Fatalf("SMTP command %q was sent after an insecure EHLO", line)
	case <-done:
	case <-ctx.Done():
		t.Fatal("SMTP test server did not observe the connection closing")
	}
}
