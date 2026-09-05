// Package mail sends the handful of messages this server has to send.
//
// net/smtp and nothing else. A mail library would be a dependency and a
// configuration surface for what is, here, one plain-text message to one
// recipient; when that stops being true it will be obvious.
//
// Sending is optional. An instance with no SMTP host configured is not
// broken — it simply cannot offer the features that need mail, and the admin
// screen says so rather than letting an operator switch on a verification
// requirement that would lock every new account out.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

var (
	ErrNotConfigured = errors.New("mail: no SMTP host is configured")
	ErrTLSRequired   = errors.New("mail: SMTP server does not offer STARTTLS")
)

// Config is what an operator supplies through the environment. Credentials do
// not belong in the settings table: it is served to the admin screen, and a
// password that reaches a browser is a password that has leaked.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	// The envelope and header sender. Falls back to the username when it
	// looks like an address, because that is what most providers require
	// anyway and it removes a step from the common setup.
	From string
	// Implicit TLS from the first byte, as on port 465. Otherwise the
	// connection starts plain and is upgraded with STARTTLS, which is what
	// 587 wants.
	ImplicitTLS bool
	// The address links in outgoing mail point at. Without it a verification
	// link cannot be built, because the server has no reliable idea what
	// hostname a user reached it by.
	PublicURL string
}

func (c Config) Configured() bool {
	return strings.TrimSpace(c.Host) != "" && c.sender() != ""
}

func (c Config) sender() string {
	if from := strings.TrimSpace(c.From); from != "" {
		return from
	}
	if strings.Contains(c.Username, "@") {
		return strings.TrimSpace(c.Username)
	}
	return ""
}

type Sender struct{ cfg Config }

func New(cfg Config) *Sender { return &Sender{cfg: cfg} }

func (s *Sender) Configured() bool { return s.cfg.Configured() }

// PublicURL is the base links are built from, without a trailing slash.
func (s *Sender) PublicURL() string {
	return strings.TrimRight(strings.TrimSpace(s.cfg.PublicURL), "/")
}

type Message struct {
	To      string
	Subject string
	Body    string
}

// Send delivers one message.
//
// The context bounds the whole exchange rather than being handed to net/smtp,
// which takes none: the dial is given the deadline and the connection is
// closed if the context ends, which is what stops a hung SMTP server from
// holding a request open indefinitely.
func (s *Sender) Send(ctx context.Context, message Message) error {
	if !s.cfg.Configured() {
		return ErrNotConfigured
	}
	to := strings.TrimSpace(message.To)
	if to == "" || !strings.Contains(to, "@") {
		return fmt.Errorf("mail: %q is not an address", message.To)
	}
	// A header cannot contain a newline. Refusing beats silently sending a
	// message with an injected Bcc.
	if strings.ContainsAny(to, "\r\n") || strings.ContainsAny(message.Subject, "\r\n") {
		return errors.New("mail: header contains a line break")
	}

	address := net.JoinHostPort(s.cfg.Host, fmt.Sprint(s.cfg.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("mail: dial %s: %w", address, err)
	}
	// Closed here as well as through Quit: Quit does not run on every error
	// path, and a leaked connection per failed send is a slow leak.
	defer func() { _ = conn.Close() }()

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if s.cfg.ImplicitTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: s.cfg.Host})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("mail: tls handshake: %w", err)
		}
		conn = tlsConn
	}

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("mail: smtp: %w", err)
	}
	defer func() { _ = client.Close() }()

	if !s.cfg.ImplicitTLS {
		// Verification links and SMTP credentials are secrets in transit.
		// Silently continuing when a relay omits STARTTLS turns a downgrade or
		// a configuration mistake into plaintext mail. Port 465 is protected
		// from the first byte above; every other connection must upgrade.
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return ErrTLSRequired
		}
		if err := client.StartTLS(&tls.Config{ServerName: s.cfg.Host}); err != nil {
			return fmt.Errorf("mail: starttls: %w", err)
		}
	}

	if s.cfg.Username != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("mail: auth: %w", err)
		}
	}

	sender := s.cfg.sender()
	if err := client.Mail(sender); err != nil {
		return fmt.Errorf("mail: from: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("mail: to: %w", err)
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	if _, err := writer.Write([]byte(compose(sender, to, message))); err != nil {
		return fmt.Errorf("mail: write: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("mail: close: %w", err)
	}
	return client.Quit()
}

// compose builds a plain-text message. UTF-8 subjects are encoded per RFC
// 2047, because an unencoded one arrives as mojibake in most clients.
func compose(from, to string, message Message) string {
	var out strings.Builder
	out.WriteString("From: " + from + "\r\n")
	out.WriteString("To: " + to + "\r\n")
	out.WriteString("Subject: " + encodeHeader(message.Subject) + "\r\n")
	out.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	out.WriteString("MIME-Version: 1.0\r\n")
	out.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	out.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	out.WriteString("\r\n")
	// Bare newlines are illegal in SMTP data, and a leading dot on a line
	// ends the message early.
	body := strings.ReplaceAll(strings.ReplaceAll(message.Body, "\r\n", "\n"), "\n", "\r\n")
	out.WriteString(strings.ReplaceAll(body, "\r\n.", "\r\n.."))
	out.WriteString("\r\n")
	return out.String()
}

// encodeHeader wraps a header value in RFC 2047 base64 when it is not plain
// ASCII. Everything this server sends may have a Chinese subject.
func encodeHeader(value string) string {
	ascii := true
	for _, r := range value {
		if r > 127 {
			ascii = false
			break
		}
	}
	if ascii {
		return value
	}
	return mime.QEncoding.Encode("UTF-8", value)
}
