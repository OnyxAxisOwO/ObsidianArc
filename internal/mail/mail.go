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
	netmail "net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotConfigured = errors.New("mail: no SMTP host is configured")
	ErrTLSRequired   = errors.New("mail: SMTP server does not offer STARTTLS")
)

// Config is the effective SMTP setup, loaded from the database override or
// the legacy environment values when no override exists.
type Config struct {
	Host     string
	Port     int
	Username string
	// Password is sealed before persistence and omitted from every admin
	// response; the general settings table is served to browsers.
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
	return strings.TrimSpace(c.Host) != "" && c.Port >= 1 && c.Port <= 65535 && c.sender() != ""
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

// ValidateConfig rejects values that would become headers, dial targets, or
// externally visible links before they reach the live sender.
func ValidateConfig(c Config) error {
	host := strings.TrimSpace(c.Host)
	if strings.ContainsAny(host, "\r\n \t") || strings.Contains(host, "/") || strings.Contains(host, ":") {
		return errors.New("mail: host must be a hostname without a port")
	}
	if c.Port != 0 && (c.Port < 1 || c.Port > 65535) {
		return errors.New("mail: port must be between 1 and 65535")
	}
	if host != "" && c.Port == 0 {
		return errors.New("mail: a port is required when an SMTP host is configured")
	}
	from := c.sender()
	if host != "" && from == "" {
		return errors.New("mail: sender email is required when an SMTP host is configured")
	}
	if strings.ContainsAny(from, "\r\n") {
		return errors.New("mail: sender contains a line break")
	}
	if from != "" {
		address, err := netmail.ParseAddress(from)
		if err != nil || address.Address != strings.TrimSpace(from) || !strings.Contains(address.Address, "@") {
			return errors.New("mail: sender must be an email address")
		}
	}
	if c.PublicURL != "" && !validPublicURL(c.PublicURL) {
		return errors.New("mail: public URL must be an HTTPS origin")
	}
	return nil
}

func validPublicURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil &&
		(u.Path == "" || u.Path == "/") && u.RawPath == "" && u.RawQuery == "" && !u.ForceQuery &&
		u.Fragment == "" && u.RawFragment == ""
}

type Sender struct {
	mu  sync.RWMutex
	cfg Config
}

func New(cfg Config) *Sender { return &Sender{cfg: cfg} }

func (s *Sender) Configured() bool { return s.config().Configured() }

// VerificationReady includes the stable public origin needed to construct a
// link; merely having an SMTP relay is not enough to make verification usable.
func (s *Sender) VerificationReady() bool {
	cfg := s.config()
	return cfg.Configured() && validPublicURL(cfg.PublicURL)
}

// Update replaces one complete configuration atomically so a send observes
// either the old credentials or the new ones, never a mixture of both.
func (s *Sender) Update(cfg Config) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

func (s *Sender) config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// PublicURL is the base links are built from, without a trailing slash.
func (s *Sender) PublicURL() string {
	return strings.TrimRight(strings.TrimSpace(s.config().PublicURL), "/")
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
	// Resends and administrator test sends use request contexts without a
	// deadline. A relay that accepts TCP but stops answering must not hold
	// their HTTP handlers indefinitely; earlier caller deadlines still win.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cfg := s.config()
	if !cfg.Configured() {
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

	address := net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("mail: dial %s: %w", address, err)
	}
	// Closed here as well as through Quit: Quit does not run on every error
	// path, and a leaked connection per failed send is a slow leak.
	defer func() { _ = conn.Close() }()

	// Keep the raw connection in the cancellation callback. The TLS wrapper
	// below replaces conn while cancellation can run on another goroutine.
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()

	if cfg.ImplicitTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: cfg.Host})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("mail: tls handshake: %w", err)
		}
		conn = tlsConn
	}

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("mail: smtp: %w", err)
	}
	defer func() { _ = client.Close() }()

	if !cfg.ImplicitTLS {
		// Verification links and SMTP credentials are secrets in transit.
		// Silently continuing when a relay omits STARTTLS turns a downgrade or
		// a configuration mistake into plaintext mail. Port 465 is protected
		// from the first byte above; every other connection must upgrade.
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return ErrTLSRequired
		}
		if err := client.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
			return fmt.Errorf("mail: starttls: %w", err)
		}
	}

	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("mail: auth: %w", err)
		}
	}

	sender := cfg.sender()
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
	// The body travels as written, and the terminating CRLF is what leaves
	// the writer at the start of a line for its own ".\r\n" below.
	//
	// Neither dot-stuffing nor newline conversion happens here because
	// Client.Data returns a textproto.DotWriter, which already does both.
	// Doing the dot here as well sent ".." for a body line that began with
	// ".", and the recipient's unstuffing takes back only one — so the reader
	// got a dot nobody typed. The comment that used to sit here described the
	// reader's job, not this one's.
	out.WriteString(message.Body)
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
