// Package consolessh is the SSH transport for the administrative console.
//
// It is a second door onto the same console engine the browser terminal
// talks to (internal/console), not a shell. There is no path from this
// package to os/exec anywhere: a "shell" request drives the console's own
// line-oriented Execute loop, and an "exec" request runs the one line it
// was given through Execute exactly once — never a host command. Every
// permission decision is the console engine's, re-checked against the same
// /api/admin/* routes the web UI uses; this package only carries bytes and
// keeps a person authenticated as an administrator on the other end of
// them.
package consolessh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Config is what New needs to start the listener. The zero value is not
// valid: Console, Authenticate and Reauthorize have no safe default, so New refuses it
// rather than silently running a console nobody can reach or one that lets
// anybody in.
type Config struct {
	Addr        string // ":2222"
	HostKeyPath string // <data dir>/ssh_host_ed25519_key

	Console *console.Console

	// Authenticate verifies a password against the same store the web login
	// uses and returns the account, with the credential fingerprint (see
	// Credential) of the hash the password was checked against. The fingerprint
	// must come from that check's own read: a change made while the password is
	// being verified then leaves the connection with the old value, and its next
	// command ends it. Whether that account may have a console is Permitted's
	// question, asked by consolessh itself and folded into the same error a
	// wrong password gets, so the prompt cannot be used to find out which
	// accounts hold it.
	Authenticate func(ctx context.Context, username, password, ip string) (user.User, string, error)

	// Permitted answers whether an account may use the console at all: the
	// same rule the web terminal applies, so the two doors cannot disagree
	// about who is let in. Asked at the handshake and again before every
	// command. Nil admits administrators only, which is what this transport
	// did before the terminal was offered to every account.
	//
	// An error means the rule could not be evaluated, which is not the same as
	// a refusal. The handshake treats it as one, but a session already open ends
	// with the retry message rather than the revocation notice.
	Permitted func(ctx context.Context, account user.User) (bool, error)

	// Reauthorize re-reads the account behind a live session.
	//
	// A console connection outlives the moment it was authenticated, and the
	// grants it was opened with are not necessarily the grants its owner
	// still holds. The web transport never had this problem: auth.Attach
	// resolves the session cookie against the database on every request,
	// which is also how disabling an account stops it "now, not at its next
	// expiry". An SSH connection has no cookie and no per-request lookup, so
	// without this the actor is frozen at the handshake and a demoted,
	// narrowed, disabled or deleted administrator keeps everything they had
	// until they happen to disconnect.
	//
	// Required; New refuses nil. user.ErrNotFound or database.IsNotFound ends
	// the session as revoked. Any other error ends it with the retry message.
	// Called before every command.
	Reauthorize func(ctx context.Context, userID string) (user.User, error)

	// Credential fingerprints the account's password, and must change whenever
	// the password does. It must also agree with what Authenticate returns for
	// the same password, or every connection would end at its first command. A
	// connection keeps the value it was opened with, and its next command ends
	// once the account's value differs. A password change ends the sessions a
	// web sign-in holds, but a console connection has no cookie and no session
	// row for that to reach. Nil checks nothing.
	Credential func(ctx context.Context, userID string) (string, error)

	// SecondFactor checks the code from an authenticator app, for an account
	// that has two-step sign-in switched on. The password alone opens the
	// web sign-in only halfway for such an account, and this door must not
	// be the one that opens all the way on it.
	//
	// Asked after the password, as a keyboard-interactive prompt: the SSH
	// protocol's own "that was right, now this", which every OpenSSH client
	// understands. Nil refuses such accounts outright rather than admitting
	// them on a password.
	SecondFactor func(ctx context.Context, account user.User, code, ip string) error

	// ConnectionContext makes the context every command on one connection
	// runs under, once per connection. It is where the wiring keeps state
	// that belongs to a connection and must end with it — the visit to the
	// backoffice a code at the console's `2fa backoffice` opens — without
	// this package having to know what that state is. Nil is a bare
	// background context.
	ConnectionContext func(ctx context.Context) context.Context

	IdleTimeout time.Duration // default 30m
	MaxSessions int           // default 16, 0 = unlimited
	// MaxSessionsPerAccount bounds how many of those slots one account may
	// hold at once, over any number of connections. MaxSessions is shared by
	// everybody, so without this a single account could fill it and lock the
	// rest out. 0 means 4; negative means no per-account limit.
	MaxSessionsPerAccount int
	// ExecTimeout is the longest a one-shot `ssh host 'cmd'` may run. A
	// command that never ends (`watch`, a stuck upstream) would otherwise hold
	// its slot until somebody noticed. 0 means 15m.
	ExecTimeout time.Duration
	// MaxUnauthenticated bounds the connections that have not finished the
	// handshake yet, across all addresses; at most maxPreauthPerIP of them
	// may come from one. 0 means 64; negative means no limit.
	MaxUnauthenticated int

	// HandshakeTimeout bounds the unauthenticated part of a connection, from
	// accept to a completed SSH handshake, and each database callback the
	// handshake makes. 0 means 30s. Tests shorten it on the Config they start a
	// server with, so the server's goroutines never read a value that is being
	// written.
	HandshakeTimeout time.Duration
	// SessionStartTimeout bounds how long a signed-in connection may go without
	// a session, and how long an opened session may go without a shell or an
	// exec. 0 means 30s.
	SessionStartTimeout time.Duration
	// WriteStallTimeout bounds one write to the client. 0 means 30s.
	WriteStallTimeout time.Duration

	// Logf receives the listener's operational warnings: Accept retrying,
	// connections turned away at a cap. nil means the process log at warning
	// level, so leaving it unset does not leave them unread.
	Logf func(ctx context.Context, msg string, args ...any)
}

// errAuthFailed is returned for every password-callback failure: wrong
// password, unknown account, disabled account and non-administrator all
// collapse to this one error so none of them is distinguishable at the SSH
// prompt.
var errAuthFailed = errors.New("consolessh: incorrect username or password")

// Server is one SSH listener serving the console. The zero value is not
// usable; construct one with New.
type Server struct {
	cfg       Config
	signer    ssh.Signer
	sshConfig *ssh.ServerConfig

	mu       sync.Mutex
	listener net.Listener
	addr     string
	closed   bool
	sessions map[*sshSession]struct{}
	// Every accepted connection, not just the ones with a live channel.
	// Sessions come and go within one connection — a one-shot exec closes
	// its channel and leaves the connection open on purpose, so a client
	// can run a second command over it — which used to leave Shutdown with
	// nothing to close and its handleConn goroutine parked on `range chans`
	// until the deadline expired.
	conns map[net.Conn]struct{}

	sem chan struct{} // nil when MaxSessions == 0 (unlimited)
	wg  sync.WaitGroup

	// Sessions held per account, so one account cannot take every slot.
	perAccount map[string]int
	// Authenticated connections held per account, sessions or not.
	connsPerAccount map[string]int
	// Connections still in the handshake: the total, and per address.
	preauth   int
	preauthIP map[string]int
}

// maxPreauthPerIP is how many unauthenticated connections one address may
// hold. The total cap alone would let a single host fill it and keep every
// administrator out for as long as it kept reconnecting.
const maxPreauthPerIP = 8

// maxUsernameLen is the longest user name that is worth handing to the
// password check. Accounts are 3-32 characters; anything past this is not a
// name, and the check does work (a rate-limit key, a hash) per attempt.
const maxUsernameLen = 64

// New loads or creates the host key and prepares the server, but does not
// listen yet — that is ListenAndServe's job, so a bad HostKeyPath fails at
// wiring time, in the caller's boot context, rather than surfacing later as
// a failed goroutine nobody is watching.
func New(cfg Config) (*Server, error) {
	if cfg.Console == nil {
		return nil, errors.New("consolessh: Config.Console is required")
	}
	if cfg.Authenticate == nil {
		return nil, errors.New("consolessh: Config.Authenticate is required")
	}
	// Without it a session keeps the grants it had at the handshake for as long
	// as it lasts, and nothing reports that the re-check was dropped.
	if cfg.Reauthorize == nil {
		return nil, errors.New("consolessh: Config.Reauthorize is required")
	}
	if cfg.HostKeyPath == "" {
		return nil, errors.New("consolessh: Config.HostKeyPath is required")
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 30 * time.Minute
	}
	if cfg.MaxSessionsPerAccount == 0 {
		cfg.MaxSessionsPerAccount = 4
	}
	if cfg.ExecTimeout <= 0 {
		cfg.ExecTimeout = 15 * time.Minute
	}
	if cfg.MaxUnauthenticated == 0 {
		cfg.MaxUnauthenticated = 64
	}
	// The process log unless a caller routes these elsewhere. A silent
	// default left an accept loop retrying, or a flood of unauthenticated
	// connections being turned away, invisible in production, which wires no
	// Logf of its own.
	if cfg.Logf == nil {
		cfg.Logf = slog.WarnContext
	}

	signer, err := loadOrCreateHostKey(cfg.HostKeyPath)
	if err != nil {
		return nil, err
	}

	srv := &Server{
		cfg:      cfg,
		signer:   signer,
		sessions: make(map[*sshSession]struct{}),
	}
	if cfg.MaxSessions > 0 {
		srv.sem = make(chan struct{}, cfg.MaxSessions)
	}

	sshConfig := &ssh.ServerConfig{
		// Beyond this many failed passwords the connection is dropped; the
		// real brake on guessing is auth.Limiter, shared with the web login
		// through Authenticate, which is keyed by IP and identifier rather
		// than by connection.
		MaxAuthTries:     6,
		ServerVersion:    "SSH-2.0-ObsidianArc",
		PasswordCallback: srv.passwordCallback,
	}
	sshConfig.AddHostKey(signer)
	srv.sshConfig = sshConfig

	return srv, nil
}

// Fingerprint is the SHA256 fingerprint of the host key, exactly as an SSH
// client reports it — what `help ssh` and the startup log show so an
// administrator can verify the console before trusting it.
func (s *Server) Fingerprint() string { return fingerprint(s.signer) }

// Addr is the resolved listen address, useful when Config.Addr ends in
// ":0". Empty until ListenAndServe has bound the socket.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// ListenAndServe binds and accepts connections until Shutdown is called, at
// which point it returns nil rather than an error — mirroring
// http.Server.ListenAndServe's http.ErrServerClosed contract lets
// cmd/server/main.go treat this listener exactly like the HTTP one in its
// startup select.
func (s *Server) ListenAndServe() error {
	listener, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("consolessh: listen on %s: %w", s.cfg.Addr, err)
	}
	return s.serve(listener)
}

// serve runs the accept loop on an already-bound listener. It is split from
// ListenAndServe only so the tests can drive the loop with a scripted
// listener; nothing else calls it.
//
// A transient Accept failure does not end the loop. Under descriptor
// exhaustion Accept fails with EMFILE until a connection closes, and returning
// that error hands main.go a reason to exit, which takes the HTTP listener down
// with it. net/http's Serve waits out the same errors, and this does too.
func (s *Server) serve(listener net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = listener.Close()
		return nil
	}
	s.listener = listener
	s.addr = listener.Addr().String()
	s.mu.Unlock()

	var retryDelay time.Duration
	for {
		conn, err := listener.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			if !retryableAcceptError(err) {
				return err
			}
			retryDelay = nextAcceptDelay(retryDelay)
			s.cfg.Logf(context.Background(), "consolessh: accept failed, retrying", "error", err, "delay", retryDelay)
			time.Sleep(retryDelay)
			continue
		}
		// A successful accept ends a run of failures. Without the reset, the
		// next and unrelated failure would resume the old schedule, possibly at
		// the one-second cap, instead of starting again at 5ms.
		retryDelay = 0
		// Checked and counted under the same lock Shutdown takes to set closed.
		// Without it, an accept that lands just as Shutdown starts would call
		// Add while Shutdown is already in Wait, which WaitGroup forbids, and
		// its connection would be in no snapshot for Shutdown to close.
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			return nil
		}
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			s.handleConn(conn)
		}()
	}
}

// retryableAcceptError reports whether an Accept failure should be waited out
// rather than returned. Temporary() is documented as ill-defined, but it is the
// test net/http applies, so the two listeners agree on what is transient.
// EMFILE and ENFILE are named as well:
// they are what a process out of descriptors gets, which is the case this
// exists for, and naming them keeps the retry from depending on how a
// platform classifies them.
func retryableAcceptError(err error) bool {
	if errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Temporary()
}

// nextAcceptDelay is the wait before retrying a failed Accept: 5ms, doubling
// with each consecutive failure, and never more than a second. The short start
// suits a descriptor that frees up a moment later. The cap means a listener
// that stays broken is still retried often enough to notice when it recovers.
func nextAcceptDelay(prev time.Duration) time.Duration {
	if prev == 0 {
		return 5 * time.Millisecond
	}
	if next := 2 * prev; next < time.Second {
		return next
	}
	return time.Second
}

// Shutdown closes the listener and every live session, then waits for their
// goroutines to finish or ctx to expire — the same shape as http.Server's
// Shutdown, so main.go can hand both the same deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	listener := s.listener
	live := make([]*sshSession, 0, len(s.sessions))
	for sess := range s.sessions {
		live = append(live, sess)
	}
	conns := make([]net.Conn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	s.mu.Unlock()

	if listener != nil {
		_ = listener.Close()
	}
	for _, sess := range live {
		sess.close()
	}
	// After the sessions, so a command still writing its last line has had
	// its context cancelled before the socket under it goes.
	for _, conn := range conns {
		_ = conn.Close()
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// passwordCallback is the one way in: no "none" and no public key. Keyboard-
// interactive is offered only after a right password, and only for the code
// of an account with two-step sign-in. A successful verification's account
// travels to the connection handler through ssh.Permissions.Extensions,
// JSON-encoded, because that is the only channel the ssh package offers
// between the callback and the rest of the handshake.
func (s *Server) passwordCallback(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
	// Before anything is looked up: the name is the client's to make as long
	// as the packet allows, and Authenticate keys its attempt budget on it.
	if len(conn.User()) > maxUsernameLen {
		return nil, errAuthFailed
	}
	ip := hostOnly(conn.RemoteAddr())
	// The credential is the one Config.Authenticate read while it checked the
	// password, and it is not read again here. A second read would sit behind the
	// whole verification, Argon2 included, so a password changed anywhere in that
	// span would be recorded as the one this connection signed in with, and the
	// old password would keep a console the change should have ended. A sign-in
	// that rehashes the stored hash gets the fingerprint of the hash it checked,
	// so its own connection ends at its first command; that happens once per
	// account per parameter change and is accepted.
	ctx, cancel := s.handshakeContext()
	defer cancel()
	account, credential, err := s.cfg.Authenticate(ctx, conn.User(), string(password), ip)
	if err != nil {
		return nil, errAuthFailed
	}
	// Checked here, not inside Authenticate: collapsing it into the same
	// error a wrong password gets is what stops this prompt from being
	// usable to enumerate which accounts have a console.
	permitted, err := s.permitted(ctx, account)
	if err != nil {
		// Still the refusal a wrong password gets; the log is where a database
		// outage at sign-in shows up.
		slog.Error("console could not check its permission at sign-in", "error", err)
		return nil, errAuthFailed
	}
	if !permitted {
		return nil, errAuthFailed
	}

	if account.TwoFactorEnabled() {
		if s.cfg.SecondFactor == nil {
			return nil, errAuthFailed
		}
		// Partial success: the password is spent, and the only way on is
		// the code. The account travels in the closure rather than being
		// looked up again, so the prompt cannot be answered for somebody
		// other than whoever typed the password.
		return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{
			KeyboardInteractiveCallback: func(_ ssh.ConnMetadata, challenge ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
				answers, err := challenge("", "", []string{"Verification code: "}, []bool{false})
				if err != nil || len(answers) != 1 {
					return nil, errAuthFailed
				}
				// The prompt is answered later in the same handshake, so the password
				// step's context has been cancelled by then; the code gets its own.
				codeCtx, codeCancel := s.handshakeContext()
				defer codeCancel()
				if err := s.cfg.SecondFactor(codeCtx, account, answers[0], ip); err != nil {
					return nil, errAuthFailed
				}
				return actorPermissions(account, credential)
			},
		}}
	}
	return actorPermissions(account, credential)
}

func actorPermissions(account user.User, credential string) (*ssh.Permissions, error) {
	encoded, err := json.Marshal(account)
	if err != nil {
		return nil, errAuthFailed
	}
	return &ssh.Permissions{Extensions: map[string]string{
		"actor":      string(encoded),
		"credential": credential,
	}}, nil
}

func credentialFromPermissions(perm *ssh.Permissions) string {
	if perm == nil {
		return ""
	}
	return perm.Extensions["credential"]
}

func actorFromPermissions(perm *ssh.Permissions) (user.User, bool) {
	if perm == nil {
		return user.User{}, false
	}
	raw, ok := perm.Extensions["actor"]
	if !ok {
		return user.User{}, false
	}
	var account user.User
	if err := json.Unmarshal([]byte(raw), &account); err != nil {
		return user.User{}, false
	}
	return account, true
}

func (s *Server) handleConn(conn net.Conn) {
	defer recoverHandshake(conn)
	if !s.addConn(conn) {
		// Shutdown has already taken its snapshot, so nothing else will close it.
		_ = conn.Close()
		return
	}
	defer s.removeConn(conn)

	// A peer that connects and then says nothing would otherwise hold a
	// goroutine and a file descriptor for as long as it liked, before
	// authenticating anything — the cheapest denial of service there is
	// against a listener on the public internet. Cleared once the handshake
	// is done, because a console session is legitimately idle for long
	// stretches and the idle timeout is what governs it from then on.
	_ = conn.SetDeadline(time.Now().Add(s.handshakeTimeout()))

	ip := hostOnly(conn.RemoteAddr())
	// Counted from accept to the end of the handshake, whichever way it ends.
	slot, admitted := s.acquirePreauth(conn.RemoteAddr())
	if !admitted {
		// NewServerConn closes the socket when a handshake fails; this one
		// never starts, so nothing else will.
		_ = conn.Close()
		s.cfg.Logf(context.Background(), "consolessh: too many unauthenticated connections", "remote", conn.RemoteAddr().String())
		return
	}
	// Released by defer rather than after the call: passwordCallback runs inside
	// NewServerConn, and a panic there is recovered by recoverHandshake, which
	// would skip a plain release and hold this slot for good.
	sconn, chans, reqs, err := func() (*ssh.ServerConn, <-chan ssh.NewChannel, <-chan *ssh.Request, error) {
		defer s.releasePreauth(slot)
		return ssh.NewServerConn(conn, s.sshConfig)
	}()
	if err != nil {
		// A failed handshake is routine on the public internet (scanners,
		// mistyped passwords) and not worth more than a debug-level trace, so
		// it bypasses Logf, whose default is a warning.
		slog.Debug("consolessh: handshake failed", "remote", conn.RemoteAddr().String(), "error", err)
		return
	}
	defer sconn.Close()
	_ = conn.SetDeadline(time.Time{})
	go ssh.DiscardRequests(reqs)

	actor, ok := actorFromPermissions(sconn.Permissions)
	if !ok {
		return
	}
	if !s.acquireAccountConn(actor.ID) {
		s.cfg.Logf(context.Background(), "consolessh: too many connections for this account", "remote", conn.RemoteAddr().String())
		return
	}
	defer s.releaseAccountConn(actor.ID)

	// A connection with no live session is in no slot, so nothing else would
	// end it: one that signs in and opens nothing, or that ran a one-shot exec
	// and went quiet, would otherwise stay open for as long as its client liked.
	idle := newConnIdle(s.sessionStartTimeout(), s.cfg.IdleTimeout, func() { _ = sconn.Close() })
	defer idle.stop()

	credential := credentialFromPermissions(sconn.Permissions)
	base := context.Background()
	if s.cfg.ConnectionContext != nil {
		base = s.cfg.ConnectionContext(base)
	}

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			// Port forwarding, X11, and anything else: this listener has
			// exactly one purpose, and accepting other channel types would
			// turn an authenticated console session into a general SSH
			// server.
			_ = newChannel.Reject(ssh.UnknownChannelType, "only a console session is available")
			continue
		}
		if !s.acquireAccountSlot(actor.ID) {
			_ = newChannel.Reject(ssh.ResourceShortage, "too many concurrent console sessions for this account")
			continue
		}
		if !s.acquireSlot() {
			s.releaseAccountSlot(actor.ID)
			_ = newChannel.Reject(ssh.ResourceShortage, "too many concurrent console sessions")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			s.releaseSlot()
			s.releaseAccountSlot(actor.ID)
			continue
		}

		sess := newSSHSession(s, sconn, channel, actor, ip, credential)
		sess.base = base
		if !s.addSession(sess) {
			// Shutdown has taken its snapshot of the sessions, so this one would
			// outlive it. Refuse it, and the connection goes with the handler.
			_ = channel.Close()
			s.releaseSlot()
			s.releaseAccountSlot(actor.ID)
			return
		}
		idle.sessionAdmitted()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.releaseSlot()
			defer s.releaseAccountSlot(actor.ID)
			defer s.removeSession(sess)
			defer idle.sessionEnded()
			defer recoverSession(sess)
			sess.serve(requests)
		}()
	}
}

// The defaults for the timeouts a Config leaves at zero. They are applied
// where the timeout is read, not in New, so a Server built without New, as some
// tests build one, gets the same values.
const (
	defaultHandshakeTimeout  = 30 * time.Second
	defaultSessionStart      = 30 * time.Second
	defaultWriteStallTimeout = 30 * time.Second
)

func (s *Server) handshakeTimeout() time.Duration {
	if s.cfg.HandshakeTimeout > 0 {
		return s.cfg.HandshakeTimeout
	}
	return defaultHandshakeTimeout
}

func (s *Server) sessionStartTimeout() time.Duration {
	if s.cfg.SessionStartTimeout > 0 {
		return s.cfg.SessionStartTimeout
	}
	return defaultSessionStart
}

func (s *Server) writeStallTimeout() time.Duration {
	if s.cfg.WriteStallTimeout > 0 {
		return s.cfg.WriteStallTimeout
	}
	return defaultWriteStallTimeout
}

// handshakeContext bounds one callback the handshake makes into the database.
// The connection deadline reaches the socket, not a query that is blocked
// inside Authenticate, so without this a stalled database pins a preauth slot
// for as long as it stalls, and up to MaxUnauthenticated of them.
func (s *Server) handshakeContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), s.handshakeTimeout())
}

// maxConnectionsPerAccount is how many connections one account may hold open at
// once, whether or not they have opened a session.
const maxConnectionsPerAccount = 8

// addConn registers a connection with Shutdown. It reports false once Shutdown
// has begun: the snapshot has been taken, so a connection registered after it
// would never be closed by Shutdown and would hold Wait until its own idle
// timeout.
func (s *Server) addConn(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if s.conns == nil {
		s.conns = make(map[net.Conn]struct{})
	}
	s.conns[conn] = struct{}{}
	return true
}

// connIdle ends a connection that has no live session. It starts on the
// session-start timeout, so a client that signs in and opens nothing is cut
// off. Admitting a session stops it; when the last live session ends it is
// re-armed with the idle timeout, which is the bound that governs a connection
// once it has had a session at all.
type connIdle struct {
	mu        sync.Mutex
	live      int
	timer     *time.Timer
	done      bool
	idle      time.Duration
	closeConn func()
}

func newConnIdle(start, idle time.Duration, closeConn func()) *connIdle {
	c := &connIdle{idle: idle, closeConn: closeConn}
	c.timer = time.AfterFunc(start, c.expire)
	return c
}

func (c *connIdle) sessionAdmitted() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.live++
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}

func (c *connIdle) sessionEnded() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.live--
	if c.live == 0 && !c.done {
		c.timer = time.AfterFunc(c.idle, c.expire)
	}
}

// expire holds the lock across the close so that a session admitted in the
// same instant cannot be cut off by a timer that had already decided to fire.
// closeConn never calls back into connIdle, so that cannot deadlock.
func (c *connIdle) expire() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.live == 0 && !c.done {
		c.closeConn()
	}
}

func (c *connIdle) stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.done = true
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}

func (s *Server) removeConn(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, conn)
}

func (s *Server) acquireSlot() bool {
	if s.sem == nil {
		return true
	}
	select {
	case s.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseSlot() {
	if s.sem == nil {
		return
	}
	<-s.sem
}

// acquireAccountConn counts an authenticated connection against its account. It
// is separate from the session cap because a connection that never opens a
// session holds a descriptor and a goroutine and is counted by neither.
func (s *Server) acquireAccountConn(accountID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return takeCount(&s.connsPerAccount, accountID, maxConnectionsPerAccount)
}

func (s *Server) releaseAccountConn(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	giveCount(s.connsPerAccount, accountID)
}

func (s *Server) acquireAccountSlot(accountID string) bool {
	if s.cfg.MaxSessionsPerAccount < 0 {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return takeCount(&s.perAccount, accountID, s.cfg.MaxSessionsPerAccount)
}

func (s *Server) releaseAccountSlot(accountID string) {
	if s.cfg.MaxSessionsPerAccount < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	giveCount(s.perAccount, accountID)
}

// takeCount and giveCount are the one spelling of a per-key cap that both
// account counters share, so a fix to one (an entry left at zero, a release
// that goes negative) cannot be made to only one of them. The caller holds
// s.mu.
func takeCount(counts *map[string]int, key string, limit int) bool {
	if (*counts)[key] >= limit {
		return false
	}
	if *counts == nil {
		*counts = make(map[string]int)
	}
	(*counts)[key]++
	return true
}

// An entry is deleted at its last release rather than left at zero, so the
// map holds the accounts connected now and not every account that ever was.
func giveCount(counts map[string]int, key string) {
	if counts[key] <= 1 {
		delete(counts, key)
		return
	}
	counts[key]--
}

// acquirePreauth reserves a handshake slot and returns the key it is counted
// against, which releasePreauth takes back. The key is derived here from the
// address rather than passed in: a caller that counted by the raw host would
// give each IPv6 address of a /64 its own share, although one client owns the
// whole /64.
func (s *Server) acquirePreauth(addr net.Addr) (string, bool) {
	key := preauthKey(addr)
	if s.cfg.MaxUnauthenticated < 0 {
		return key, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// The total is tested first, so a connection refused by the total takes
	// nothing from its address's share.
	if s.preauth >= s.cfg.MaxUnauthenticated || !takeCount(&s.preauthIP, key, maxPreauthPerIP) {
		return "", false
	}
	s.preauth++
	return key, true
}

func (s *Server) releasePreauth(key string) {
	if s.cfg.MaxUnauthenticated < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.preauth--
	giveCount(s.preauthIP, key)
}

// addSession registers a session for Shutdown to close. It reports false once
// Shutdown has begun, for the same reason addConn does.
func (s *Server) addSession(sess *sshSession) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.sessions[sess] = struct{}{}
	return true
}

func (s *Server) removeSession(sess *sshSession) {
	s.mu.Lock()
	delete(s.sessions, sess)
	s.mu.Unlock()
}

// hostOnly strips the port from a net.Addr so the console engine and its
// audit trail record the same shape of address httpx.ClientIP produces for
// the web transport.
func hostOnly(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}

// preauthKey is what a handshake slot is counted against. An IPv6 client holds
// a whole /64, so counting each address on its own would let one client take
// every handshake slot from eight addresses. The HTTP limiters key the same way.
func preauthKey(addr net.Addr) string {
	return httpx.RateKey(hostOnly(addr))
}

// --- one session: pty/window/env negotiation, then shell or exec ---

type sshSession struct {
	server  *Server
	conn    ssh.Conn // the whole connection, not just this channel — see close's doc comment
	channel ssh.Channel
	actor   user.User
	ip      string
	// The credential the connection was opened with. Set once at the handshake
	// and never refreshed: reauthorize compares the account's current value
	// against it, so refreshing it would forgive a change.
	credential string
	// What every command on this connection runs under: shared by all the
	// connection's channels, gone when it hangs up.
	base context.Context

	mu    sync.Mutex
	width int
	lang  string
	// json is what `format` leaves set. Like lang it has to outlive the line
	// that changed it: snapshot builds a fresh Session for every line, so
	// keepSettings copies both back after each command.
	json   bool
	hasPty bool
	cancel context.CancelFunc // set while a command is running

	closeOnce sync.Once
	// dead is closed by abort. A loop waiting on the session's events can then
	// see that it ended even when the goroutine that would have reported it
	// panicked and sent nothing.
	dead     chan struct{}
	deadOnce sync.Once
}

func newSSHSession(server *Server, conn ssh.Conn, channel ssh.Channel, actor user.User, ip, credential string) *sshSession {
	return &sshSession{
		server:     server,
		conn:       conn,
		channel:    channel,
		actor:      actor,
		ip:         ip,
		credential: credential,
		dead:       make(chan struct{}),
		base:       context.Background(),
		width:      100, // console.Session.Width: 0 means unknown, assume 100 — pick it up front rather than repeat the fallback at every render.
		lang:       "en",
	}
}

// closeChannel ends a finished one-shot command cleanly.
//
// The output is complete and the exit status is sent, so the right thing to
// close is this channel — not the connection underneath it. Closing the
// connection is a hang-up the client has to report, which is why every
// `ssh host 'user list'` used to print "Connection closed by remote host"
// underneath its own output, on stderr, on every scripted run. The client
// hangs up by itself once the channel is done.
//
// It shares closeOnce with close, so whichever end of the session arrives
// first wins and the other becomes a no-op.
func (sess *sshSession) closeChannel() {
	sess.closeOnce.Do(func() {
		_ = sess.stalled(func() error {
			_ = sess.channel.CloseWrite()
			return sess.channel.Close()
		})
	})
}

// errAccountNoLongerAdmin ends a session whose owner is no longer entitled to
// one. Deliberately one error for every reason — demoted, disabled, deleted —
// because the console has nothing useful to say about which.
var errAccountNoLongerAdmin = errors.New("consolessh: the account no longer has console access")

// errCredentialChanged ends a connection that was opened with a password the
// account no longer has. Its own error, so the person typing sees the reason:
// signing in again is what they can do about it.
var errCredentialChanged = errors.New("consolessh: the account's password changed")

func (s *Server) permitted(ctx context.Context, account user.User) (bool, error) {
	if s.cfg.Permitted == nil {
		return account.IsAdmin(), nil
	}
	return s.cfg.Permitted(ctx, account)
}

// errReauthorizeFailed is a lookup that could not be completed. It is not a
// decision about the account, so the session ends with a retry line rather
// than the revocation notice.
var errReauthorizeFailed = errors.New("consolessh: could not re-read the account")

// endNotice is what a connection says when its next command is refused, in the
// session's language.
func endNotice(lang string, err error) string {
	switch {
	case errors.Is(err, errCredentialChanged):
		return noticeCredentialChanged.For(lang)
	case errors.Is(err, errReauthorizeFailed):
		return noticeRetry.For(lang)
	}
	return noticeRevoked.For(lang)
}

// close ends the session by closing the whole SSH connection, not just this
// channel. ssh.Channel.Close only sends a close message and waits for the
// peer's own close in reply before a blocked local Read unblocks — the
// mux's teardown (golang.org/x/crypto/ssh's mux.loop, on the read side)
// only EOFs every channel's buffer once the underlying net.Conn itself
// errors out. Closing the connection is what makes Shutdown's deadline and
// the idle timeout actually deterministic instead of depending on a
// possibly-gone client to cooperate.
func (sess *sshSession) close() {
	sess.closeOnce.Do(sess.abort)
}

// abort ends the session without taking closeOnce. A close that is itself stuck
// in a write to a client that stopped reading holds closeOnce for as long as the
// write stays stuck, so a stall timer that went through close would wait on the
// very write it was armed to end.
func (sess *sshSession) abort() {
	sess.mu.Lock()
	cancel := sess.cancel
	sess.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	_ = sess.conn.Close()
	sess.deadOnce.Do(func() { close(sess.dead) })
}

// recoverSession is deferred by every goroutine that runs for a connection.
// The HTTP side is covered by httpx.Recover. Nothing covers these goroutines,
// and an unrecovered panic on any goroutine ends the whole process, the HTTP
// listener with it. Recovering here ends that one session instead.
func recoverSession(sess *sshSession) {
	r := recover()
	if r == nil {
		return
	}
	slog.Error("console session panicked", "panic", r, "stack", string(debug.Stack()))
	func() {
		// Telling the client must not be able to take the process down either.
		defer func() { _ = recover() }()
		_ = sess.stalled(func() error {
			_, err := sess.channel.Write([]byte(noticeInternalError.For(sess.language())))
			return err
		})
	}()
	sess.abort()
}

// recoverHandshake is the same for the goroutine that negotiates a connection
// before any session exists. The password check runs there.
func recoverHandshake(conn net.Conn) {
	r := recover()
	if r == nil {
		return
	}
	slog.Error("console handshake panicked", "panic", r, "stack", string(debug.Stack()))
	_ = conn.Close()
}

type ptyRequestPayload struct {
	Term      string
	Width     uint32
	Height    uint32
	PixWidth  uint32
	PixHeight uint32
	Modes     string
}

type windowChangePayload struct {
	Width, Height, PixWidth, PixHeight uint32
}

type envPayload struct {
	Name, Value string
}

type execPayload struct {
	Command string
}

func (sess *sshSession) serve(requests <-chan *ssh.Request) {
	// Closing the connection (not just the channel) is what guarantees the
	// background reader goroutines runInteractive/watchCancel spawn are not
	// left blocked on Read after this function returns — see sess.close's
	// doc comment for why Channel.Close alone would not do that promptly.
	defer sess.close()
	// Deferred after close, so it runs first: a panic on this goroutine, such as
	// one in runInteractive's loop, still gets its notice to the client, which
	// close would otherwise have shut before the write.
	defer recoverSession(sess)

	// A session that never asks for a shell or an exec holds its slots, the
	// global one and the account's, and nothing else would give them back until
	// the client chose to leave. Stopped by either request.
	started := time.AfterFunc(sess.server.sessionStartTimeout(), sess.close)
	defer started.Stop()

	for req := range requests {
		switch req.Type {
		case "pty-req":
			var payload ptyRequestPayload
			ok := ssh.Unmarshal(req.Payload, &payload) == nil
			if ok {
				sess.mu.Lock()
				sess.hasPty = true
				if payload.Width > 0 {
					sess.width = int(payload.Width)
				}
				sess.mu.Unlock()
			}
			sess.reply(req, ok)

		case "window-change":
			sess.windowChange(req)

		case "env":
			var payload envPayload
			ok := ssh.Unmarshal(req.Payload, &payload) == nil
			if ok {
				sess.applyEnv(payload.Name, payload.Value)
			}
			sess.reply(req, ok)

		case "shell":
			sess.reply(req, true)
			started.Stop()
			// runInteractive blocks, and the requests that keep arriving while it
			// runs need answering too. x/crypto queues each one in a 16-slot buffer
			// read by the connection's single mux loop, so the seventeenth unread
			// request would stop every packet behind it, keystrokes included.
			go sess.followRequests(requests)
			sess.runInteractive()
			return

		case "exec":
			var payload execPayload
			ok := ssh.Unmarshal(req.Payload, &payload) == nil
			sess.reply(req, ok)
			if !ok {
				return
			}
			started.Stop()
			sess.serveExec(requests, payload.Command)
			// Deliberately not sess.close(): see closeChannel.
			sess.closeChannel()
			return

		default:
			// subsystem (sftp included), break, x11-req, and anything
			// future clients invent: this session is a console, not a
			// shell, and the only requests it understands are the ones
			// handled above. A `signal` arriving here has no command to
			// interrupt; the one that does is handled in serveExec.
			sess.reply(req, false)
		}
	}
}

// windowChange records a resize. Both the shell and the one-shot loop need
// it, and the shell's copy of the request loop is followRequests.
func (sess *sshSession) windowChange(req *ssh.Request) {
	var payload windowChangePayload
	ok := ssh.Unmarshal(req.Payload, &payload) == nil
	if ok && payload.Width > 0 {
		sess.mu.Lock()
		sess.width = int(payload.Width)
		sess.mu.Unlock()
	}
	sess.reply(req, ok)
}

// followRequests answers the channel's requests for as long as the shell runs.
// A resize is still recorded, so commands see the terminal's current width,
// and everything else is refused as it is by serve.
func (sess *sshSession) followRequests(requests <-chan *ssh.Request) {
	defer recoverSession(sess)
	for req := range requests {
		if req.Type == "window-change" {
			sess.windowChange(req)
			continue
		}
		sess.reply(req, false)
	}
}

// serveExec runs one command while still answering the channel's requests.
//
// The command runs on its own goroutine rather than inline because this loop
// has to stay live underneath it: a client's interrupt may arrive as a
// `signal` request rather than as a byte on the channel, and a loop blocked
// inside the command would not read it until the command it was meant to stop
// had already finished.
func (sess *sshSession) serveExec(requests <-chan *ssh.Request, line string) {
	// A signal may arrive as soon as the exec request is acknowledged. Make
	// cancellation visible before the command goroutine starts, or that early
	// signal finds no cancel function and a long-running command continues.
	ctx, cancel := context.WithTimeout(sess.base, sess.server.cfg.ExecTimeout)
	sess.mu.Lock()
	sess.cancel = cancel
	sess.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer recoverSession(sess)
		sess.runExec(ctx, cancel, line)
	}()

	for {
		select {
		case <-done:
			return
		case req, open := <-requests:
			if !open {
				// The channel is gone: the client closed it, or the connection
				// died under it. Nobody is left to read what the command
				// prints, and one that waits on a ticker (`watch`) would
				// otherwise run on, holding this session's slot for good.
				cancel()
				<-done
				return
			}
			if req.Type == "signal" {
				var payload struct{ Name string }
				if ssh.Unmarshal(req.Payload, &payload) == nil && interrupting(payload.Name) {
					sess.interrupt()
				}
			}
			sess.reply(req, false)
		}
	}
}

// interrupting reports whether a signal name means "stop what you are doing".
// Anything else is ignored rather than guessed at: this is a console, and the
// only thing there is to signal is the command in front of it.
func interrupting(name string) bool {
	switch ssh.Signal(name) {
	case ssh.SIGINT, ssh.SIGTERM, ssh.SIGQUIT, ssh.SIGHUP:
		return true
	default:
		return false
	}
}

// interrupt cancels the running command without ending the session, which is
// what Ctrl-C means. Ending the session is close's job.
func (sess *sshSession) interrupt() {
	sess.mu.Lock()
	cancel := sess.cancel
	sess.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (sess *sshSession) reply(req *ssh.Request, ok bool) {
	if req.WantReply {
		_ = sess.stalled(func() error { return req.Reply(ok, nil) })
	}
}

// stalled runs one write to the client under a timer. A client that has stopped
// reading leaves its write blocked in the SSH window reservation, which has no
// deadline and no context, so nothing else in the session can end it. Closing
// the connection is what releases that write, and the timer does it once a
// write has taken longer than the server's WriteStallTimeout.
func (sess *sshSession) stalled(write func() error) error {
	timer := time.AfterFunc(sess.server.writeStallTimeout(), sess.abort)
	defer timer.Stop()
	return write()
}

// output is the client's main stream with every write under stalled, and
// errOutput is its stderr.
func (sess *sshSession) output() io.Writer {
	return clientStream{sess: sess, stream: sess.channel}
}

func (sess *sshSession) errOutput() io.Writer {
	return clientStream{sess: sess, stream: sess.channel.Stderr()}
}

type clientStream struct {
	sess   *sshSession
	stream io.Writer
}

func (c clientStream) Write(p []byte) (int, error) {
	var n int
	err := c.sess.stalled(func() error {
		var err error
		n, err = c.stream.Write(p)
		return err
	})
	return n, err
}

// exitStatus tells the client how its command ended, under the same bound.
func (sess *sshSession) exitStatus(status uint32) {
	_ = sess.stalled(func() error {
		_, err := sess.channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
		return err
	})
}

// langFromEnv reads a LANG or LC_ALL value as the console's language. Anything
// starting with zh is Chinese and anything starting with en is English; the
// console only distinguishes those two, matching Session.Lang and web/src/i18n.
// Any other locale says nothing, so the caller keeps the language it had.
func langFromEnv(value string) (string, bool) {
	v := strings.ToLower(value)
	if strings.HasPrefix(v, "zh") {
		return "zh", true
	}
	if strings.HasPrefix(v, "en") {
		return "en", true
	}
	return "", false
}

func (sess *sshSession) applyEnv(name, value string) {
	if name != "LANG" && name != "LC_ALL" {
		return
	}
	lang, ok := langFromEnv(value)
	if !ok {
		return
	}
	sess.mu.Lock()
	sess.lang = lang
	sess.mu.Unlock()
}

// currentSession is snapshot with the account re-read first. Every command
// goes through this rather than snapshot, so a grant revoked while somebody
// sits at a prompt takes effect on their next command instead of whenever
// they get round to closing the connection.
//
// The session it returns carries the same re-read for a command that keeps
// running past this line: a watch asks before each of its runs.
//
// A failure ends the session: the account is gone, disabled, or no longer an
// administrator, and none of those should keep a console open.
func (sess *sshSession) currentSession(ctx context.Context, transport string) (*console.Session, error) {
	if _, err := sess.reauthorize(ctx); err != nil {
		return nil, err
	}
	current := sess.snapshot(transport)
	current.Reauthorize = sess.reauthorize
	return current, nil
}

// reauthorize is the one check a fresh command and a running watch both pass
// before they act, so the two cannot disagree about who may keep going.
func (sess *sshSession) reauthorize(ctx context.Context) (user.User, error) {
	account, err := sess.server.cfg.Reauthorize(ctx, sess.actorID())
	if err != nil {
		// Only a row that is not there means the account is gone. A database
		// error, or a cancelled context, says nothing about the account, and
		// reporting it as a revocation would tell an administrator that access
		// was taken away when it was not.
		if errors.Is(err, user.ErrNotFound) || database.IsNotFound(err) {
			return user.User{}, errAccountNoLongerAdmin
		}
		slog.Error("console could not re-read its account", "error", err)
		return user.User{}, errReauthorizeFailed
	}
	if !account.IsActive() {
		return user.User{}, errAccountNoLongerAdmin
	}
	// A rule that cannot be evaluated says nothing about the account. Only a
	// rule that answers no ends the session as a revocation.
	permitted, err := sess.server.permitted(ctx, account)
	if err != nil {
		slog.Error("console could not re-read its permission", "error", err)
		return user.User{}, errReauthorizeFailed
	}
	if !permitted {
		return user.User{}, errAccountNoLongerAdmin
	}
	if sess.server.cfg.Credential != nil {
		current, err := sess.server.cfg.Credential(ctx, account.ID)
		if err != nil {
			slog.Error("console could not re-read its credential", "error", err)
			return user.User{}, errReauthorizeFailed
		}
		if current != sess.credential {
			return user.User{}, errCredentialChanged
		}
	}

	sess.mu.Lock()
	sess.actor = account
	sess.mu.Unlock()
	return account, nil
}

func (sess *sshSession) actorID() string {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.actor.ID
}

// language is the session's language for the notices this package writes. It is
// read under the same lock applyEnv writes it under.
func (sess *sshSession) language() string {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.lang
}

func (sess *sshSession) snapshot(transport string) *console.Session {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return &console.Session{
		Actor:     sess.actor,
		Transport: transport,
		IP:        sess.ip,
		Width:     sess.width,
		Colour:    sess.hasPty,
		Lang:      sess.lang,
		JSON:      sess.json,
	}
}

// keepSettings carries what `format` and `lang` left on the command's session
// back onto this channel's session, so the next line's snapshot sees them. A
// session is made per channel, so another channel on the same connection starts
// from the defaults. Only those two settings are the command's to change.
func (sess *sshSession) keepSettings(current *console.Session) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.json = current.JSON
	sess.lang = current.Lang
}

// runExec is `ssh admin@host 'user list --json'`: one line, no pty, the
// channel's own exit status carries the verdict so `ssh ... | jq` and
// similar scripting works. Never anything but console.Console.Execute —
// this is the one function in the package that could be mistaken for a
// shell, and it is not one.
func (sess *sshSession) runExec(ctx context.Context, cancel context.CancelFunc, line string) {
	defer cancel()

	consoleSession, err := sess.currentSession(ctx, "ssh")
	if err != nil {
		_, _ = sess.output().Write([]byte(endNotice(sess.language(), err)))
		sess.exitStatus(1)
		return
	}
	consoleSession.Colour = false

	go sess.watchCancel(ctx, cancel)

	result := sess.server.cfg.Console.Execute(ctx, consoleSession, sess.output(), line)

	status := uint32(0)
	if !result.OK {
		status = 1
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status = 1
		_, _ = fmt.Fprintf(sess.errOutput(), noticeCommandStopped.For(sess.language()), sess.server.cfg.ExecTimeout)
	}
	sess.exitStatus(status)
}

// runInteractive is the pty path: banner, prompt, line editor, until `exit`,
// `quit`, Ctrl-D on an empty line, or the idle timeout closes it.
//
// One goroutine owns every read of the channel, whether the byte it just
// read is an edit keystroke or a Ctrl-C that should cancel a running
// command. A second reader watching for Ctrl-C independently would race
// this one for bytes already sitting in the line editor's buffered reader
// — see lineEditor.Start's doc comment — so instead this loop asks the
// editor for one rune at a time and decides, based on whether a command is
// currently running, whether that rune is line-editing input or a cancel
// signal.
func (sess *sshSession) runInteractive() {
	engine := sess.server.cfg.Console
	lang := sess.language()
	_, _ = sess.output().Write([]byte(engine.Banner(sess.snapshot("ssh"))))

	complete := func(line string, pos int) (int, []editorCompletion) {
		c := engine.Complete(context.Background(), sess.snapshot("ssh"), line, pos)
		items := make([]editorCompletion, len(c.Items))
		for i, item := range c.Items {
			items[i] = editorCompletion{Value: item.Value, Label: item.Label}
		}
		return c.From, items
	}
	editor := newLineEditor(sess.channel, sess.output(), "> ", complete, newHistory())
	editor.lang = lang
	editor.Start()

	type readEvent struct {
		r   rune
		err error
	}
	reads := make(chan readEvent)
	// Closed when this function returns. The session's end closes the
	// connection, which makes the next read fail, and with nobody left
	// receiving that failure the goroutine below would block on its send for
	// the life of the process.
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		defer recoverSession(sess)
		for {
			r, err := editor.NextRune()
			select {
			case reads <- readEvent{r, err}:
			case <-quit:
				return
			}
			if err != nil {
				return
			}
		}
	}()

	type execEvent struct{ result console.Result }
	results := make(chan execEvent, 1)

	var cancel context.CancelFunc
	// Every return path must end a command still running. Once the loop is gone
	// nothing reads what the command prints, and the session's slots are released
	// when serve returns, so a command left to finish would outlive them.
	defer func() {
		if cancel != nil {
			cancel()
		}
	}()
	running := false
	var typeAhead typeAheadQueue
	idle := sess.server.cfg.IdleTimeout
	idleTimer := time.NewTimer(idle)
	defer idleTimer.Stop()
	// restartIdle is safe whether or not the timer has fired. The idle case
	// does not drain the channel when a command is running, so a blocking
	// drain here would wait on a value that was already taken.
	restartIdle := func() {
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
		idleTimer.Reset(idle)
	}

	startCommand := func(line string) {
		runCtx, runCancel := context.WithCancel(sess.base)
		cancel = runCancel
		// abort can only reach a command through sess.cancel. Without this a session
		// that dies mid-command releases its slots while the command keeps running.
		sess.mu.Lock()
		sess.cancel = runCancel
		sess.mu.Unlock()
		running = true
		// Shutdown waits for the command too. The session's own goroutine is
		// counted in s.wg and still running here, so this Add cannot race a Wait.
		sess.server.wg.Add(1)
		go func(line string) {
			defer sess.server.wg.Done()
			defer recoverSession(sess)
			current, authErr := sess.currentSession(runCtx, "ssh")
			if authErr != nil {
				_, _ = sess.output().Write([]byte(endNotice(lang, authErr)))
				results <- execEvent{result: console.Result{Exit: true}}
				return
			}
			result := engine.Execute(runCtx, current, sess.output(), line)
			// Before the send: the loop takes its next snapshot only after it
			// receives this result, so the next line is guaranteed to see it.
			sess.keepSettings(current)
			results <- execEvent{result}
		}(line)
	}

	// feedEditor gives one key to the line editor. It reports true when the
	// session is over, which is Ctrl-D on an empty line.
	feedEditor := func(r rune) (exit bool) {
		line, done, eof := editor.Feed(r)
		if eof {
			return true
		}
		if !done {
			return false
		}
		if strings.TrimSpace(line) == "" {
			editor.Start()
			return false
		}
		startCommand(line)
		return false
	}

	// drainTypeAhead feeds the keys typed during the last command until one of
	// them submits a line, which starts the next command. The rest keep waiting.
	drainTypeAhead := func() (exit bool) {
		for !running {
			r, ok := typeAhead.pop()
			if !ok {
				break
			}
			if feedEditor(r) {
				return true
			}
		}
		// Only once the queue is empty and nothing is running is the editor's
		// line the one the discarded keys belong to. If a command started
		// instead, the discarded keys are in the line after it, and the next
		// drain or the next results event refuses that one.
		if !running && typeAhead.takeLost() {
			editor.refuseOnEnter()
		}
		return false
	}

	for {
		select {
		case ev := <-reads:
			restartIdle()

			if ev.err != nil {
				// io.EOF (Ctrl-D on an empty line) or the channel closing
				// underneath the reader: either way, the session is over.
				if cancel != nil {
					cancel()
				}
				return
			}

			if running {
				// A command is running: the editor is not consuming input.
				// Ctrl-C cancels that command without ending the session and
				// drops what was typed ahead of it. Everything else waits, up
				// to the same bound a line has, so that one paste cannot grow
				// the queue without limit.
				if ev.r == 0x03 {
					if cancel != nil {
						cancel()
					}
					typeAhead.clear()
					continue
				}
				if typeAhead.push(ev.r) {
					_, _ = sess.output().Write([]byte(noticeTypeAhead.For(lang)))
				}
				continue
			}

			if feedEditor(ev.r) {
				return
			}

		case ev := <-results:
			running = false
			if cancel != nil {
				cancel()
				cancel = nil
			}
			typeAhead.commandEnded()
			if ev.result.Exit {
				return
			}
			editor.Start()
			// The idle clock starts when the command ends, not when the last key
			// before it was typed.
			restartIdle()
			if drainTypeAhead() {
				return
			}

		case <-idleTimer.C:
			// A command still working is not an idle session. Its output is
			// the client's activity, and cutting it off here killed long
			// imports started from the shell.
			if running {
				continue
			}
			_, _ = sess.output().Write([]byte(noticeIdle.For(lang)))
			return

		case <-sess.dead:
			// The session was ended from elsewhere, such as a goroutine that
			// panicked. Its reader may never send the event that would say so.
			return
		}
	}
}

// typeAheadQueue holds the keys typed while a command runs, to be fed to the
// editor when it returns. A paste of several lines would otherwise lose every
// line after the first, and whether a later line survived depended on how long
// the command took. It belongs to runInteractive's loop and is never shared,
// so it takes no lock.
type typeAheadQueue struct {
	keys []rune
	// full is set once per command, so the notice that keys are being
	// dropped is printed once rather than for every key of a long paste.
	full bool
	// lost is set when a key was discarded because the queue was full. The
	// discarded keys continue whatever line the queue ends in, so that line
	// must be refused on Enter, not run as the truncated prefix it now is.
	lost bool
}

// push queues a key, up to the same bound a line has, so that one paste
// cannot grow the queue without limit. It reports true the first time a key
// is dropped during the current command, which is when to say so.
func (q *typeAheadQueue) push(r rune) (firstDrop bool) {
	if len(q.keys) < maxLineRunes {
		q.keys = append(q.keys, r)
		return false
	}
	q.lost = true
	if q.full {
		return false
	}
	q.full = true
	return true
}

// clear is Ctrl-C during a command: what was typed ahead of it goes with it,
// and so does the refusal owed to a truncated line that is no longer there.
// full stays, because the notice has already been printed for this command.
func (q *typeAheadQueue) clear() {
	q.keys = nil
	q.lost = false
}

// commandEnded lets the next command print the drop notice again.
func (q *typeAheadQueue) commandEnded() {
	q.full = false
}

func (q *typeAheadQueue) pop() (rune, bool) {
	if len(q.keys) == 0 {
		return 0, false
	}
	r := q.keys[0]
	q.keys = q.keys[1:]
	return r, true
}

// takeLost reports, once, that the queue has emptied into a line some of whose
// keys were dropped. While keys remain, the truncated line is still ahead.
func (q *typeAheadQueue) takeLost() bool {
	if len(q.keys) > 0 || !q.lost {
		return false
	}
	q.lost = false
	return true
}

// watchCancel watches a one-shot command's channel for an interrupt. The
// exec path never constructs a lineEditor — there is nothing to edit in a
// one-shot command — so there is only the one reader here and no race to
// avoid.
//
// Only Ctrl-C cancels. The end of the client's input does not, and that
// distinction is the whole reason this function has a comment: `ssh host
// 'user list'` sends no input at all, so the first thing this read returns
// is io.EOF, immediately, before the command has done anything. Treating
// that as an interrupt cancelled the request context of every scripted
// command — which surfaced as "context canceled" from whatever query the
// command had just reached, and never once from the browser, because the
// web transport has no stdin to end.
//
// A client that actually goes away is a different event and is already
// handled: sshSession.close cancels this same context when the connection
// tears down.
func (sess *sshSession) watchCancel(ctx context.Context, cancel context.CancelFunc) {
	buf := make([]byte, 1)
	for {
		n, err := sess.channel.Read(buf)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			// The end of the client's input is not an interrupt (above), but
			// a channel that failed is the client gone, and the command has
			// nobody to run for.
			if !errors.Is(err, io.EOF) {
				cancel()
			}
			return
		}
		if n > 0 && buf[0] == 0x03 {
			cancel()
			return
		}
	}
}
