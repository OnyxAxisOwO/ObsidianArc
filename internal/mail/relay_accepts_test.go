package mail

import (
	"context"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"testing"
)

// serveRelay answers one plain SMTP session. The relay takes the message at
// the end of DATA with 250 and answers QUIT with quitReply, which is how a
// relay that accepted a message can still fail the session's close.
func serveRelay(listener net.Listener, quitReply string) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	tc := textproto.NewConn(conn)
	defer tc.Close()
	if err := tc.PrintfLine("220 relay.test ready"); err != nil {
		return
	}
	for {
		line, err := tc.ReadLine()
		if err != nil {
			return
		}
		verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch verb {
		case "EHLO", "HELO":
			err = tc.PrintfLine("250 relay.test")
		case "MAIL", "RCPT":
			err = tc.PrintfLine("250 OK")
		case "DATA":
			if err = tc.PrintfLine("354 go ahead"); err == nil {
				if _, err = tc.ReadDotLines(); err == nil {
					err = tc.PrintfLine("250 accepted")
				}
			}
		case "QUIT":
			_ = tc.PrintfLine("%s", quitReply)
			return
		default:
			err = tc.PrintfLine("502 not implemented")
		}
		if err != nil {
			return
		}
	}
}

// The relay has the message once it answers the end of DATA with 250. A QUIT
// that fails after that used to come back as a failed send, and a caller
// that retried on error delivered a second copy of a one-time code.
func TestAMessageAcceptedAtDataIsNotReportedFailedByQuit(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go serveRelay(listener, "500 no QUIT for you")

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	client, err := smtp.NewClient(conn, "relay.test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	err = deliver(context.Background(), client, "noreply@example.com", "reader@example.com",
		Message{Subject: "Your code", Body: "123456"})
	if err != nil {
		t.Fatalf("a message the relay accepted came back as failed: %v", err)
	}
}
